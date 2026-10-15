package tools

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
	"github.com/mattoddie/hive-mcp/internal/hive/hivetest"
)

func TestOverview(t *testing.T) {
	env := newEnv(t)
	res := env.obj("hive_get_overview", nil)
	zones := res["heating"].([]any)
	if len(zones) != 2 {
		t.Fatalf("zones: %v", zones)
	}
	heating := zones[0].(Obj)
	if heating["name"] != "Heating" || heating["current_temperature"] != 18.5 || heating["heating_active"] != true {
		t.Errorf("heating: %v", heating)
	}
	if res["hot_water"].(Obj)["mode"] != "schedule" || len(res["plugs_lights_and_sensors"].([]any)) != 4 {
		t.Errorf("overview: %v", res)
	}
	att := strings.Join(toStrings(res["needs_attention"]), "\n")
	if !strings.Contains(att, "Bedroom (trv): battery low (20%)") || !strings.Contains(att, "Keypad (keypad): discontinued") {
		t.Errorf("attention: %s", att)
	}
	if res["homes"].([]any)[0].(Obj)["time_zone"] != "Europe/London" {
		t.Errorf("homes: %v", res["homes"])
	}
	// A second read within HIVE_CACHE_SECONDS comes from the cache; fresh asks again.
	env.obj("hive_get_overview", nil)
	env.obj("hive_get_overview", map[string]any{"fresh": true})
	if n := env.mock.Count("GET", "/nodes/all"); n != 2 {
		t.Errorf("nodes fetched %d times", n)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestListDevices(t *testing.T) {
	env := newEnv(t)
	all := env.obj("hive_list_devices", nil)
	if all["total"] != 9.0 {
		t.Errorf("devices: %v", all["total"])
	}
	low := env.obj("hive_list_devices", map[string]any{"low_battery": true})
	if low["total"] != 2.0 { // the TRV at 20% and the dead keypad
		t.Errorf("low battery: %v", low)
	}
	off := env.obj("hive_list_devices", map[string]any{"offline": true})
	if off["total"] != 1.0 || !strings.Contains(off["devices"].([]any)[0].(Obj)["discontinued"].(string), "HomeShield") {
		t.Errorf("offline: %v", off)
	}
	trv := env.obj("hive_list_devices", map[string]any{"type": "trv", "raw": true})
	if trv["devices"].([]any)[0].(Obj)["id"] != hivetest.TRVID {
		t.Errorf("raw trv: %v", trv)
	}
}

func TestHolidayMode(t *testing.T) {
	env := newEnv(t)
	if res := env.obj("hive_get_holiday_mode", nil); res["enabled"] != false {
		t.Errorf("not set: %v", res)
	}
	future := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	res := env.obj("hive_set_holiday_mode", map[string]any{"start": "2030-01-01T08:00", "end": "2030-01-08", "temperature": 10})
	if res["done"] != true || res["start"] != "2030-01-01T08:00:00Z" {
		t.Errorf("set: %v", res)
	}
	body := env.mock.Last("POST").Body.(map[string]any)
	if body["start"] != float64(time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC).UnixMilli()) || body["temperature"] != 10.0 {
		t.Errorf("sent: %v", body)
	}
	if got := env.obj("hive_get_holiday_mode", nil); got["enabled"] != true || got["temperature"] != 10.0 || got["end"] != "2030-01-08T00:00:00Z" {
		t.Errorf("get: %v", got)
	}
	for args, want := range map[string]map[string]any{
		"end must be after start": {"start": "2030-01-08", "end": "2030-01-01", "temperature": 10},
		"steps of 0.5":            {"end": future, "temperature": 10.2},
		"not a date":              {"end": "soon", "temperature": 10},
	} {
		if msg := env.callErr("hive_set_holiday_mode", want); !strings.Contains(msg, args) {
			t.Errorf("%v: %s", want, msg)
		}
	}
	env.obj("hive_cancel_holiday_mode", nil)
	if r := env.mock.Last("DELETE"); r == nil || r.Path != "/holiday-mode" {
		t.Errorf("cancel: %v", r)
	}
	if got := env.obj("hive_get_holiday_mode", nil); got["enabled"] != false {
		t.Errorf("after cancel: %v", got)
	}
}

func TestAuthToolsLogInWithSMSCode(t *testing.T) {
	m := hivetest.New()
	t.Cleanup(m.Server.Close)
	client, err := hive.New(m.Config(filepath.Join(t.TempDir(), "tokens.json")))
	if err != nil {
		t.Fatal(err)
	}
	env := &testEnv{t: t, mock: m, client: client, session: connect(t, client, config.AllTools())}
	if st := env.obj("hive_auth_status", nil); st["logged_in"] != false || st["password_configured"] != true {
		t.Errorf("status before: %v", st)
	}
	start := env.obj("hive_auth_start", nil)
	if !strings.Contains(start["next_step"].(string), "+*******1234") {
		t.Errorf("start: %v", start)
	}
	if msg := env.callErr("hive_auth_complete", map[string]any{"code": "000000"}); !strings.Contains(msg, "isn't right") {
		t.Errorf("wrong code: %s", msg)
	}
	res := env.obj("hive_auth_complete", map[string]any{"code": hivetest.SMSCode})
	if res["logged_in"] != true || res["device_remembered"] != true {
		t.Errorf("complete: %v", res)
	}
	st := env.obj("hive_auth_status", nil)
	if st["logged_in"] != true || st["device_remembered"] != true || st["device_name"] != "hive-mcp-test" {
		t.Errorf("status: %v", st)
	}
	if s := asJSON(t, st); strings.Contains(s, "id-") || strings.Contains(s, "refresh-") || strings.Contains(s, hivetest.Password) {
		t.Errorf("status leaks a secret: %s", s)
	}
	if env.obj("hive_get_overview", nil)["heating"] == nil {
		t.Error("overview after login")
	}
}
