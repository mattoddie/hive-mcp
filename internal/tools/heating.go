package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mattoddie/hive-mcp/internal/hive"
)

type zoneArgs struct {
	Zone string `json:"zone,omitempty" jsonschema:"Heating zone or TRV zone, by name (for example \"Heating\" or \"Bedroom\") or ID. Can be left out when there's only one. See hive_get_heating"`
}

type getHeatingArgs struct {
	Zone string `json:"zone,omitempty" jsonschema:"Only this zone, by name or ID (default every heating zone and TRV zone)"`
	rawArgs
	freshArgs
}

type setTempArgs struct {
	zoneArgs
	Temperature float64 `json:"temperature" jsonschema:"Target temperature in °C, from 5 in steps of 0.5, up to HIVE_MAX_TEMP (see the schema maximum)"`
	Mode        string  `json:"mode,omitempty" jsonschema:"Also switch mode: manual (hold this temperature) or schedule (just until the next scheduled change, as turning the dial does)"`
	confirmArgs
}

type setModeArgs struct {
	zoneArgs
	Mode        string   `json:"mode" jsonschema:"schedule (follow the heating schedule), manual (hold one temperature) or off (frost protection only)"`
	Temperature *float64 `json:"temperature,omitempty" jsonschema:"With manual: the temperature to hold, in °C (from 5 in steps of 0.5, up to HIVE_MAX_TEMP). Default: the zone's current target"`
	confirmArgs
}

type boostHeatingArgs struct {
	zoneArgs
	Temperature float64 `json:"temperature" jsonschema:"Temperature to boost to, in °C (from 5 in steps of 0.5, up to HIVE_MAX_TEMP)"`
	Minutes     *int    `json:"minutes,omitempty" jsonschema:"How long to boost for, in minutes (default 60, at most 360)"`
}

type setHeatingScheduleArgs struct {
	zoneArgs
	Days map[string][]heatingPeriod `json:"days" jsonschema:"The new periods for the days that change, keyed by day (monday ... sunday) or group (weekdays, weekend, every_day); a day wins over a group. Days left out keep their periods"`
}

type setHeatOnDemandArgs struct {
	Zone    string `json:"zone,omitempty" jsonschema:"Heating zone (a Hive thermostat, not a TRV zone), by name or ID. Can be left out when there's only one"`
	Enabled bool   `json:"enabled" jsonschema:"true to let radiator valves turn the boiler on when their rooms need heat, false to stop them"`
}

var heatingModes = map[string]string{"schedule": "SCHEDULE", "manual": "MANUAL", "off": "OFF"}

