package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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

func TestSetLightReportsAPartialChange(t *testing.T) {
	env := newEnv(t)
	env.mock.On("POST", "/nodes/tuneablelight/.*", func(r hivetest.Request, _ []string) (int, any) {
		if b, _ := r.Body.(map[string]any); b["colourTemperature"] != nil {
			return 500, map[string]any{"error": "boom"}
		}
		return 200, []any{}
	})
	res := env.raw("hive_set_light", map[string]any{"light": "living", "brightness": 50, "colour_temperature": 4000})
	if !res.IsError || len(res.Content) != 2 {
		t.Fatalf("want an error with the partial result, got %v", res.Content)
	}
	if msg := text(res); !strings.Contains(msg, "next change failed") || !strings.Contains(msg, "brightness") {
		t.Errorf("error: %s", msg)
	}
	var partial Obj
	if err := json.Unmarshal([]byte(res.Content[1].(*mcp.TextContent).Text), &partial); err != nil {
		t.Fatal(err)
	}
	if partial["done"] != "partly" || len(partial["not_sent"].([]any)) != 1 {
		t.Errorf("partial result: %v", partial)
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
	for _, p := range []string{"/../x", "nodes", "/nodes?x=1", "//x", "/nodes/.", "/nodes/..", "/nodes/%252e%252e", "/nodes%25", "/nodes/%0a", "/nodes/%7f", "/nodes/\tall"} {
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

func TestSetLightColour(t *testing.T) {
	env := newEnv(t)
	n := env.mock.Nodes()
	n["products"] = append(n["products"].([]any), map[string]any{
		"id": "light-0000-4000-8000-000000000003", "type": "colourtuneablelight", "sortOrder": 9,
		"props": map[string]any{"online": true, "colourTemperature": map[string]any{"min": 2700, "max": 6535}},
		"state": map[string]any{"name": "Lounge lamp", "status": "OFF", "hue": 45, "saturation": 65, "value": 100, "brightness": 100, "colourMode": "WHITE", "colourTemperature": 3300},
	})
	env.mock.SetNodes(n)

	res := env.obj("hive_set_light", map[string]any{"light": "lounge", "hue": 240})
	if got := env.lastBody(); got != `/nodes/colourtuneablelight/light-0000-4000-8000-000000000003 {"colourMode":"COLOUR","hue":"240","saturation":"65","status":"ON","value":"100"}` {
		t.Errorf("sent %s", got)
	}
	if now := res["now"].(map[string]any); now["colour_mode"] != "colour" || now["on"] != true {
		t.Errorf("result: %v", res)
	}
	before := env.posts()
	env.obj("hive_set_light", map[string]any{"light": "lounge", "saturation": 30, "brightness": 50})
	if env.posts()-before != 1 || !strings.HasSuffix(env.lastBody(), `{"colourMode":"COLOUR","hue":"240","saturation":"30","status":"ON","value":"50"}`) {
		t.Errorf("sent %s", env.lastBody())
	}

	for want, args := range map[string]map[string]any{
		"isn't a colour bulb": {"light": "living", "hue": 10},
		"not both":            {"light": "lounge", "hue": 10, "colour_temperature": 3000},
		"can't also take":     {"light": "lounge", "on": false, "saturation": 10},
	} {
		if msg := env.callErr("hive_set_light", args); !strings.Contains(msg, want) {
			t.Errorf("%v: %s", args, msg)
		}
	}
	if !env.rejects("hive_set_light", map[string]any{"light": "lounge", "hue": 360}) {
		t.Error("hue 360 accepted")
	}
}
