package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mattoddie/hive-mcp/internal/hive"
)

type authCompleteArgs struct {
	Code string `json:"code" jsonschema:"The code from the text message Hive sent to the account's phone"`
}

type listDevicesArgs struct {
	Type       string `json:"type,omitempty" jsonschema:"Only devices of this type, for example trv, thermostatui, boilermodule, hub, activeplug, warmwhitelight, contactsensor"`
	LowBattery bool   `json:"low_battery,omitempty" jsonschema:"Only battery devices at or below 25%"`
	Offline    bool   `json:"offline,omitempty" jsonschema:"Only devices Hive says are offline"`
	rawArgs
	freshArgs
}

type setActionArgs struct {
	Action  string `json:"action" jsonschema:"The action, by name or ID (see hive_list_actions)"`
	Enabled bool   `json:"enabled" jsonschema:"true to turn the action on, false to turn it off"`
}

type setHolidayArgs struct {
	Start       string  `json:"start,omitempty" jsonschema:"When holiday mode starts, as YYYY-MM-DD or YYYY-MM-DDTHH:MM in the home's local time (default now)"`
	End         string  `json:"end" jsonschema:"When it ends and the normal schedule resumes, as YYYY-MM-DD or YYYY-MM-DDTHH:MM in the home's local time; at most 60 days after the start"`
	Temperature float64 `json:"temperature" jsonschema:"Temperature to keep the home at while away, in °C (from 5 in steps of 0.5, up to HIVE_MAX_TEMP; frost protection is usually 7)"`
}

func (c *Context) nodes(ctx context.Context, fresh bool) (*hive.Nodes, error) {
	return c.Client.Nodes(ctx, fresh)
}

// maxHolidayDays caps holiday mode, so a mistaken end date can't leave the
// heating at one temperature for months. It counts calendar days in the
// home's time zone, so a holiday across a clock change isn't an hour short.
const maxHolidayDays = 60

