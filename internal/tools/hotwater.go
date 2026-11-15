package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mattoddie/hive-mcp/internal/hive"
)

type setHotWaterArgs struct {
	Mode string `json:"mode" jsonschema:"on (stay on until changed), off, or schedule (follow the hot water schedule)"`
	confirmArgs
}

type boostHotWaterArgs struct {
	Minutes *int `json:"minutes,omitempty" jsonschema:"How long to heat the water for, in minutes (default 60, at most 360)"`
}

type setHotWaterScheduleArgs struct {
	Days map[string][]hotWaterPeriod `json:"days" jsonschema:"The new periods for the days that change, keyed by day (monday ... sunday) or group (weekdays, weekend, every_day); a day wins over a group. Days left out keep their periods"`
}

var hotWaterModes = map[string]string{"on": "MANUAL", "off": "OFF", "schedule": "SCHEDULE"}

func registerHotWater(c *Context) {
	cl := c.Client
	summarize := func(n *hive.Nodes, p Obj) Obj { return n.SummarizeHotWater(p, time.Now()) }
	hotWater := func(ctx context.Context, fresh bool) (*hive.Nodes, Obj, error) {
		n, err := c.nodes(ctx, fresh)
		if err != nil {
			return nil, nil, err
		}
		p, err := find(n.Products(hive.HotWaterType), "", "hot water control")
		return n, p, err
	}

	add(c, Def{
		Toolset: "hotwater", Name: "hive_get_hot_water", Title: "Get hot water",
		Description: "Show the hot water: whether it's heating now, the mode (schedule, on, off or boost), boost time left, and what the schedule asks for now and next.",
	}, func(ctx context.Context, a rawFreshArgs) (any, error) {
		n, p, err := hotWater(ctx, a.Fresh)
		if err != nil {
			return nil, err
		}
		if a.Raw {
			return redactObj(p), nil
		}
		return n.SummarizeHotWater(p, time.Now()), nil
	})

	add(c, Def{
		Toolset: "hotwater", Name: "hive_get_hot_water_schedule", Title: "Get hot water schedule",
		Description: "Show the weekly hot water schedule: for each day, when the hot water is on and off, in the home's local time.",
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		n, p, err := hotWater(ctx, false)
		if err != nil {
			return nil, err
		}
		sched, _ := hive.Get(p, "state", "schedule").(Obj)
		if len(sched) == 0 {
			return nil, fmt.Errorf("the hot water has no schedule")
		}
		return Obj{"time_zone": n.Location().String(), "now": n.ScheduleNow(p, time.Now()), "week": hive.FormatSchedule(sched)}, nil
	})

	add(c, Def{
		Toolset: "hotwater", Name: "hive_set_hot_water_schedule", Title: "Set hot water schedule",
		Description: "Change the weekly hot water schedule. In days, give the periods for the days that change, keyed by day or by weekdays, weekend or every_day (a day wins over a group); other days keep their current periods. Each day has 1 to 6 periods, each a start time (HH:MM, home's local time) and on or off, lasting until the next period starts (before a day's first period, the previous day's last one carries on). " +
			"This replaces those days' schedule for good, so show the user the new periods and get their go-ahead first. The hot water only follows it in schedule mode.",
		Gate: Write, Idempotent: true, Destructive: true,
		Enums: map[string][]string{"status": {"on", "off"}},
		Keys:  map[string][]string{"days": dayKeyNames},
	}, func(ctx context.Context, a setHotWaterScheduleArgs) (any, error) {
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
				if p.Status != "on" && p.Status != "off" {
					return nil, fmt.Errorf("%s %s: status must be on or off", day, p.From)
				}
				changed[day] = append(changed[day], slot{start, Obj{"status": strings.ToUpper(p.Status)}})
			}
			if len(periods) == 0 {
				changed[day] = nil
			}
		}
		_, p, err := hotWater(ctx, true)
		if err != nil {
			return nil, err
		}
		week, days, err := buildSchedule(p, changed)
		if err != nil {
			return nil, err
		}
		out, err := setSchedule(ctx, c, p, week, days)
		if err == nil && hive.Str(p, "state", "mode") != "SCHEDULE" {
			out.(Obj)["note_mode"] = "The hot water isn't in schedule mode, so it won't follow the schedule until it's switched to schedule (hive_set_hot_water_mode)."
		}
		return out, err
	})

	add(c, Def{
		Toolset: "hotwater", Name: "hive_set_hot_water_mode", Title: "Set hot water mode",
		Description: "Switch the hot water on (it stays on until changed), off, or back to its schedule. Cancels a boost. For a limited time, use hive_boost_hot_water instead of on. On lasts indefinitely, so it needs confirm: true.",
		Gate:        Write, Idempotent: true,
		Enums: map[string][]string{"mode": {"on", "off", "schedule"}},
	}, func(ctx context.Context, a setHotWaterArgs) (any, error) {
		mode, ok := hotWaterModes[a.Mode]
		if !ok {
			return nil, fmt.Errorf("mode must be on, off or schedule")
		}
		if a.Mode == "on" {
			if err := needConfirm(a.Confirm, "Hot water on", "hive_boost_hot_water"); err != nil {
				return nil, err
			}
		}
		_, p, err := hotWater(ctx, false)
		if err != nil {
			return nil, err
		}
		return change(ctx, cl, p, Obj{"mode": mode}, summarize)
	})

	add(c, Def{
		Toolset: "hotwater", Name: "hive_boost_hot_water", Title: "Boost hot water",
		Description: "Heat the hot water now for a number of minutes, then go back to the previous mode.",
		Gate:        Write,
		Ranges:      map[string][2]float64{"minutes": {1, 360}},
	}, func(ctx context.Context, a boostHotWaterArgs) (any, error) {
		minutes := 60
		if a.Minutes != nil {
			minutes = *a.Minutes
		}
		if minutes < 1 || minutes > 360 {
			return nil, fmt.Errorf("minutes must be between 1 and 360")
		}
		_, p, err := hotWater(ctx, false)
		if err != nil {
			return nil, err
		}
		return change(ctx, cl, p, Obj{"mode": "BOOST", "boost": minutes}, summarize)
	})

	add(c, Def{
		Toolset: "hotwater", Name: "hive_cancel_hot_water_boost", Title: "Cancel hot water boost",
		Description: "Stop a hot water boost early and go back to the previous mode.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		_, p, err := hotWater(ctx, true)
		if err != nil {
			return nil, err
		}
		body, _, err := cancelBoostBody(p, false)
		if err != nil {
			return nil, err
		}
		return change(ctx, cl, p, body, summarize)
	})
}
