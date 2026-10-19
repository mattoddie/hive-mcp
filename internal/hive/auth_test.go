package hive_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
	"github.com/mattoddie/hive-mcp/internal/hive/hivetest"
)

var ctx = context.Background()

func newClient(t *testing.T, m *hivetest.Mock, edit ...func(*config.Hive)) (*hive.Client, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "data", "tokens.json")
	cfg := m.Config(file)
	for _, e := range edit {
		e(&cfg)
	}
	c, err := hive.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, file
}

// login runs the whole first login: password, SMS code, device registration.
func login(t *testing.T, c *hive.Client) {
	t.Helper()
	res, err := c.Auth.StartLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.CodeRequired || res.CodeSentTo != "+*******1234" {
		t.Fatalf("start: %+v", res)
	}
	res, err = c.Auth.CompleteLogin(ctx, hivetest.SMSCode)
	if err != nil {
		t.Fatal(err)
	}
	if !res.LoggedIn || !res.DeviceRemembered {
		t.Fatalf("complete: %+v", res)
	}
}

func countCalls(m *hivetest.Mock, call string) int {
	n := 0
	for _, c := range m.Cognito {
		if c == call {
			n++
		}
	}
	return n
}

func TestFirstLoginWithSMSRemembersDevice(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, file := newClient(t, m)

	res, err := c.Auth.StartLogin(ctx)
	if err != nil || !res.CodeRequired {
		t.Fatalf("start: %+v %v", res, err)
	}
	if _, err := c.Auth.CompleteLogin(ctx, "999999"); err == nil || !strings.Contains(err.Error(), "isn't right") {
		t.Errorf("wrong code: %v", err)
	}
	if _, err := c.Auth.CompleteLogin(ctx, "not a code"); err == nil {
		t.Error("letters accepted as a code")
	}
	res, err = c.Auth.CompleteLogin(ctx, hivetest.SMSCode)
	if err != nil || !res.LoggedIn || !res.DeviceRemembered {
		t.Fatalf("complete: %+v %v", res, err)
	}
	if m.SMSSent != 1 || m.RememberedDevices() != 1 || m.SSOHits != 1 {
		t.Errorf("sms %d, devices %d, sso %d", m.SMSSent, m.RememberedDevices(), m.SSOHits)
	}
	if countCalls(m, "ConfirmDevice") != 1 || countCalls(m, "UpdateDeviceStatus") != 1 {
		t.Errorf("calls: %v", m.Cognito)
	}

	st, err := os.Stat(file)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", st, err)
	}
	saved, _ := hive.TokenStore{File: file}.Load()
	if saved.Device == nil || saved.Device.Password == "" || saved.PoolID != hivetest.PoolID || saved.ClientID != hivetest.ClientID || saved.Username != hivetest.Username {
		t.Errorf("saved: %+v", saved)
	}

	// The API works with the new token.
	if _, err := c.Nodes(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := m.Last("GET").Header.Get("Authorization"); got != saved.IDToken || strings.HasPrefix(got, "Bearer") {
		t.Errorf("Authorization header %q", got)
	}

	// Status never shows tokens or device secrets.
	b, _ := json.Marshal(c.Auth.Status())
	for _, secret := range []string{saved.IDToken, saved.AccessToken, saved.RefreshToken, saved.Device.Password, saved.Device.Key, hivetest.Password} {
		if strings.Contains(string(b), secret) {
			t.Errorf("status leaks a secret: %s", b)
		}
	}
	if !strings.Contains(string(b), `"device_remembered":true`) || !strings.Contains(string(b), `"can_log_in_again_without_sms":true`) {
		t.Errorf("status: %s", b)
	}
}

func TestRefreshUsesRefreshTokenWithDeviceKey(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m)
	login(t, c)
	m.ExpireTokens()
	c.Invalidate()
	if _, err := c.Nodes(ctx, true); err != nil {
		t.Fatal(err)
	}
	if countCalls(m, "InitiateAuth") != 2 || m.SMSSent != 1 {
		t.Errorf("calls %v, sms %d", m.Cognito, m.SMSSent)
	}
}

