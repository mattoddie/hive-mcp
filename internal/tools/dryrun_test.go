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
			`{"body":{"boost":30,"mode":"BOOST","target":21},"method":"POST","path":"/nodes/heating/` + hivetest.HeatingID + `"}`},
		{"hive_set_light", map[string]any{"light": "living", "brightness": 50, "colour_temperature": 4000},
			`{"body":{"brightness":50,"status":"ON"},"method":"POST","path":"/nodes/tuneablelight/` + hivetest.TuneableID + `"}`},
		{"hive_cancel_holiday_mode", nil, `{"body":{},"method":"DELETE","path":"/holiday-mode"}`},
		{"hive_set_action", map[string]any{"action": "lamp", "enabled": true}, `"method":"PUT","path":"/actions/action-2"`},
		{"hive_api_request", map[string]any{"method": "POST", "path": "/nodes/activeplug/" + hivetest.PlugID, "body": map[string]any{"status": "OFF"}},
			`{"body":{"status":"OFF"},"method":"POST","path":"/nodes/activeplug/` + hivetest.PlugID + `"}`},
		{"hive_set_hot_water_schedule", map[string]any{"days": map[string]any{"monday": []any{map[string]any{"from": "00:00", "status": "off"}}}}, `"monday":[{"start":0,"value":{"status":"OFF"}}]`},
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

func TestDryRunShowsCurrentAndChanges(t *testing.T) {
	env := newEnv(t)
	res := env.obj("hive_boost_heating", map[string]any{"temperature": 21, "minutes": 30, "zone": "Heating", "dry_run": true})
	cur, _ := res["current"].(Obj)
	ch, _ := res["changes"].(Obj)
	if cur == nil || cur["mode"] == nil || ch == nil {
		t.Fatalf("boost: %v", res)
	}
	if m, _ := ch["changed"].(Obj)["mode"].(Obj); m == nil || m["to"] != "BOOST" {
		t.Errorf("boost changes: %v", ch)
	}

	// An action is sent back whole; only enabled changes.
	res = env.obj("hive_set_action", map[string]any{"action": "lamp", "enabled": true, "dry_run": true})
	ch, _ = res["changes"].(Obj)
	if changed, _ := ch["changed"].(Obj); len(changed) != 1 || changed["enabled"] == nil || ch["added"] != nil {
		t.Errorf("action changes: %v", res["changes"])
	}

	// Holiday mode previews show the holiday mode they would replace or cancel.
	res = env.obj("hive_cancel_holiday_mode", map[string]any{"dry_run": true})
	if res["current"] == nil || res["changes"] != nil {
		t.Errorf("cancel holiday: %v", res)
	}

	// A light change sent as two requests describes the first and counts the rest.
	res = env.obj("hive_set_light", map[string]any{"light": "living", "brightness": 50, "colour_temperature": 4000, "dry_run": true})
	if !strings.Contains(res["note"].(string), "1 more request after this one") {
		t.Errorf("light note: %v", res["note"])
	}

	// A raw request with a query string reports it separately.
	res = env.obj("hive_api_request", map[string]any{"method": "POST", "path": "/nodes/activeplug/" + hivetest.PlugID, "query": map[string]any{"x": "1"}, "body": map[string]any{"status": "OFF"}, "dry_run": true})
	if ws := res["would_send"].(Obj); ws["query"] != "x=1" || strings.Contains(ws["path"].(string), "?") {
		t.Errorf("raw query: %v", ws)
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
	if res["would_send"] != nil || res["result"] == nil {
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