func registerHeating(c *Context) {
	cl := c.Client
	summarize := func(n *hive.Nodes, p Obj) Obj { return n.SummarizeHeating(p, time.Now()) }
	zone := func(ctx context.Context, ref string, fresh bool) (*hive.Nodes, Obj, error) {
		n, err := c.nodes(ctx, fresh)
		if err != nil {
			return nil, nil, err
		}
		p, err := find(n.Products(hive.HeatingTypes...), ref, "heating zone")
		return n, p, err
	}

	add(c, Def{
		Toolset: "heating", Name: "hive_get_heating", Title: "Get heating",
		Description: "Show the heating: for each heating zone (a Hive thermostat) and TRV zone (Hive radiator valves), the current temperature, target temperature, mode (schedule, manual, off or boost), boost time left, whether it's calling for heat, what the schedule asks for now and next, and thermostat or valve batteries.",
	}, func(ctx context.Context, a getHeatingArgs) (any, error) {
		n, err := c.nodes(ctx, a.Fresh)
		if err != nil {
			return nil, err
		}
		list := n.Products(hive.HeatingTypes...)
		if a.Zone != "" {
			p, err := find(list, a.Zone, "heating zone")
			if err != nil {
				return nil, err
			}
			list = []Obj{p}
		}
		var out []Obj
		for _, p := range list {
			if a.Raw {
				out = append(out, redactObj(p))
			} else {
				out = append(out, n.SummarizeHeating(p, time.Now()))
			}
		}
		if out == nil {
			return nil, fmt.Errorf("there is no heating on this Hive account")
		}
		return listResult(out), nil
	})

	add(c, Def{
		Toolset: "heating", Name: "hive_get_heating_schedule", Title: "Get heating schedule",
		Description: "Show a zone's weekly heating schedule: for each day, the periods and the target temperature for each, in the home's local time. Also says what the schedule asks for now and when it next changes.",
	}, func(ctx context.Context, a zoneArgs) (any, error) {
		n, p, err := zone(ctx, a.Zone, false)
		if err != nil {
			return nil, err
		}
		sched, _ := hive.Get(p, "state", "schedule").(Obj)
		if len(sched) == 0 {
			return nil, fmt.Errorf("%s has no schedule", hive.Name(p))
		}
		return Obj{"zone": hive.Name(p), "mode": strings.ToLower(hive.Str(p, "state", "mode")), "time_zone": n.Location().String(),
			"now": n.ScheduleNow(p, time.Now()), "week": hive.FormatSchedule(sched)}, nil
	})

	add(c, Def{
		Toolset: "heating", Name: "hive_set_heating_schedule", Title: "Set heating schedule",
		Description: "Change a heating zone's or TRV zone's weekly schedule. In days, give the periods for the days that change, keyed by day or by weekdays, weekend or every_day (a day wins over a group); other days keep their current periods. Each day has 1 to 6 periods, each a start time (HH:MM, home's local time) and a target temperature, lasting until the next period starts (before a day's first period, the previous day's last one carries on). " +
			"This replaces those days' schedule for good, so show the user the new periods and get their go-ahead first. The zone only follows it in schedule mode.",
		Gate: Write, Idempotent: true, Destructive: true,
		Ranges: map[string][2]float64{"target_temperature": c.tempRange()},
		Keys:   map[string][]string{"days": dayKeyNames},
	}, func(ctx context.Context, a setHeatingScheduleArgs) (any, error) {
		byDay, err := expandDays(a.Days)
		if err != nil {
			return nil, err
		}
		changed := map[string][]slot{}
		for day, periods := range byDay {
			for _, p := range periods {
				start, err := parseClock(p.From)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", day, err)
				}
				if err := checkTemperature(day+" "+p.From+" target_temperature", p.Temperature, c.maxTemp()); err != nil {
					return nil, err
				}
				changed[day] = append(changed[day], slot{start, Obj{"target": p.Temperature}})
			}
			if len(periods) == 0 {
				changed[day] = nil
			}
		}
		_, p, err := zone(ctx, a.Zone, true)
		if err != nil {
			return nil, err
		}
		week, days, err := buildSchedule(p, changed)
		if err != nil {
			return nil, err
		}
		out, err := setSchedule(ctx, c, p, week, days)
		if err == nil && hive.Str(p, "state", "mode") != "SCHEDULE" {
			out.(Obj)["note_mode"] = fmt.Sprintf("%s is in %s mode, so it won't follow the schedule until it's switched to schedule (hive_set_heating_mode).", hive.Name(p), strings.ToLower(hive.Str(p, "state", "mode")))
		}
		return out, err
	})

	add(c, Def{
		Toolset: "heating", Name: "hive_list_trvs", Title: "List radiator valves",
		Description: "List the Hive radiator valves (TRVs) by zone: each zone's temperature, target and mode, and each valve's battery, signal, online status, calibration, mounting and child lock.",
	}, func(ctx context.Context, a freshArgs) (any, error) {
		n, err := c.nodes(ctx, a.Fresh)
		if err != nil {
			return nil, err
		}
		var zones []Obj
		for _, p := range n.Products("trvcontrol") {
			z := n.SummarizeHeating(p, time.Now())
			var valves []Obj
			ids, _ := hive.Get(p, "props", "trvs").([]any)
			for _, id := range ids {
				if d := n.Device(fmt.Sprint(id)); d != nil {
					valves = append(valves, n.SummarizeDevice(d))
				}
			}
			delete(z, "batteries")
			z["valves"] = valves
			zones = append(zones, z)
		}
		out := listResult(zones)
		if zones == nil {
			out["note"] = "No Hive radiator valves on this account."
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "heating", Name: "hive_set_heating_temperature", Title: "Set heating temperature",
		Description: "Set a zone's target temperature. In schedule mode this lasts until the next scheduled change, like turning the thermostat dial; pass mode=manual to hold it. If the zone is off, the target is saved but nothing heats until the mode changes. Holding a temperature in manual mode (mode=manual, or a zone that is already in manual) lasts indefinitely, so it needs confirm: true; prefer hive_boost_heating for a temporary change.",
		Gate:        Write, Idempotent: true,
		Enums:  map[string][]string{"mode": {"manual", "schedule"}},
		Ranges: map[string][2]float64{"temperature": c.tempRange()},
	}, func(ctx context.Context, a setTempArgs) (any, error) {
		if err := checkTemperature("temperature", a.Temperature, c.maxTemp()); err != nil {
			return nil, err
		}
		_, p, err := zone(ctx, a.Zone, false)
		if err != nil {
			return nil, err
		}
		body := Obj{"target": a.Temperature}
		if a.Mode == "manual" || (a.Mode == "" && hive.Str(p, "state", "mode") == "MANUAL") {
			if err := needConfirm(a.Confirm, fmt.Sprintf("Manual mode at %g °C", a.Temperature), "hive_boost_heating"); err != nil {
				return nil, err
			}
		}
		if a.Mode != "" {
			body["mode"] = heatingModes[a.Mode]
		}
		out, err := change(ctx, cl, p, body, summarize)
		if err == nil && a.Mode == "" && hive.Str(p, "state", "mode") == "OFF" {
			out.(Obj)["warning"] = "This zone is off, so it won't heat to the new target until its mode is changed (hive_set_heating_mode)."
		}
		return out, err
	})

	add(c, Def{
		Toolset: "heating", Name: "hive_set_heating_mode", Title: "Set heating mode",
		Description: "Switch a zone between schedule (follow the weekly schedule), manual (hold one temperature) and off (frost protection only). Cancels a boost. Manual lasts indefinitely, so it needs confirm: true; prefer hive_boost_heating for a temporary change.",
		// Destructive: it can turn the heating off.
		Gate: Write, Idempotent: true, Destructive: true,
		Enums:  map[string][]string{"mode": {"schedule", "manual", "off"}},
		Ranges: map[string][2]float64{"temperature": c.tempRange()},
	}, func(ctx context.Context, a setModeArgs) (any, error) {
		mode, ok := heatingModes[a.Mode]
		if !ok {
			return nil, fmt.Errorf("mode must be schedule, manual or off")
		}
		body := Obj{"mode": mode}
		if a.Temperature != nil {
			if a.Mode != "manual" {
				return nil, fmt.Errorf("temperature only goes with mode manual; use hive_set_heating_temperature otherwise")
			}
			if err := checkTemperature("temperature", *a.Temperature, c.maxTemp()); err != nil {
				return nil, err
			}
			body["target"] = *a.Temperature
		}
		_, p, err := zone(ctx, a.Zone, false)
		if err != nil {
			return nil, err
		}
		if a.Mode == "manual" {
			target, _ := hive.Num(p, "state", "target")
			if a.Temperature != nil {
				target = *a.Temperature
			} else if err := checkTemperature("the zone's current target", target, c.maxTemp()); err != nil {
				return nil, fmt.Errorf("%w; pass a temperature to hold", err)
			}
			if err := needConfirm(a.Confirm, fmt.Sprintf("Manual mode at %g °C", target), "hive_boost_heating"); err != nil {
				return nil, err
			}
		}
		return change(ctx, cl, p, body, summarize)
	})

	add(c, Def{
		Toolset: "heating", Name: "hive_boost_heating", Title: "Boost heating",
		Description: "Boost a zone: heat to a temperature for a number of minutes, then go back to the previous mode. Replaces any boost already running.",
		Gate:        Write,
		Ranges:      map[string][2]float64{"temperature": c.tempRange(), "minutes": {1, 360}},
	}, func(ctx context.Context, a boostHeatingArgs) (any, error) {
		if err := checkTemperature("temperature", a.Temperature, c.maxTemp()); err != nil {
			return nil, err
		}
		minutes := 60
		if a.Minutes != nil {
			minutes = *a.Minutes
		}
		if minutes < 1 || minutes > 360 {
			return nil, fmt.Errorf("minutes must be between 1 and 360")
		}
		_, p, err := zone(ctx, a.Zone, false)
		if err != nil {
			return nil, err
		}
		return change(ctx, cl, p, Obj{"mode": "BOOST", "boost": minutes, "target": a.Temperature}, summarize)
	})

	add(c, Def{
		Toolset: "heating", Name: "hive_cancel_heating_boost", Title: "Cancel heating boost",
		Description: "Stop a zone's boost early and go back to the mode (and, for manual or off, the temperature) it had before.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a zoneArgs) (any, error) {
		// Fresh, because what to go back to comes from the zone's current state.
		_, p, err := zone(ctx, a.Zone, true)
		if err != nil {
			return nil, err
		}
		body, note, err := cancelBoostBody(p, true)
		if err != nil {
			return nil, err
		}
		out, err := change(ctx, cl, p, body, summarize)
		if err == nil && note != "" {
			out.(Obj)["warning"] = note
		}
		return out, err
	})

	add(c, Def{
		Toolset: "heating", Name: "hive_set_heat_on_demand", Title: "Set heat on demand",
		Description: "Turn heat on demand on or off for a heating zone. With it on, Hive radiator valves can turn the boiler on through this zone when their rooms are below target, even when the thermostat isn't calling for heat (Hive shows this as a boost with boost_reason \"heat on demand\"). The setting stays until changed.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a setHeatOnDemandArgs) (any, error) {
		n, err := c.nodes(ctx, false)
		if err != nil {
			return nil, err
		}
		p, err := find(n.Products("heating"), a.Zone, "heating zone with a thermostat")
		if err != nil {
			return nil, err
		}
		if !supportsHeatOnDemand(p) {
			return nil, fmt.Errorf("%s doesn't have heat on demand; it needs Hive radiator valves on the same hub", hive.Name(p))
		}
		state := "DISABLED"
		if a.Enabled {
			state = "ENABLED"
		}
		// As pyhiveapi's set_heat_on_demand sends it.
		return change(ctx, cl, p, Obj{"autoBoost": state}, summarize)
	})
}

