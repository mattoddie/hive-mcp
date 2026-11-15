package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive/hivetest"
)

func TestGetHeating(t *testing.T) {
	env := newEnv(t)
	res := env.obj("hive_get_heating", map[string]any{"zone": "bedroom"})
	zones := res["zones"].([]any)
	if len(zones) != 1 || zones[0].(Obj)["kind"] != "TRV zone" || zones[0].(Obj)["target_temperature"] != 18.0 {
		t.Errorf("bedroom: %v", res)
	}
	raw := env.obj("hive_get_heating", map[string]any{"raw": true})
	if len(raw["zones"].([]any)) != 2 || raw["zones"].([]any)[0].(Obj)["state"] == nil {
		t.Errorf("raw: %v", raw)
	}

	sched := env.obj("hive_get_heating_schedule", map[string]any{"zone": "Heating"})
	week := sched["week"].(Obj)
	if len(week) != 7 || sched["time_zone"] != "Europe/London" || sched["now"] == nil {
		t.Errorf("schedule: %v", sched)
	}
	if mon := week["monday"].([]any)[2].(Obj); mon["from"] != "17:00" || mon["target_temperature"] != 21.0 {
		t.Errorf("monday: %v", mon)
	}
	if msg := env.callErr("hive_get_heating_schedule", nil); !strings.Contains(msg, "more than one heating zone") {
		t.Errorf("ambiguous: %s", msg)
	}

	trvs := env.obj("hive_list_trvs", nil)
	valve := trvs["zones"].([]any)[0].(Obj)["valves"].([]any)[0].(Obj)
	if valve["battery"] != 20.0 || valve["calibration"] != "calibrated" {
		t.Errorf("valve: %v", valve)
	}
}

func TestSetHeating(t *testing.T) {
	env := newEnv(t)
	res := env.obj("hive_set_heating_temperature", map[string]any{"zone": "Heating", "temperature": 21.5})
	if got := env.lastBody(); got != "/nodes/heating/"+hivetest.HeatingID+` {"target":21.5}` {
		t.Errorf("sent %s", got)
	}
	if res["now"].(Obj)["target_temperature"] != 21.5 {
		t.Errorf("result: %v", res)
	}
	if msg := env.callErr("hive_set_heating_temperature", map[string]any{"zone": "Bedroom", "temperature": 19, "mode": "manual"}); !strings.Contains(msg, "confirm: true") {
		t.Errorf("manual without confirm: %s", msg)
	}
	posts := env.posts()
	env.obj("hive_set_heating_temperature", map[string]any{"zone": "Bedroom", "temperature": 19, "mode": "manual", "confirm": true})
	if env.posts() != posts+1 {
		t.Errorf("posts: %d then %d", posts, env.posts())
	}
	if got := env.lastBody(); got != "/nodes/trvcontrol/"+hivetest.TRVZoneID+` {"mode":"MANUAL","target":19}` {
		t.Errorf("sent %s", got)
	}
	if msg := env.callErr("hive_set_heating_temperature", map[string]any{"zone": "Heating", "temperature": 20.25}); !strings.Contains(msg, "steps of 0.5") {
		t.Errorf("step: %s", msg)
	}

	env.obj("hive_set_heating_mode", map[string]any{"zone": "Heating", "mode": "off"})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"mode":"OFF"}`) {
		t.Errorf("sent %s", got)
	}
	res = env.obj("hive_set_heating_temperature", map[string]any{"zone": "Heating", "temperature": 20})
	if !strings.Contains(res["warning"].(string), "off") {
		t.Errorf("no warning when off: %v", res)
	}
	if msg := env.callErr("hive_set_heating_mode", map[string]any{"zone": "Heating", "mode": "manual", "temperature": 19.5}); !strings.Contains(msg, "lasts indefinitely") {
		t.Errorf("manual without confirm: %s", msg)
	}
	if msg := env.callErr("hive_set_heating_temperature", map[string]any{"zone": "Bedroom", "temperature": 20}); !strings.Contains(msg, "confirm: true") {
		t.Errorf("a zone already in manual: %s", msg)
	}
	if !env.rejects("hive_set_heating_mode", map[string]any{"zone": "Heating", "mode": "manual", "temperature": 26, "confirm": true}) {
		t.Error("26 °C accepted above HIVE_MAX_TEMP")
	}
	env.obj("hive_set_heating_mode", map[string]any{"zone": "Heating", "mode": "manual", "temperature": 19.5, "confirm": true})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"mode":"MANUAL","target":19.5}`) {
		t.Errorf("sent %s", got)
	}
	if msg := env.callErr("hive_set_heating_mode", map[string]any{"zone": "Heating", "mode": "schedule", "temperature": 20}); !strings.Contains(msg, "only goes with mode manual") {
		t.Errorf("schedule+temp: %s", msg)
	}
	if !env.rejects("hive_set_heating_mode", map[string]any{"zone": "Heating", "mode": "eco"}) {
		t.Error("unknown mode accepted")
	}
}

