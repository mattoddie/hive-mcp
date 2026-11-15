package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattoddie/hive-mcp/internal/hive/hivetest"
)

func TestDryRunSendsNothing(t *testing.T) {
	env := newEnv(t)
	before := env.mock.Nodes()
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"hive_boost_heating", map[string]any{"temperature": 21, "minutes": 30, "zone": "Heating"},
			`[{"body":{"boost":30,"mode":"BOOST","target":21},"method":"POST","path":"/nodes/heating/` + hivetest.HeatingID + `"}]`},
		{"hive_set_light", map[string]any{"light": "living", "brightness": 50, "colour_temperature": 4000},
			`[{"body":{"brightness":50,"status":"ON"},"method":"POST","path":"/nodes/tuneablelight/` + hivetest.TuneableID + `"},{"body":{"colourTemperature":4000},"method":"POST","path":"/nodes/tuneablelight/` + hivetest.TuneableID + `"}]`},
		{"hive_cancel_holiday_mode", nil, `[{"body":{},"method":"DELETE","path":"/holiday-mode"}]`},
		{"hive_set_action", map[string]any{"action": "lamp", "enabled": true}, `"method":"PUT","path":"/actions/action-2"`},
		{"hive_api_request", map[string]any{"method": "POST", "path": "/nodes/activeplug/" + hivetest.PlugID, "body": map[string]any{"status": "OFF"}},
			`[{"body":{"status":"OFF"},"method":"POST","path":"/nodes/activeplug/` + hivetest.PlugID + `"}]`},
		{"hive_set_hot_water_schedule", map[string]any{"monday": []any{map[string]any{"from": "00:00", "status": "off"}}}, `"monday":[{"start":0,"value":{"status":"OFF"}}]`},
	}
	for _, c := range cases {
		args := map[string]any{"dry_run": true}
		for k, v := range c.args {
			args[k] = v
		}
		res := env.obj(c.tool, args)
		got, _ := json.Marshal(res["would_send"])
		if res["dry_run"] != true || !strings.Contains(string(got), c.want) || !strings.Contains(res["note"].(string), "Nothing was changed") {
			t.Errorf("%s: %v", c.tool, res)
		}
	}
	if n := len(env.mock.Requests()) - countGets(env); n != 0 {
		t.Errorf("a dry run sent %d changes", n)
	}
	after, _ := json.Marshal(env.mock.Nodes())
	if b, _ := json.Marshal(before); string(b) != string(after) {
		t.Error("a dry run changed the home")
	}
}

func countGets(env *testEnv) int {
	n := 0
	for _, r := range env.mock.Requests() {
		if r.Method == "GET" {
			n++
		}
	}
	return n
}

func TestDryRunStillValidates(t *testing.T) {
	env := newEnv(t)
	if msg := env.callErr("hive_set_heating_mode", map[string]any{"zone": "Heating", "mode": "manual", "dry_run": true}); !strings.Contains(msg, "confirm: true") {
		t.Errorf("error: %s", msg)
	}
	if msg := env.callErr("hive_set_holiday_mode", map[string]any{"end": "soon", "temperature": 10, "dry_run": true}); !strings.Contains(msg, "not a date") {
		t.Errorf("error: %s", msg)
	}
	// A raw GET in a dry run is still read.
	res := env.obj("hive_api_request", map[string]any{"path": "/holiday-mode", "dry_run": true})
	if len(res["would_send"].([]any)) != 0 || res["result"] == nil {
		t.Errorf("raw GET: %v", res)
	}
}

func TestOnlyToolsThatChangeThingsHaveDryRun(t *testing.T) {
	env := newEnv(t)
	for name, tool := range toolNames(t, env.session) {
		props, _ := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		_, has := props["dry_run"]
		changes := !tool.Annotations.ReadOnlyHint && !strings.HasPrefix(name, "hive_auth_")
		if has != changes {
			t.Errorf("%s: dry_run %v, changes things %v", name, has, changes)
		}
	}
	if !env.rejects("hive_get_heating", map[string]any{"dry_run": true}) {
		t.Error("a read tool accepted dry_run")
	}
}
