package hive_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
	"github.com/mattoddie/hive-mcp/internal/hive/hivetest"
)

func loggedIn(t *testing.T, m *hivetest.Mock) *hive.Client {
	t.Helper()
	c, _ := newClient(t, m)
	login(t, c)
	return c
}

func TestNodesAreCachedBriefly(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c := loggedIn(t, m)
	for i := 0; i < 3; i++ {
		if _, err := c.Nodes(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	if n := m.Count("GET", "/nodes/all"); n != 1 {
		t.Errorf("cached reads hit the API %d times", n)
	}
	if _, err := c.Nodes(ctx, true); err != nil || m.Count("GET", "/nodes/all") != 2 {
		t.Errorf("fresh read: %v", err)
	}
	if _, err := c.SetNode(ctx, "activeplug", hivetest.PlugID, hive.Obj{"status": "OFF"}); err != nil {
		t.Fatal(err)
	}
	n, err := c.Nodes(ctx, false)
	if err != nil || m.Count("GET", "/nodes/all") != 3 {
		t.Errorf("a change should drop the cache: %v", err)
	}
	if p := n.Products("activeplug")[0]; hive.Str(p, "state", "status") != "OFF" {
		t.Errorf("plug: %v", p["state"])
	}
	if got := m.Last("POST").Header.Get("User-Agent"); !strings.HasPrefix(got, "hive-mcp") {
		t.Errorf("user agent %q", got)
	}
}

func TestRetriesWhenRateLimited(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c := loggedIn(t, m)
	calls := 0
	m.On("GET", "/holiday-mode", func(hivetest.Request, []string) (int, any) {
		calls++
		if calls < 2 {
			return 429, map[string]string{"message": "slow down"}
		}
		return 200, map[string]any{"active": false}
	})
	var out hive.Obj
	if err := c.Get(ctx, "/holiday-mode", &out); err != nil || calls != 2 {
		t.Errorf("%v after %d calls", err, calls)
	}
}

func TestErrorMessages(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c := loggedIn(t, m)
	m.On("POST", "/nodes/heating/.*", func(hivetest.Request, []string) (int, any) {
		return 400, map[string]any{"errors": []any{map[string]any{"title": "INVALID_TARGET"}}}
	})
	_, err := c.SetNode(ctx, "heating", hivetest.HeatingID, hive.Obj{"target": 99})
	var apiErr *hive.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(err.Error(), "INVALID_TARGET") {
		t.Errorf("400: %v", err)
	}
	err = c.Get(ctx, "/nope", nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("404: %v", err)
	}
	// An API that keeps refusing the token after a refresh gives up with a hint.
	m.On("GET", "/always-401", func(hivetest.Request, []string) (int, any) { return 401, map[string]string{"error": "NOT_AUTHORIZED"} })
	if err := c.Get(ctx, "/always-401", nil); err == nil || !strings.Contains(err.Error(), "hive-mcp login") {
		t.Errorf("401: %v", err)
	}
}

func TestSummaries(t *testing.T) {
	n := &hive.Nodes{Raw: hivetest.SampleNodes()}
	// Monday 2026-10-12 at 07:00 London time (06:00 UTC).
	now := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)

	heating := n.SummarizeHeating(n.Products("heating")[0], now)
	if heating["current_temperature"] != 18.5 || heating["target_temperature"] != 20.0 || heating["mode"] != "schedule" || heating["heating_active"] != true || heating["lowest_battery"] != 70.0 {
		t.Errorf("heating: %v", heating)
	}
	sched := heating["schedule_now"].(hive.Obj)
	if sched["target_temperature"] != 20.0 || sched["since"] != "monday 06:30" || sched["next_change"].(hive.Obj)["at"] != "monday 08:30" {
		t.Errorf("schedule now: %v", sched)
	}

	trv := n.SummarizeHeating(n.Products("trvcontrol")[0], now)
	if trv["kind"] != "TRV zone" || trv["lowest_battery"] != 20.0 || trv["trvs"] != 1 || trv["mode"] != "manual" {
		t.Errorf("trv: %v", trv)
	}

	water := n.SummarizeHotWater(n.Products(hive.HotWaterType)[0], now)
	if water["on"] != false || water["mode"] != "schedule" || water["schedule_now"].(hive.Obj)["status"] != "on" {
		t.Errorf("hot water: %v", water)
	}

	// Late on Sunday, the schedule's next change is Monday morning.
	late := time.Date(2026, 10, 18, 23, 30, 0, 0, time.UTC)
	if s := n.ScheduleNow(n.Products("heating")[0], late); s["next_change"].(hive.Obj)["at"] != "monday 06:30" {
		t.Errorf("wraps to next week: %v", s)
	}

	week := hive.FormatSchedule(hive.Get(n.Products("heating")[0], "state", "schedule").(hive.Obj))
	mon := week["monday"].([]hive.Obj)
	if len(mon) != 4 || mon[0]["from"] != "06:30" || mon[0]["until"] != "08:30" || mon[3]["until"] != "24:00" {
		t.Errorf("schedule: %v", mon)
	}

	plug := n.SummarizePlug(n.Products("activeplug")[0])
	if plug["on"] != true || plug["power_watts"] != 12.0 {
		t.Errorf("plug: %v", plug)
	}
	light := n.SummarizeLight(n.Products(hive.LightTypes...)[1])
	if light["on"] != true || light["colour_temperature_kelvin"] != 3000.0 || light["brightness"] != 60.0 {
		t.Errorf("light: %v", light)
	}
	sensor := n.SummarizeSensor(n.Products(hive.SensorTypes...)[0])
	if sensor["open"] != false || sensor["battery"] != 90.0 {
		t.Errorf("sensor: %v", sensor)
	}
	trvDev := n.SummarizeDevice(n.Device(hivetest.TRVID))
	if trvDev["battery"] != 20.0 || trvDev["calibration"] != "calibrated" || trvDev["child_lock"] != false {
		t.Errorf("trv device: %v", trvDev)
	}
	if hub := n.SummarizeDevice(n.Device(hivetest.HubID)); hub["battery"] != nil || hub["online"] != true {
		t.Errorf("hub: %v", hub)
	}
}

func TestBoostingSummary(t *testing.T) {
	raw := hivetest.SampleNodes()
	p := raw["products"].([]any)[0].(map[string]any)
	p["state"].(map[string]any)["mode"] = "BOOST"
	p["state"].(map[string]any)["boost"] = 25.0
	p["props"].(map[string]any)["previous"] = map[string]any{"mode": "SCHEDULE"}
	n := &hive.Nodes{Raw: raw}
	s := n.SummarizeHeating(n.Products("heating")[0], time.Now())
	if s["mode"] != "boost" || s["boost_minutes_left"] != 25.0 || s["mode_after_boost"] != "schedule" || s["schedule_now"] == nil {
		t.Errorf("boost: %v", s)
	}
}

func TestOnly401RefreshesTheLogin(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c := loggedIn(t, m)
	before := countCalls(m, "InitiateAuth")
	m.On("GET", "/forbidden", func(hivetest.Request, []string) (int, any) { return 403, map[string]string{"error": "FORBIDDEN"} })
	if err := c.Get(ctx, "/forbidden", nil); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("403: %v", err)
	}
	if n := countCalls(m, "InitiateAuth"); n != before {
		t.Errorf("a 403 refreshed the login: %v", m.Cognito)
	}
	if n := m.Count("GET", "/forbidden"); n != 1 {
		t.Errorf("a 403 was retried %d times", n)
	}
}

func TestPostIsNotRetriedAfterAServerError(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c := loggedIn(t, m)
	m.On("POST", "/flaky", func(hivetest.Request, []string) (int, any) { return 503, map[string]string{"error": "UNAVAILABLE"} })
	if err := c.Post(ctx, "/flaky", hive.Obj{}, nil); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("503: %v", err)
	}
	if n := m.Count("POST", "/flaky"); n != 1 {
		t.Errorf("a POST was sent %d times after a 503", n)
	}
	// A GET is retried.
	calls := 0
	m.On("GET", "/flaky", func(hivetest.Request, []string) (int, any) {
		if calls++; calls == 1 {
			return 503, nil
		}
		return 200, map[string]any{}
	})
	if err := c.Get(ctx, "/flaky", nil); err != nil || calls != 2 {
		t.Errorf("GET after a 503: %v (%d calls)", err, calls)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	hit := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer elsewhere.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c, file := newClient(t, m, func(h *config.Hive) { h.APIURL = redirect.URL })
	if err := (hive.TokenStore{File: file}).Save(&hive.Tokens{IDToken: m.IDToken(), ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, "/nodes/all", nil); err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Errorf("redirect: %v", err)
	}
	if hit {
		t.Error("the redirect was followed, with the token")
	}
}
