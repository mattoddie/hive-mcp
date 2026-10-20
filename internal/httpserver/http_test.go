package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
	"github.com/mattoddie/hive-mcp/internal/tools"
)

func newServer(t *testing.T, cfg config.HTTP) *httptest.Server {
	t.Helper()
	client, err := hive.New(config.Hive{APIURL: "http://invalid", SSOURL: "http://invalid", TokenFile: t.TempDir() + "/t.json", Timeout: 1e9})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(cfg, tools.NewServer(client, config.AllTools(), nil)))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

const listTools = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

func TestBearerAuthAndToolsList(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "s3cret"})
	if res := post(t, srv.URL+"/mcp", "", listTools); res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("no token: %d", res.StatusCode)
	}
	if res := post(t, srv.URL+"/mcp", "wrong", listTools); res.StatusCode != 401 {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	res := post(t, srv.URL+"/mcp", "s3cret", listTools)
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var out struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Result.Tools) < 20 {
		t.Errorf("tools: %d", len(out.Result.Tools))
	}
}

func TestHealthAndNotFound(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "x"})
	res, err := http.Get(srv.URL + "/health")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("health: %v %v", res, err)
	}
	res.Body.Close()
	res, _ = http.Get(srv.URL + "/other")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("other: %d", res.StatusCode)
	}
}

func TestAllowedHosts(t *testing.T) {
	srv := newServer(t, config.HTTP{AllowedHosts: []string{"mcp.example.com"}})
	if res := post(t, srv.URL+"/mcp", "", listTools); res.StatusCode != 403 {
		t.Errorf("unlisted host: %d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(listTools))
	req.Host = "mcp.example.com:443"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("listed host: %d", res.StatusCode)
	}
}

func send(t *testing.T, method, url string, edit func(*http.Request), body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if edit != nil {
		edit(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestOriginAllowList(t *testing.T) {
	srv := newServer(t, config.HTTP{AllowedHosts: []string{"mcp.example.com", "app.example.com:3000", "[::1]"}})
	for origin, want := range map[string]int{
		"":                               403, // present but empty
		"null":                           403,
		"https://evil.example":           403,
		"https://mcp.example.com":        200,
		"https://mcp.example.com:443":    200,
		"http://mcp.example.com":         200,
		"https://mcp.example.com:8443":   403, // a port-less entry only matches the default port
		"http://app.example.com:3000":    200,
		"http://app.example.com":         403,
		"http://[::1]":                   200,
		"http://[::1]:5173":              403,
		"https://user@mcp.example.com":   403,
		"https://mcp.example.com/path":   403,
		"mcp.example.com":                403,
		"chrome-extension://abcdefghijk": 403,
	} {
		res := send(t, http.MethodPost, srv.URL+"/mcp", func(r *http.Request) {
			r.Host = "mcp.example.com:8080"
			r.Header["Origin"] = []string{origin}
		}, listTools)
		if res.StatusCode != want {
			t.Errorf("Origin %q: got %d, want %d", origin, res.StatusCode, want)
		}
	}
	// Two Origin headers are refused.
	res := send(t, http.MethodPost, srv.URL+"/mcp", func(r *http.Request) {
		r.Host = "mcp.example.com"
		r.Header["Origin"] = []string{"https://mcp.example.com", "https://evil.example"}
	}, listTools)
	if res.StatusCode != 403 {
		t.Errorf("two origins: %d", res.StatusCode)
	}
}

func TestHostAllowListPorts(t *testing.T) {
	srv := newServer(t, config.HTTP{AllowedHosts: []string{"mcp.example.com", "other.example:8080"}})
	for host, want := range map[string]int{
		"mcp.example.com":        200,
		"mcp.example.com:9999":   200, // a port-less entry matches any port
		"other.example:8080":     200,
		"other.example:9090":     403,
		"other.example":          403,
		"evil.mcp.example.com":   403,
		"mcp.example.com.evil.x": 403,
	} {
		res := send(t, http.MethodPost, srv.URL+"/mcp", func(r *http.Request) { r.Host = host }, listTools)
		if res.StatusCode != want {
			t.Errorf("Host %q: got %d, want %d", host, res.StatusCode, want)
		}
	}
}

func TestPostOnlyAndBodyChecks(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "s3cret-s3cret-s3cret"})
	auth := func(r *http.Request) { r.Header.Set("Authorization", "Bearer s3cret-s3cret-s3cret") }
	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		if res := send(t, m, srv.URL+"/mcp", auth, ""); res.StatusCode != 405 || res.Header.Get("Allow") != "POST" {
			t.Errorf("%s: %d", m, res.StatusCode)
		}
	}
	if res := send(t, http.MethodPost, srv.URL+"/mcp", auth, "{not json"); res.StatusCode != 400 {
		t.Errorf("bad JSON: %d", res.StatusCode)
	}
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"x":"` + strings.Repeat("a", maxBodyBytes) + `"}}`
	if res := send(t, http.MethodPost, srv.URL+"/mcp", auth, big); res.StatusCode != 413 {
		t.Errorf("large body: %d", res.StatusCode)
	}
	// Unauthenticated requests are refused before anything else.
	if res := send(t, http.MethodGet, srv.URL+"/mcp", nil, ""); res.StatusCode != 401 {
		t.Errorf("GET without a token: %d", res.StatusCode)
	}
}

func TestHealthURL(t *testing.T) {
	for host, want := range map[string]string{
		"":          "http://127.0.0.1:9000/health",
		"0.0.0.0":   "http://127.0.0.1:9000/health",
		"::":        "http://[::1]:9000/health",
		"10.0.0.5":  "http://10.0.0.5:9000/health",
		"127.0.0.1": "http://127.0.0.1:9000/health",
		"::1":       "http://[::1]:9000/health",
	} {
		if got := HealthURL(config.HTTP{Host: host, Port: 9000}); got != want {
			t.Errorf("%q: %s, want %s", host, got, want)
		}
	}
}