// supportsHeatOnDemand reports whether a heating zone has the heat on demand
// setting: Hive reports it in state.autoBoost, and lists the capability.
func supportsHeatOnDemand(p Obj) bool {
	if hive.Str(p, "type") != "heating" {
		return false
	}
	if hive.Str(p, "state", "autoBoost") != "" {
		return true
	}
	caps, _ := hive.Get(p, "props", "capabilities").([]any)
	for _, c := range caps {
		if c == "TRV_AUTO_BOOST_READY" {
			return true
		}
	}
	return false
}

// cancelBoostBody works out what a product goes back to after a boost, as
// the Hive app and pyhiveapi do: the previous mode, and for heating in
// manual or off, the previous target.
func cancelBoostBody(p Obj, heating bool) (Obj, string, error) {
	if hive.Str(p, "state", "mode") != "BOOST" {
		if b, _ := hive.Num(p, "state", "boost"); b <= 0 {
			return nil, "", fmt.Errorf("%s isn't boosting", hive.Name(p))
		}
	}
	prev := hive.Str(p, "props", "previous", "mode")
	if prev == "" || prev == "BOOST" {
		prev = "SCHEDULE"
	}
	body := Obj{"mode": prev}
	note := ""
	if heating && (prev == "MANUAL" || prev == "OFF") {
		if t, ok := hive.Num(p, "props", "previous", "target"); ok {
			body["target"] = t
		} else {
			body["target"] = 7.0
			if prev == "MANUAL" {
				note = "Hive didn't say what the temperature was before the boost, so it's now holding 7 °C. Set the temperature you want with hive_set_heating_temperature."
			}
		}
	}
	return body, note, nil
}
