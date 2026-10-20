package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
	"github.com/mattoddie/hive-mcp/internal/hive/hivetest"
)

func TestSmartDevices(t *testing.T) {
	env := newEnv(t)
	res := env.obj("hive_list_smart_devices", nil)
	if len(res["plugs"].([]any)) != 1 || len(res["lights"].([]any)) != 2 || len(res["sensors"].([]any)) != 1 {
		t.Errorf("devices: %v", res)
	}
	if lights := env.obj("hive_list_smart_devices", map[string]any{"kind": "lights"}); lights["plugs"] != nil {
		t.Errorf("kind filter: %v", lights)
	}

	env.obj("hive_set_plug", map[string]any{"on": false})
	if got := env.lastBody(); got != "/nodes/activeplug/"+hivetest.PlugID+` {"status":"OFF"}` {
		t.Errorf("plug sent %s", got)
	}

	env.obj("hive_set_light", map[string]any{"light": "hallway", "brightness": 42})
	if got := env.lastBody(); got != "/nodes/warmwhitelight/"+hivetest.LightID+` {"brightness":40,"status":"ON"}` {
		t.Errorf("brightness sent %s", got)
	}
	before := env.posts()
	res = env.obj("hive_set_light", map[string]any{"light": "living", "brightness": 100, "colour_temperature": 4000})
	if env.posts()-before != 2 || !strings.HasSuffix(env.lastBody(), `{"colourTemperature":4000}`) {
		t.Errorf("tuneable sent %s", env.lastBody())
	}
	if len(res["sent"].([]any)) != 2 {
		t.Errorf("result: %v", res)
	}
	env.obj("hive_set_light", map[string]any{"light": "living", "on": false})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"status":"OFF"}`) {
		t.Errorf("off sent %s", got)
	}

	for args, want := range map[string]map[string]any{
		"warm white bulb":     {"light": "hallway", "colour_temperature": 3000},
		"nothing to change":   {"light": "hallway"},
		"can't also take":     {"light": "hallway", "on": false, "brightness": 50},
		"more than one":       {"on": true},
		`no light called "x"`: {"light": "x", "on": true},
	} {
		if msg := env.callErr("hive_set_light", want); !strings.Contains(msg, args) {
			t.Errorf("%v: %s", want, msg)
		}
	}
	if !env.rejects("hive_set_light", map[string]any{"light": "hallway", "brightness": 2}) {
		t.Error("brightness 2 accepted")
	}
}

func TestRawRequest(t *testing.T) {
	ro := config.AllTools()
	ro.AllowWrites = false
	env := newEnv(t, ro)
	res := env.obj("hive_api_request", map[string]any{"path": "/holiday-mode"})
	if res["enabled"] != false {
		t.Errorf("get: %v", res)
	}
	if msg := env.callErr("hive_api_request", map[string]any{"method": "POST", "path": "/holiday-mode", "body": map[string]any{}}); !strings.Contains(msg, "HIVE_ALLOW_WRITES") {
		t.Errorf("post without writes: %s", msg)
	}
	for _, p := range []string{"/../x", "nodes", "/nodes?x=1", "//x"} {
		if msg := env.callErr("hive_api_request", map[string]any{"path": p}); !strings.Contains(msg, "path must") {
			t.Errorf("%s: %s", p, msg)
		}
	}
	env.obj("hive_api_request", map[string]any{"path": "/1.0/nodes/all", "query": map[string]any{"products": true, "devices": true}})
	if r := env.mock.Last("GET"); r.Path != "/nodes/all" || r.Query.Get("products") != "true" {
		t.Errorf("query: %+v", r)
	}

	rw := newEnv(t)
	if res := rw.raw("hive_api_request", map[string]any{"method": "POST", "path": "/nodes/activeplug/" + hivetest.PlugID, "body": map[string]any{"status": "OFF"}}); res.IsError {
		t.Fatalf("raw post: %s", text(res))
	}
	if got := rw.lastBody(); !strings.HasSuffix(got, `{"status":"OFF"}`) {
		t.Errorf("raw post: %s", got)
	}
}

func TestRawOutputRedactsTheUserProfile(t *testing.T) {
	env := newEnv(t)
	res := env.obj("hive_api_request", map[string]any{"path": "/nodes/all", "query": map[string]any{"products": true, "devices": true}})
	user := res["user"].(Obj)
	for _, k := range []string{"email", "username", "firstName", "lastName", "postcode"} {
		if user[k] != "[redacted]" {
			t.Errorf("user.%s: %v", k, user[k])
		}
	}
	if user["id"] != "user-1" || user["temperatureUnit"] != "C" {
		t.Errorf("kept fields: %v", user)
	}
	if s := asJSON(t, res); strings.Contains(s, "sam@example.com") || strings.Contains(s, "Smith") {
		t.Errorf("profile leaked: %s", s)
	}
	// The profile endpoint itself is redacted at the top level.
	env.mock.On("GET", "/users/me", func(hivetest.Request, []string) (int, any) {
		return 200, map[string]any{"id": "user-1", "email": "sam@example.com", "mobile": "+447700900000"}
	})
	if me := env.obj("hive_api_request", map[string]any{"path": "/users/me"}); me["email"] != "[redacted]" || me["mobile"] != "[redacted]" || me["id"] != "user-1" {
		t.Errorf("/users/me: %v", me)
	}
	// Redacting copies: the cached reading still has the profile for the summaries.
	n, err := env.client.Nodes(context.Background(), false)
	if err != nil || hive.Str(n.Raw, "user", "email") != "sam@example.com" {
		t.Errorf("cache changed: %v", err)
	}
}