func TestDeviceLoginSkipsSMSWhenRefreshTokenRunsOut(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, file := newClient(t, m)
	login(t, c)
	before, _ := hive.TokenStore{File: file}.Load()
	m.ExpireTokens()
	m.ExpireRefreshTokens()
	if _, err := c.Nodes(ctx, true); err != nil {
		t.Fatal(err)
	}
	if m.SMSSent != 1 {
		t.Errorf("an SMS code was sent: %d", m.SMSSent)
	}
	for _, call := range []string{"RespondToAuthChallenge:DEVICE_SRP_AUTH", "RespondToAuthChallenge:DEVICE_PASSWORD_VERIFIER"} {
		if countCalls(m, call) != 1 {
			t.Errorf("%s: %v", call, m.Cognito)
		}
	}
	after, _ := hive.TokenStore{File: file}.Load()
	if after.Device == nil || after.Device.Key != before.Device.Key || after.RefreshToken == before.RefreshToken {
		t.Errorf("device should be kept and tokens renewed: %+v", after)
	}

	// Logging in again from the terminal uses the device too.
	res, err := c.Auth.StartLogin(ctx)
	if err != nil || !res.LoggedIn || !res.UsedDevice || m.SMSSent != 1 {
		t.Errorf("second login: %+v %v (sms %d)", res, err, m.SMSSent)
	}
}

func TestServerNeverTriggersAnSMS(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m)
	_, err := c.Nodes(ctx, false)
	if !hive.IsNotLoggedIn(err) {
		t.Errorf("expected not logged in, got %v", err)
	}
	if len(m.Cognito) != 0 || m.SMSSent != 0 {
		t.Errorf("cognito was called: %v", m.Cognito)
	}
}

func TestForgottenDeviceNeedsNewLoginAndBacksOff(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m)
	login(t, c)
	m.ForgetDevices()
	m.ExpireTokens()
	m.ExpireRefreshTokens()
	_, err := c.Nodes(ctx, true)
	if err == nil || !strings.Contains(err.Error(), "hive-mcp login") {
		t.Errorf("error: %v", err)
	}
	calls := len(m.Cognito)
	if _, err := c.Nodes(ctx, true); err == nil || !strings.Contains(err.Error(), "not retrying") {
		t.Errorf("second attempt: %v", err)
	}
	if len(m.Cognito) != calls || m.SMSSent != 1 {
		t.Errorf("retried too soon: %v (sms %d)", m.Cognito[calls:], m.SMSSent)
	}

	// A new terminal login starts again as a new device, with an SMS code.
	newClock(c.Auth).advance(2 * time.Minute) // past the cooldown since the first code
	res, err := c.Auth.StartLogin(ctx)
	if err != nil || !res.CodeRequired {
		t.Fatalf("relogin: %+v %v", res, err)
	}
	if _, err := c.Auth.CompleteLogin(ctx, hivetest.SMSCode); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Nodes(ctx, true); err != nil {
		t.Errorf("after new login: %v", err)
	}
}

func TestLoginErrors(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m, func(h *config.Hive) { h.Password = "wrong" })
	if _, err := c.Auth.StartLogin(ctx); err == nil || !strings.Contains(err.Error(), "HIVE_PASSWORD") {
		t.Errorf("wrong password: %v", err)
	}
	c, _ = newClient(t, m, func(h *config.Hive) { h.Username = "nobody@example.com" })
	if _, err := c.Auth.StartLogin(ctx); err == nil || !strings.Contains(err.Error(), "doesn't recognise that username") {
		t.Errorf("unknown user: %v", err)
	}
	c, _ = newClient(t, m, func(h *config.Hive) { h.Password = "" })
	if _, err := c.Auth.StartLogin(ctx); err == nil || !strings.Contains(err.Error(), "HIVE_PASSWORD") {
		t.Errorf("no password: %v", err)
	}
	if _, err := c.Auth.CompleteLogin(ctx, "123456"); err == nil || !strings.Contains(err.Error(), "no login waiting") {
		t.Errorf("no pending login: %v", err)
	}
	// The username is matched case-insensitively, as the Hive app does.
	c, _ = newClient(t, m, func(h *config.Hive) { h.Username = "  Sam@Example.com " })
	if res, err := c.Auth.StartLogin(ctx); err != nil || !res.CodeRequired {
		t.Errorf("mixed-case username: %+v %v", res, err)
	}
}

func TestAccountWithoutTwoStepVerification(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	m.MFA, m.DeviceTracking = false, false
	c, _ := newClient(t, m)
	res, err := c.Auth.StartLogin(ctx)
	if err != nil || !res.LoggedIn || res.DeviceRemembered {
		t.Fatalf("login: %+v %v", res, err)
	}
	m.ExpireTokens()
	m.ExpireRefreshTokens()
	if _, err := c.Nodes(ctx, true); err != nil {
		t.Errorf("relogin without a device: %v", err)
	}
}

