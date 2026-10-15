package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/hive-mcp/internal/hive/hivetest"
)

func TestHotWater(t *testing.T) {
	env := newEnv(t)
	hw := env.obj("hive_get_hot_water", nil)
	if hw["mode"] != "schedule" || hw["on"] != false || hw["name"] != "Hot Water" {
		t.Errorf("hot water: %v", hw)
	}
	sched := env.obj("hive_get_hot_water_schedule", nil)
	if p := sched["week"].(Obj)["sunday"].([]any)[0].(Obj); p["from"] != "06:00" || p["until"] != "07:30" || p["status"] != "on" {
		t.Errorf("schedule: %v", p)
	}

	res := env.obj("hive_set_hot_water_mode", map[string]any{"mode": "on"})
	if got := env.lastBody(); got != "/nodes/hotwater/"+hivetest.HotWaterID+` {"mode":"MANUAL"}` {
		t.Errorf("sent %s", got)
	}
	if res["now"].(Obj)["mode"] != "on" || res["now"].(Obj)["on"] != true {
		t.Errorf("on: %v", res)
	}
	env.obj("hive_set_hot_water_mode", map[string]any{"mode": "schedule"})

	res = env.obj("hive_boost_hot_water", map[string]any{"minutes": 45})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"boost":45,"mode":"BOOST"}`) {
		t.Errorf("boost sent %s", got)
	}
	if res["now"].(Obj)["mode_after_boost"] != "schedule" {
		t.Errorf("boost: %v", res)
	}
	env.obj("hive_cancel_hot_water_boost", nil)
	if got := env.lastBody(); !strings.HasSuffix(got, `{"mode":"SCHEDULE"}`) {
		t.Errorf("cancel sent %s", got)
	}
	if msg := env.callErr("hive_cancel_hot_water_boost", nil); !strings.Contains(msg, "isn't boosting") {
		t.Errorf("cancel twice: %s", msg)
	}
	if !env.rejects("hive_set_hot_water_mode", map[string]any{"mode": "boost"}) {
		t.Error("boost accepted as a mode")
	}
}

func TestNoHotWater(t *testing.T) {
	env := newEnv(t)
	nodes := env.mock.Nodes()
	var keep []any
	for _, p := range nodes["products"].([]any) {
		if p.(map[string]any)["type"] != "hotwater" {
			keep = append(keep, p)
		}
	}
	nodes["products"] = keep
	env.mock.SetNodes(nodes)
	if msg := env.callErr("hive_get_hot_water", nil); !strings.Contains(msg, "no hot water control") {
		t.Errorf("error: %s", msg)
	}
}
