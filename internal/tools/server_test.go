package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
)

func TestGatesControlWhichToolsExist(t *testing.T) {
	all := config.AllTools()
	registered := map[string]Info{}
	NewServer(nil, all, registered)

	readOnly := all
	readOnly.AllowWrites = false

	env := newEnv(t, readOnly)
	reads := toolNames(t, env.session)
	allTools := toolNames(t, connect(t, env.client, all))

	if len(allTools) != len(registered) {
		t.Errorf("registered %d, listed %d", len(registered), len(allTools))
	}
	for name, info := range registered {
		_, inRead := reads[name]
		tool := allTools[name]
		switch info.Gate {
		case Read:
			if !inRead {
				t.Errorf("%s: read tool missing in read-only mode", name)
			}
			if !tool.Annotations.ReadOnlyHint && name != "hive_api_request" && !strings.HasPrefix(name, "hive_auth_") {
				t.Errorf("%s: read tool without readOnlyHint", name)
			}
		case Login:
			if tool.Annotations.ReadOnlyHint {
				t.Errorf("%s: login tool marked read-only", name)
			}
		case Write:
			if inRead {
				t.Errorf("%s: write tool registered without HIVE_ALLOW_WRITES", name)
			}
			if tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil {
				t.Errorf("%s: write tool annotations %+v", name, tool.Annotations)
			}
		}
	}
	if tool := allTools["hive_auth_start"]; tool == nil || !*tool.Annotations.OpenWorldHint {
		t.Error("hive_auth_start sends a text message and should be open-world")
	}
	if raw := reads["hive_api_request"]; raw == nil || !raw.Annotations.ReadOnlyHint {
		t.Error("the raw tool should be read-only without writes")
	}
	if raw := allTools["hive_api_request"]; raw == nil || raw.Annotations.ReadOnlyHint || !*raw.Annotations.DestructiveHint {
		t.Error("the raw tool should be marked destructive with writes")
	}
	if len(reads) < 12 || len(allTools) < 22 {
		t.Errorf("unexpectedly few tools: %d read, %d total", len(reads), len(allTools))
	}
}

func TestToolsetsFilter(t *testing.T) {
	o := config.AllTools()
	o.Toolsets = map[string]bool{"hotwater": true}
	env := newEnv(t, o)
	names := toolNames(t, env.session)
	for name := range names {
		if !strings.Contains(name, "hot_water") {
			t.Errorf("%s registered with only the hotwater toolset", name)
		}
	}
	if len(names) != 5 {
		t.Errorf("hotwater tools: %d", len(names))
	}
}

func TestSchemasDescribeEveryArgument(t *testing.T) {
	env := newEnv(t)
	for name, tool := range toolNames(t, env.session) {
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s: missing title or description", name)
		}
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for arg, p := range props {
			if d, _ := p.(map[string]any)["description"].(string); d == "" {
				t.Errorf("%s.%s has no description", name, arg)
			}
		}
	}
	tool := toolNames(t, env.session)["hive_boost_heating"]
	temp := tool.InputSchema.(map[string]any)["properties"].(map[string]any)["temperature"].(map[string]any)
	if temp["minimum"] != 5.0 || temp["maximum"] != 32.0 {
		t.Errorf("temperature range: %v", temp)
	}
}

func TestPrompts(t *testing.T) {
	env := newEnv(t)
	var names []string
	for p, err := range env.session.Prompts(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, p.Name)
	}
	if len(names) != len(prompts) {
		t.Errorf("prompts: %v", names)
	}
	res, err := env.session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "warm_up", Arguments: map[string]string{"temperature": "22"}})
	if err != nil {
		t.Fatal(err)
	}
	txt := res.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(txt, "22 °C for 60 minutes") || !strings.Contains(txt, "hive_boost_heating") {
		t.Errorf("prompt text: %s", txt)
	}
}

func TestToolErrorsAreToolResults(t *testing.T) {
	env := newEnv(t)
	msg := env.callErr("hive_get_heating_schedule", map[string]any{"zone": "Kitchen"})
	if !strings.Contains(msg, `no heating zone called "Kitchen"`) || !strings.Contains(msg, `"Bedroom"`) {
		t.Errorf("error: %s", msg)
	}
	// Schema validation rejects out-of-range input before the handler runs.
	if !env.rejects("hive_set_heating_temperature", map[string]any{"temperature": 40}) {
		t.Error("40 °C was accepted")
	}
	if env.posts() != 0 {
		t.Error("a rejected call reached Hive")
	}
}

func TestNotLoggedIn(t *testing.T) {
	env := newEnv(t)
	if _, err := env.client.Auth.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	msg := env.callErr("hive_get_overview", nil)
	if !strings.Contains(msg, "hive-mcp login") || !strings.Contains(msg, "hive_auth_start") {
		t.Errorf("error: %s", msg)
	}
}

func TestLoginToolsNeedTheirOwnSwitch(t *testing.T) {
	o := config.AllTools()
	o.AllowLoginTools = false
	names := toolNames(t, newEnv(t, o).session)
	for _, name := range []string{"hive_auth_start", "hive_auth_complete"} {
		if names[name] != nil {
			t.Errorf("%s registered without HIVE_ALLOW_LOGIN_TOOLS", name)
		}
	}
	if names["hive_auth_status"] == nil {
		t.Error("hive_auth_status should always be registered")
	}
	if names["hive_set_heating_temperature"] == nil {
		t.Error("the login switch shouldn't affect write tools")
	}
}