func TestExplicitCognitoSettingsSkipDiscovery(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m, func(h *config.Hive) { h.PoolID, h.ClientID = hivetest.PoolID, hivetest.ClientID })
	login(t, c)
	if m.SSOHits != 0 {
		t.Errorf("sign-in page fetched %d times", m.SSOHits)
	}
	c, _ = newClient(t, m, func(h *config.Hive) { h.PoolID, h.ClientID = hivetest.PoolID, "other-client" })
	if _, err := c.Auth.StartLogin(ctx); err == nil {
		t.Error("wrong client ID accepted")
	}
}

func TestServerPicksUpLoginFromAnotherProcess(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	server, file := newClient(t, m)
	if _, err := server.Nodes(ctx, false); !hive.IsNotLoggedIn(err) {
		t.Fatalf("expected not logged in: %v", err)
	}
	cfg := m.Config(file)
	cli, err := hive.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	login(t, cli)
	if _, err := server.Nodes(ctx, false); err != nil {
		t.Errorf("server didn't pick up the login: %v", err)
	}
}

func TestLogoutForgetsDevice(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, file := newClient(t, m)
	login(t, c)
	forgot, err := c.Auth.Logout(ctx)
	if err != nil || !forgot || m.RememberedDevices() != 0 {
		t.Errorf("logout: %v %v %d", forgot, err, m.RememberedDevices())
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("token file still there: %v", err)
	}
	if c.Auth.HasCredentials() {
		t.Error("still has credentials")
	}
}

func TestUnwritableTokenFileWarns(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if os.WriteFile(filepath.Join(dir, "probe"), nil, 0o600) == nil {
		t.Skip("running as a user that can write anywhere")
	}
	c, _ := newClient(t, m, func(h *config.Hive) { h.TokenFile = filepath.Join(dir, "tokens.json") })
	login(t, c)
	if w, _ := c.Auth.Status()["warning"].(string); !strings.Contains(w, "could not save tokens") {
		t.Errorf("warning: %q", w)
	}
}

// clock lets a test move the Auth's time forward.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock(a *hive.Auth) *clock       { c := &clock{t: time.Now()}; hive.SetNow(a, c.now); return c }

func TestStartLoginWaitsBeforeTextingAnotherCode(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m)
	clk := newClock(c.Auth)
	if res, err := c.Auth.StartLogin(ctx); err != nil || !res.CodeRequired {
		t.Fatalf("start: %+v %v", res, err)
	}
	calls := len(m.Cognito)
	if _, err := c.Auth.StartLogin(ctx); err == nil || !strings.Contains(err.Error(), "less than a minute ago") {
		t.Errorf("second start: %v", err)
	}
	if m.SMSSent != 1 || len(m.Cognito) != calls {
		t.Errorf("a second code was sent: sms %d, calls %v", m.SMSSent, m.Cognito[calls:])
	}
	clk.advance(61 * time.Second)
	if res, err := c.Auth.StartLogin(ctx); err != nil || !res.CodeRequired || m.SMSSent != 2 {
		t.Errorf("after the cooldown: %+v %v (sms %d)", res, err, m.SMSSent)
	}
}

func TestStartLoginBacksOffAfterARefusedLogin(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m, func(h *config.Hive) { h.Password = "wrong" })
	clk := newClock(c.Auth)
	if _, err := c.Auth.StartLogin(ctx); err == nil || !strings.Contains(err.Error(), "HIVE_PASSWORD") {
		t.Fatalf("wrong password: %v", err)
	}
	calls := len(m.Cognito)
	for range 3 {
		if _, err := c.Auth.StartLogin(ctx); err == nil || !strings.Contains(err.Error(), "not trying again until") {
			t.Errorf("retry: %v", err)
		}
	}
	if len(m.Cognito) != calls {
		t.Errorf("retried during the backoff: %v", m.Cognito[calls:])
	}
	clk.advance(6 * time.Minute)
	if _, err := c.Auth.StartLogin(ctx); err == nil || strings.Contains(err.Error(), "not trying again") {
		t.Errorf("after the backoff: %v", err)
	}
	if len(m.Cognito) == calls {
		t.Error("didn't try again after the backoff")
	}
}

func TestTooManyWrongCodesEndsTheLogin(t *testing.T) {
	m := hivetest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m)
	if _, err := c.Auth.StartLogin(ctx); err != nil {
		t.Fatal(err)
	}
	var err error
	for range 5 {
		_, err = c.Auth.CompleteLogin(ctx, "000000")
	}
	if err == nil || !strings.Contains(err.Error(), "too many wrong codes") {
		t.Errorf("fifth wrong code: %v", err)
	}
	if _, err := c.Auth.CompleteLogin(ctx, hivetest.SMSCode); err == nil || !strings.Contains(err.Error(), "no login waiting") {
		t.Errorf("right code after too many wrong ones: %v", err)
	}
}