func registerHome(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "home", Name: "hive_get_auth_status", Title: "Hive login status",
		Description: "Show whether this server is logged in to Hive: the account (with the email address masked), whether this server is a remembered device (so it can log in again without an SMS code), when the current token expires and any login error. Never shows passwords, tokens or device secrets.",
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		return authStatus(cl.Auth.Status()), nil
	})

	add(c, Def{
		Toolset: "home", Name: "hive_start_auth", Title: "Start Hive login",
		Description: "Start logging this server in to Hive with HIVE_USERNAME and HIVE_PASSWORD. If this server is already a remembered device, that's the whole login. Otherwise Hive texts a code to the account's phone: ask the user for it and pass it to hive_complete_auth within about three minutes. " +
			"Only use this when the user asks to log in or a tool says the server isn't logged in; it sends a real text message. It refuses to send another code within a minute of the last one, and to try again within five minutes of a login Hive refused.",
		Gate: Login, ReadOnly: boolPtr(false), OpenWorld: true,
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		res, err := cl.Auth.StartLogin(ctx)
		if err != nil {
			return nil, err
		}
		res = maskLogin(res)
		out := Obj{"result": res}
		if res.CodeRequired {
			out["next_step"] = fmt.Sprintf("Hive has sent a code by text message to %s. Ask the user for it and call hive_complete_auth with it within about three minutes.", res.CodeSentTo)
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "home", Name: "hive_complete_auth", Title: "Finish Hive login",
		Description: "Finish a login started with hive_start_auth by giving the code Hive texted to the account's phone. This also registers the server as a remembered device, so it won't need a code again.",
		Gate:        Login, ReadOnly: boolPtr(false),
	}, func(ctx context.Context, a authCompleteArgs) (any, error) {
		res, err := cl.Auth.CompleteLogin(ctx, a.Code)
		return maskLogin(res), err
	})

	add(c, Def{
		Toolset: "home", Name: "hive_get_overview", Title: "Home overview",
		Description: "A one-call snapshot of the home: every heating zone and TRV zone (current and target temperature, mode, boost, whether it's calling for heat), the hot water, plugs, lights and sensors, holiday mode, and anything that needs attention: low batteries, offline devices and discontinued hardware. A good first call.",
	}, func(ctx context.Context, a freshArgs) (any, error) {
		n, err := c.nodes(ctx, a.Fresh)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		out := Obj{"as_of": n.Fetched.In(n.Location()).Format(time.RFC3339)}
		for _, h := range n.Homes() {
			out["homes"] = append(asAny(out["homes"]), hive.Compact(Obj{"id": hive.Str(h, "id"), "name": hive.Str(h, "name"), "primary": hive.BoolOrNil(h, "primary"), "time_zone": hive.Str(h, "location", "timeZone")}))
		}
		var zones, smart []Obj
		for _, p := range n.Products(hive.HeatingTypes...) {
			zones = append(zones, n.SummarizeHeating(p, now))
		}
		if zones != nil {
			out["heating"] = zones
		}
		for _, p := range n.Products(hive.HotWaterType) {
			out["hot_water"] = n.SummarizeHotWater(p, now)
		}
		for _, p := range n.Products(hive.PlugTypes...) {
			smart = append(smart, n.SummarizePlug(p))
		}
		for _, p := range n.Products(hive.LightTypes...) {
			smart = append(smart, n.SummarizeLight(p))
		}
		for _, p := range n.Products(hive.SensorTypes...) {
			smart = append(smart, n.SummarizeSensor(p))
		}
		if smart != nil {
			out["plugs_lights_and_sensors"] = smart
		}
		if hm := holidaySummary(n, n.Raw["holidayMode"]); hm != nil {
			out["holiday_mode"] = hm
		}
		if att := attention(n); len(att) > 0 {
			out["needs_attention"] = att
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "home", Name: "hive_list_devices", Title: "List Hive devices",
		Description: "List the Hive hardware: hub, thermostats, boiler module, radiator valves (TRVs), plugs, bulbs and sensors, with online status, battery level, signal strength, model and firmware. Filter by type, low battery or offline.",
	}, func(ctx context.Context, a listDevicesArgs) (any, error) {
		n, err := c.nodes(ctx, a.Fresh)
		if err != nil {
			return nil, err
		}
		var out []Obj
		for _, d := range n.Devices() {
			if a.Type != "" && hive.Str(d, "type") != a.Type {
				continue
			}
			s := n.SummarizeDevice(d)
			if b, ok := s["battery"].(float64); a.LowBattery && (!ok || b > hive.LowBattery) {
				continue
			}
			if a.Offline && s["online"] != false {
				continue
			}
			if note, ok := hive.Discontinued[hive.Str(d, "type")]; ok {
				s["discontinued"] = note
			}
			if a.Raw {
				out = append(out, redactObj(d))
			} else {
				out = append(out, s)
			}
		}
		if out == nil {
			out = []Obj{}
		}
		return listResult(out), nil
	})

	add(c, Def{
		Toolset: "home", Name: "hive_list_actions", Title: "List Hive actions",
		Description: "List the Hive actions (automations set up in the Hive app, such as \"when the back door opens, send a notification\"): each one's name, whether it's on, and its events: what starts it (when), any condition (while) and what it does (then), with the devices involved.",
	}, func(ctx context.Context, a rawFreshArgs) (any, error) {
		n, err := c.nodes(ctx, a.Fresh)
		if err != nil {
			return nil, err
		}
		out := []Obj{}
		for _, act := range n.Actions() {
			if a.Raw {
				out = append(out, redactObj(act))
			} else {
				out = append(out, n.SummarizeAction(act))
			}
		}
		return listResult(out), nil
	})

	add(c, Def{
		Toolset: "home", Name: "hive_set_action", Title: "Turn a Hive action on or off",
		Description: "Turn a Hive action (an automation from the Hive app) on or off. This doesn't run it: an action that's on runs when its trigger happens. Actions can only be created or edited in the Hive app.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a setActionArgs) (any, error) {
		// Fresh, because the whole action is sent back.
		n, err := c.nodes(ctx, true)
		if err != nil {
			return nil, err
		}
		act, err := find(n.Actions(), a.Action, "action")
		if err != nil {
			return nil, err
		}
		// Hive takes the whole action back with enabled changed, as pyhiveapi's
		// set_action sends it: PUT /actions/<id>.
		body := make(Obj, len(act))
		for k, v := range act {
			body[k] = v
		}
		body["enabled"] = a.Enabled
		id := hive.Str(act, "id")
		hive.SetCurrent(ctx, redactObj(act))
		if err := cl.Do(ctx, "PUT", "/actions/"+url.PathEscape(id), body, nil); err != nil {
			return nil, err
		}
		cl.Changed(ctx)
		return Obj{"done": true, "name": hive.Name(act), "sent": Obj{"enabled": a.Enabled}}, nil
	})

	add(c, Def{
		Toolset: "home", Name: "hive_get_holiday_mode", Title: "Get holiday mode",
		Description: "Show whether holiday mode is set: when it starts and ends and the temperature the heating holds while you're away.",
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		var hm Obj
		if err := cl.Get(ctx, "/holiday-mode", &hm); err != nil {
			return nil, err
		}
		n, err := c.nodes(ctx, false)
		if err != nil {
			n = &hive.Nodes{}
		}
		if s := holidaySummary(n, hm); s != nil {
			return s, nil
		}
		return Obj{"enabled": false}, nil
	})

	add(c, Def{
		Toolset: "home", Name: "hive_set_holiday_mode", Title: "Set holiday mode",
		Description: "Set holiday mode: from start (default now) until end, the heating holds one temperature, then the normal settings resume. It lasts at most 60 days. Replaces any holiday mode already set. Confirm the dates and temperature with the user first.",
		// Not idempotent: start defaults to now, so a repeat moves it.
		Gate: Write, Destructive: true, Ranges: map[string][2]float64{"temperature": c.tempRange()},
	}, func(ctx context.Context, a setHolidayArgs) (any, error) {
		if err := checkTemperature("temperature", a.Temperature, c.maxTemp()); err != nil {
			return nil, err
		}
		n, err := c.nodes(ctx, false)
		if err != nil {
			return nil, err
		}
		loc := n.Location()
		start := time.Now()
		if a.Start != "" {
			if start, err = parseWhen(a.Start, loc); err != nil {
				return nil, err
			}
		}
		end, err := parseWhen(a.End, loc)
		if err != nil {
			return nil, err
		}
		if !end.After(start) || !end.After(time.Now()) {
			return nil, errors.New("end must be after start and in the future")
		}
		if limit := start.In(loc).AddDate(0, 0, maxHolidayDays); end.After(limit) {
			return nil, fmt.Errorf("holiday mode can last at most %d days; end must be by %s", maxHolidayDays, limit.Format("2006-01-02T15:04"))
		}
		body := Obj{"start": start.UnixMilli(), "end": end.UnixMilli(), "temperature": a.Temperature}
		previewHoliday(ctx, cl)
		if err := cl.Post(ctx, "/holiday-mode", body, nil); err != nil {
			return nil, err
		}
		cl.Changed(ctx)
		return Obj{"done": true, "start": start.In(loc).Format(time.RFC3339), "end": end.In(loc).Format(time.RFC3339), "temperature": a.Temperature}, nil
	})

	add(c, Def{
		Toolset: "home", Name: "hive_cancel_holiday_mode", Title: "Cancel holiday mode",
		Description: "Cancel holiday mode, so the heating and hot water go back to their normal settings straight away.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		previewHoliday(ctx, cl)
		if err := cl.Do(ctx, "DELETE", "/holiday-mode", Obj{}, nil); err != nil {
			return nil, err
		}
		cl.Changed(ctx)
		return Obj{"done": true, "holiday_mode": "cancelled"}, nil
	})
}

// previewHoliday reads the current holiday mode for a dry run's preview.
// Outside a dry run it does nothing; if the read fails, the preview just
// goes without it.
func previewHoliday(ctx context.Context, cl *hive.Client) {
	if !hive.IsDryRun(ctx) {
		return
	}
	var hm Obj
	if err := cl.Get(ctx, "/holiday-mode", &hm); err == nil && hm != nil {
		hive.SetCurrent(ctx, hm)
	}
}

func asAny(v any) []any {
	l, _ := v.([]any)
	return l
}

func holidaySummary(n *hive.Nodes, v any) Obj {
	hm, _ := v.(Obj)
	if hm == nil || (!hive.Bool(hm, "enabled") && !hive.Bool(hm, "active")) {
		return nil
	}
	return hive.Compact(Obj{
		"enabled": true, "active_now": hive.Bool(hm, "active"),
		"start": n.When(hm["start"]), "end": n.When(hm["end"]), "temperature": hive.NumOrNil(hm, "temperature"),
	})
}

// attention lists low batteries, offline devices and discontinued hardware.
func attention(n *hive.Nodes) []string {
	var out []string
	for _, d := range n.Devices() {
		name := fmt.Sprintf("%s (%s)", hive.Name(d), hive.Str(d, "type"))
		if note, ok := hive.Discontinued[hive.Str(d, "type")]; ok {
			out = append(out, name+": discontinued, "+note)
			continue
		}
		if hive.Offline(d) {
			out = append(out, name+": offline")
		}
		if b, ok := hive.Num(d, "props", "battery"); ok && hive.Str(d, "props", "power") != "mains" && b <= hive.LowBattery {
			out = append(out, fmt.Sprintf("%s: battery low (%g%%)", name, b))
		}
	}
	return out
}

// maskEmail keeps enough of the account's email address to recognise it
// ("s***@example.com") without sending the whole address to the model.
func maskEmail(s string) string {
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" {
		if s == "" {
			return ""
		}
		return "***"
	}
	return local[:1] + "***@" + domain
}

// authStatus is Auth.Status for the model: the email address masked, and
// no path to the token file.
func authStatus(st map[string]any) map[string]any {
	if u, ok := st["username"].(string); ok {
		st["username"] = maskEmail(u)
	}
	delete(st, "token_file")
	return st
}

// maskLogin is a login result for the model.
func maskLogin(res *hive.LoginResult) *hive.LoginResult {
	if res == nil {
		return nil
	}
	r := *res
	r.Username, r.TokenFile = maskEmail(r.Username), ""
	return &r
}
