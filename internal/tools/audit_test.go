package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
)

func readAudit(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestAuditLogRecordsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	o := config.AllTools()
	o.AuditLog = path
	env := newEnv(t, o)

	env.obj("hive_get_overview", nil)
	env.raw("hive_api_request", map[string]any{"path": "/nodes/all"})
	env.obj("hive_boost_heating", map[string]any{"zone": "Heating", "temperature": 22, "minutes": 30})
	if !env.rejects("hive_boost_heating", map[string]any{"zone": "Heating", "temperature": 22, "minutes": 999}) {
		t.Fatal("a 999-minute boost was accepted")
	}
	env.raw("hive_api_request", map[string]any{"method": "post", "path": "/holiday-mode", "body": map[string]any{"enabled": false}})
	env.raw("hive_auth_complete", map[string]any{"code": "123456"})

	entries := readAudit(t, path)
	var tools []string
	for _, e := range entries {
		tools = append(tools, e["tool"].(string))
	}
	if got := strings.Join(tools, ","); got != "hive_boost_heating,hive_boost_heating,hive_api_request,hive_auth_complete" {
		t.Fatalf("logged %s", got)
	}
	first := entries[0]
	if first["outcome"] != "ok" || first["error"] != nil || !strings.HasSuffix(first["time"].(string), "Z") {
		t.Errorf("first entry: %v", first)
	}
	if args := first["arguments"].(map[string]any); args["zone"] != "Heating" || args["minutes"] != 30.0 {
		t.Errorf("arguments: %v", args)
	}
	if e := entries[1]; e["outcome"] != "error" || e["error"] == "" {
		t.Errorf("refused call: %v", e)
	}
	if args := entries[3]["arguments"].(map[string]any); args["code"] != "[redacted]" {
		t.Errorf("the SMS code was logged: %v", args)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != config.AuditLogMode {
		t.Errorf("file mode: %v %v", info, err)
	}
}

func TestAuditLogStderr(t *testing.T) {
	var buf bytes.Buffer
	l := &auditLog{target: config.AuditLogStderr, stderr: &buf}
	l.record("hive_set_plug", json.RawMessage(`{"plug":"Lamp","on":true,"nested":{"refresh_token":"x"}}`), nil, errors.New("Hive said no"))
	var e map[string]any
	if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
		t.Fatalf("%q: %v", buf.String(), err)
	}
	if e["tool"] != "hive_set_plug" || e["outcome"] != "error" || e["error"] != "Hive said no" {
		t.Errorf("entry: %v", e)
	}
	if nested := e["arguments"].(map[string]any)["nested"].(map[string]any); nested["refresh_token"] != "[redacted]" {
		t.Errorf("token logged: %v", nested)
	}

	buf.Reset()
	l.record("hive_set_light", nil, &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Error: no light"}}}, nil)
	if !strings.Contains(buf.String(), `"arguments":{}`) || !strings.Contains(buf.String(), `"error":"no light"`) {
		t.Errorf("entry: %s", buf.String())
	}
}

func TestRawRequestChanges(t *testing.T) {
	for raw, want := range map[string]bool{
		``: false, `{}`: false, `{"method":"GET"}`: false, `{"method":" get "}`: false,
		`{"method":"POST"}`: true, `{"method":"delete"}`: true, `not json`: true,
	} {
		if got := rawRequestChanges(json.RawMessage(raw)); got != want {
			t.Errorf("%q: %v", raw, got)
		}
	}
}