func TestBoostHeating(t *testing.T) {
	env := newEnv(t)
	if msg := env.callErr("hive_cancel_heating_boost", map[string]any{"zone": "Heating"}); !strings.Contains(msg, "isn't boosting") {
		t.Errorf("not boosting: %s", msg)
	}
	res := env.obj("hive_boost_heating", map[string]any{"zone": "Heating", "temperature": 22, "minutes": 30})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"boost":30,"mode":"BOOST","target":22}`) {
		t.Errorf("sent %s", got)
	}
	now := res["now"].(Obj)
	if now["mode"] != "boost" || now["mode_after_boost"] != "schedule" {
		t.Errorf("boosting: %v", now)
	}
	env.obj("hive_cancel_heating_boost", map[string]any{"zone": "Heating"})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"mode":"SCHEDULE"}`) {
		t.Errorf("cancel sent %s", got)
	}

	// From manual, cancelling goes back to manual at the previous target.
	env.obj("hive_boost_heating", map[string]any{"zone": "Bedroom", "temperature": 23})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"boost":60,"mode":"BOOST","target":23}`) {
		t.Errorf("default minutes: %s", got)
	}
	env.obj("hive_cancel_heating_boost", map[string]any{"zone": "Bedroom"})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"mode":"MANUAL","target":18}`) {
		t.Errorf("cancel to manual sent %s", got)
	}
	if !env.rejects("hive_boost_heating", map[string]any{"zone": "Heating", "temperature": 22, "minutes": 999}) {
		t.Error("999 minutes accepted")
	}
}

func TestMaxTempIsConfigurable(t *testing.T) {
	o := config.AllTools()
	o.MaxTemp = 28
	env := newEnv(t, o)
	env.obj("hive_boost_heating", map[string]any{"zone": "Heating", "temperature": 27.5})
	if !env.rejects("hive_boost_heating", map[string]any{"zone": "Heating", "temperature": 28.5}) {
		t.Error("28.5 °C accepted with HIVE_MAX_TEMP=28")
	}
	// A manual switch that keeps the current target checks it against the limit too.
	o.MaxTemp = 15
	env = newEnv(t, o)
	if msg := env.callErr("hive_set_heating_mode", map[string]any{"zone": "Bedroom", "mode": "manual", "confirm": true}); !strings.Contains(msg, "HIVE_MAX_TEMP") {
		t.Errorf("current target above the limit: %s", msg)
	}
}

