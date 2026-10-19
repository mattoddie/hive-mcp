package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
	"github.com/mattoddie/hive-mcp/internal/hive/hivetest"
)

type testEnv struct {
	t       *testing.T
	mock    *hivetest.Mock
	client  *hive.Client
	session *mcp.ClientSession
}

// newEnv starts a mock Hive, a client already logged in to it and an MCP
// session with the given tool options (all tools by default).
func newEnv(t *testing.T, opts ...config.Tools) *testEnv {
	t.Helper()
	m := hivetest.New()
	t.Cleanup(m.Server.Close)
	file := filepath.Join(t.TempDir(), "tokens.json")
	if err := (hive.TokenStore{File: file}).Save(&hive.Tokens{
		Username: hivetest.Username, PoolID: hivetest.PoolID, ClientID: hivetest.ClientID,
		IDToken: m.IDToken(), ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	client, err := hive.New(m.Config(file))
	if err != nil {
		t.Fatal(err)
	}
	o := config.AllTools()
	if len(opts) > 0 {
		o = opts[0]
	}
	return &testEnv{t: t, mock: m, client: client, session: connect(t, client, o)}
}

func connect(t *testing.T, client *hive.Client, o config.Tools) *mcp.ClientSession {
	t.Helper()
	server := NewServer(client, o, nil)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func (e *testEnv) raw(name string, args map[string]any) *mcp.CallToolResult {
	e.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// obj runs a tool and decodes its JSON object result, failing on a tool error.
func (e *testEnv) obj(name string, args map[string]any) Obj {
	e.t.Helper()
	res := e.raw(name, args)
	if res.IsError {
		e.t.Fatalf("%s returned an error: %s", name, text(res))
	}
	var out Obj
	if err := json.Unmarshal([]byte(text(res)), &out); err != nil {
		e.t.Fatalf("%s: bad JSON %q: %v", name, text(res), err)
	}
	return out
}

// callErr runs a tool that should fail and returns its error text.
func (e *testEnv) callErr(name string, args map[string]any) string {
	e.t.Helper()
	res := e.raw(name, args)
	if !res.IsError {
		e.t.Fatalf("%s: expected an error, got %s", name, text(res))
	}
	return text(res)
}

// rejects reports whether the server refused the call, as a protocol error or a tool error.
func (e *testEnv) rejects(name string, args map[string]any) bool {
	e.t.Helper()
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	return err != nil || res.IsError
}

// lastBody is the JSON body of the last POST, for comparing what was sent.
func (e *testEnv) lastBody() string {
	e.t.Helper()
	r := e.mock.Last("POST")
	if r == nil {
		e.t.Fatal("nothing was sent")
	}
	return r.Path + " " + asJSON(e.t, r.Body)
}

func (e *testEnv) posts() int {
	n := 0
	for _, r := range e.mock.Requests() {
		if r.Method != "GET" {
			n++
		}
	}
	return n
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func toolNames(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	out := map[string]*mcp.Tool{}
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		out[tool.Name] = tool
	}
	return out
}

func TestFind(t *testing.T) {
	list := []Obj{
		{"id": "a", "state": Obj{"name": "Heating"}},
		{"id": "b", "state": Obj{"name": "Bedroom"}},
		{"id": "c", "state": Obj{"name": "Bedroom 2"}},
	}
	for ref, want := range map[string]string{"a": "a", "heating": "a", "HEAT": "a", "bedroom": "b", "Bedroom 2": "c"} {
		if o, err := find(list, ref, "zone"); err != nil || o["id"] != want {
			t.Errorf("%q: %v %v", ref, o, err)
		}
	}
	for ref, want := range map[string]string{"": "more than one", "bed": "matches more than one", "kitchen": `no zone called "kitchen"`} {
		if _, err := find(list, ref, "zone"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", ref, err)
		}
	}
	if o, err := find(list[:1], "", "zone"); err != nil || o["id"] != "a" {
		t.Errorf("only one: %v", err)
	}
}

func TestCheckTemperature(t *testing.T) {
	for _, ok := range []float64{5, 7, 20.5, 32} {
		if err := checkTemperature("t", ok, 32); err != nil {
			t.Errorf("%g: %v", ok, err)
		}
	}
	for bad, want := range map[float64]string{4.5: "between", 32.5: "between", 20.3: "steps of 0.5"} {
		if err := checkTemperature("t", bad, 32); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%g: %v", bad, err)
		}
	}
	if err := checkTemperature("t", 25.5, 25); err == nil || !strings.Contains(err.Error(), "HIVE_MAX_TEMP") {
		t.Errorf("above HIVE_MAX_TEMP: %v", err)
	}
}

func TestParseWhen(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/London")
	for in, want := range map[string]string{
		"2026-12-20":                "2026-12-20T00:00:00Z",
		"2026-07-01T08:30":          "2026-07-01T07:30:00Z",
		"2026-07-01 08:30":          "2026-07-01T07:30:00Z",
		"2026-07-01T08:30:00+02:00": "2026-07-01T06:30:00Z",
	} {
		got, err := parseWhen(in, loc)
		if err != nil || got.UTC().Format(time.RFC3339) != want {
			t.Errorf("%s: %v %v", in, got.UTC(), err)
		}
	}
	if _, err := parseWhen("next tuesday", loc); err == nil {
		t.Error("accepted free text")
	}
}
