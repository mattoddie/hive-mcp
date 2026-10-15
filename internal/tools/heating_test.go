package tools

import (
	"strings"
	"testing"

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
	env.obj("hive_set_heating_temperature", map[string]any{"zone": "Bedroom", "temperature": 19, "mode": "manual"})
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
	env.obj("hive_set_heating_mode", map[string]any{"zone": "Heating", "mode": "manual", "temperature": 19.5})
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