func TestSetHeatingSchedule(t *testing.T) {
	env := newEnv(t)
	res := env.obj("hive_set_heating_schedule", map[string]any{
		"zone":     "Heating",
		"weekdays": []any{map[string]any{"from": "00:00", "target_temperature": 16}, map[string]any{"from": "07:00", "target_temperature": 20.5}},
		"friday":   []any{map[string]any{"from": "07:00", "target_temperature": 19}, map[string]any{"from": "00:00", "target_temperature": 15}},
	})
	sched := env.mock.Product(hivetest.HeatingID)["state"].(map[string]any)["schedule"].(map[string]any)
	mon, _ := json.Marshal(sched["monday"])
	fri, _ := json.Marshal(sched["friday"])
	sun, _ := json.Marshal(sched["sunday"])
	if string(mon) != `[{"start":0,"value":{"target":16}},{"start":420,"value":{"target":20.5}}]` ||
		string(fri) != `[{"start":0,"value":{"target":15}},{"start":420,"value":{"target":19}}]` {
		t.Errorf("sent monday %s friday %s", mon, fri)
	}
	if string(sun) != `[{"start":390,"value":{"target":20}},{"start":510,"value":{"target":16}},{"start":1020,"value":{"target":21}},{"start":1350,"value":{"target":16}}]` {
		t.Errorf("sunday should be kept: %s", sun)
	}
	if got := res["days_changed"].([]any); len(got) != 5 {
		t.Errorf("days changed: %v", got)
	}
	now := res["now"].(map[string]any)
	if now["warning"] != nil || now["week"].(map[string]any)["monday"] == nil {
		t.Errorf("result: %v", res)
	}
	if res["sent"] != nil {
		t.Error("the whole week shouldn't be repeated")
	}

	// TRV zones have schedules too, and a zone not in schedule mode says so.
	res = env.obj("hive_set_heating_schedule", map[string]any{"zone": "Bedroom", "sunday": []any{map[string]any{"from": "00:00", "target_temperature": 18}}})
	if res["note_mode"] == nil || !strings.HasPrefix(env.lastBody(), "/nodes/trvcontrol/") {
		t.Errorf("TRV schedule: %v", res)
	}

	before := env.posts()
	for want, args := range map[string]map[string]any{
		"not a time of day":   {"zone": "Heating", "monday": []any{map[string]any{"from": "7am", "target_temperature": 20}}},
		"steps of 0.5":        {"zone": "Heating", "monday": []any{map[string]any{"from": "00:00", "target_temperature": 20.2}}},
		"two periods start":   {"zone": "Heating", "monday": []any{map[string]any{"from": "00:00", "target_temperature": 20}, map[string]any{"from": "00:00", "target_temperature": 18}}},
		"at most 6 periods":   {"zone": "Heating", "monday": sevenPeriods()},
		"at least one period": {"zone": "Heating", "monday": []any{}},
		"nothing to change":   {"zone": "Heating"},
		"24:00":               {"zone": "Heating", "monday": []any{map[string]any{"from": "24:00", "target_temperature": 20}}},
	} {
		msg := ""
		if res := env.raw("hive_set_heating_schedule", args); res.IsError {
			msg = text(res)
		}
		if want == "24:00" {
			want = "not a time of day"
		}
		if !strings.Contains(msg, want) {
			t.Errorf("%v: %q", args, msg)
		}
	}
	if !env.rejects("hive_set_heating_schedule", map[string]any{"zone": "Heating", "monday": []any{map[string]any{"from": "00:00", "target_temperature": 26}}}) {
		t.Error("26 °C accepted")
	}
	if env.posts() != before {
		t.Error("an invalid schedule reached Hive")
	}
}

func sevenPeriods() []any {
	var out []any
	for i := 0; i < 7; i++ {
		out = append(out, map[string]any{"from": fmt.Sprintf("%02d:00", i), "target_temperature": 20})
	}
	return out
}

func TestSetScheduleWarnsWhenHiveDoesNotStoreIt(t *testing.T) {
	env := newEnv(t)
	env.mock.On("POST", "/nodes/heating/.*", hivetest.JSON([]any{}))
	res := env.obj("hive_set_heating_schedule", map[string]any{"zone": "Heating", "monday": []any{map[string]any{"from": "00:00", "target_temperature": 18}}})
	if w, _ := res["now"].(map[string]any)["warning"].(string); !strings.Contains(w, "doesn't show it yet") {
		t.Errorf("result: %v", res)
	}
}

func TestSetHeatOnDemand(t *testing.T) {
	env := newEnv(t)
	res := env.obj("hive_set_heat_on_demand", map[string]any{"enabled": true})
	if got := env.lastBody(); got != "/nodes/heating/"+hivetest.HeatingID+` {"autoBoost":"ENABLED"}` {
		t.Errorf("sent %s", got)
	}
	if res["now"].(map[string]any)["heat_on_demand"] != true {
		t.Errorf("result: %v", res)
	}
	env.obj("hive_set_heat_on_demand", map[string]any{"zone": "Heating", "enabled": false})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"autoBoost":"DISABLED"}`) {
		t.Errorf("sent %s", got)
	}
	if msg := env.callErr("hive_set_heat_on_demand", map[string]any{"zone": "Bedroom", "enabled": true}); !strings.Contains(msg, "no heating zone with a thermostat called") {
		t.Errorf("TRV zone: %s", msg)
	}

	// A thermostat without the setting is refused.
	n := env.mock.Nodes()
	heating := n["products"].([]any)[0].(map[string]any)
	delete(heating["state"].(map[string]any), "autoBoost")
	env.mock.SetNodes(n)
	before := env.posts()
	if msg := env.callErr("hive_set_heat_on_demand", map[string]any{"enabled": true}); !strings.Contains(msg, "doesn't have heat on demand") {
		t.Errorf("unsupported: %s", msg)
	}
	if env.posts() != before {
		t.Error("an unsupported change reached Hive")
	}
}
