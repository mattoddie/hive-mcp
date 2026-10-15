package hive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mattoddie/hive-mcp/internal/config"
)

// Device is the remembered device this server registered with Hive's login
// service after the first SMS code. Logging in as it skips the SMS code.
// Password is a secret, like the account password.
type Device struct {
	Key          string `json:"key"`
	GroupKey     string `json:"group_key"`
	Password     string `json:"password"`
	Name         string `json:"name,omitempty"`
	RegisteredAt string `json:"registered_at,omitempty"`
}

// Tokens is what the token file holds.
type Tokens struct {
	Username string `json:"username,omitempty"`
	// PoolID and ClientID are the Cognito pool and client the tokens and device belong to.
	PoolID       string `json:"pool_id,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresAt is when the ID and access tokens expire (Unix milliseconds).
	ExpiresAt  int64   `json:"expires_at,omitempty"`
	ObtainedAt string  `json:"obtained_at,omitempty"`
	Device     *Device `json:"device,omitempty"`
	// NoMFA records that the last full login didn't ask for an SMS code, so
	// logging in again without a remembered device won't send one.
	NoMFA bool `json:"no_mfa,omitempty"`
}

const (
	expiryMargin = 2 * time.Minute // refresh a little before the tokens expire
	// pendingLoginTTL is how long an SMS code can be entered. Cognito's challenge session lasts about three minutes.
	pendingLoginTTL = 3 * time.Minute
	// reloginBackoff stops repeated failed logins, which could get the account locked.
	reloginBackoff = 5 * time.Minute
)

// ErrNotLoggedIn is returned when there are no tokens and the server can't log in by itself.
var ErrNotLoggedIn = errors.New("not logged in to Hive. Run `hive-mcp login` once (in Docker: `docker compose run --rm hive-mcp login`) to enter the SMS code and remember this server, or use the hive_auth_start tool")

// TokenStore reads and writes the token file atomically, readable only by its owner.
type TokenStore struct{ File string }

// Load returns nil when the file doesn't exist yet.
func (s TokenStore) Load() (*Tokens, error) {
	b, err := os.ReadFile(s.File)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the token file %s: %w", s.File, err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, nil
	}
	var t Tokens
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("the token file %s is not valid JSON; delete it and log in again", s.File)
	}
	return &t, nil
}

// Save writes the tokens via a temporary file and rename, so a crash never leaves a half-written file.
func (s TokenStore) Save(t *Tokens) error {
	if err := os.MkdirAll(filepath.Dir(s.File), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := fmt.Sprintf("%s.%d.tmp", s.File, os.Getpid())
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.File)
}

// Delete removes the token file.
func (s TokenStore) Delete() error {
	err := os.Remove(s.File)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type pendingLogin struct {
	cognito  *cognito
	session  string
	userID   string // USER_ID_FOR_SRP
	username string
	created  time.Time
}

// LoginResult describes a login step without revealing any secrets.
type LoginResult struct {
	LoggedIn         bool   `json:"logged_in"`
	CodeRequired     bool   `json:"code_required,omitempty"`
	CodeSentTo       string `json:"code_sent_to,omitempty"`
	Username         string `json:"username,omitempty"`
	DeviceRemembered bool   `json:"device_remembered"`
	UsedDevice       bool   `json:"used_remembered_device,omitempty"`
	TokenFile        string `json:"token_file,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

// Auth logs in to Hive through AWS Cognito and keeps the tokens fresh.
//
// The first login needs the account password and, for accounts with
// two-step verification (all of them, these days), an SMS code. That login
// also registers this server as a "remembered device", with a random device
// password, so that later logins (when the refresh token has run out) can use
// device SRP authentication instead of a new SMS code. Everything needed for
// that is kept in the token file, which is written atomically with mode 0600.
type Auth struct {
	cfg   config.Hive
	http  *http.Client
	Store TokenStore

	mu             sync.Mutex // guards the fields below
	tokens         *Tokens
	pending        *pendingLogin
	ids            [2]string // discovered pool and client IDs
	lastError      string
	persistWarning string
	reloginFailed  time.Time
	fileMod        time.Time // modification time of the token file when last read or written

	refreshMu sync.Mutex // serialises refreshes and logins
	now       func() time.Time
}

// NewAuth loads saved tokens.
func NewAuth(cfg config.Hive, client *http.Client) (*Auth, error) {
	a := &Auth{cfg: cfg, http: client, Store: TokenStore{File: cfg.TokenFile}, now: time.Now}
	saved, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	a.tokens = saved
	a.fileMod = a.modTime()
	return a, nil
}

func (a *Auth) modTime() time.Time {
	st, err := os.Stat(a.Store.File)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// reloadLocked picks up tokens written by another process, such as
// `hive-mcp login` run while the server is up. Callers hold a.mu.
func (a *Auth) reloadLocked() {
	m := a.modTime()
	if m.IsZero() || m.Equal(a.fileMod) {
		return
	}
	a.fileMod = m
	t, err := a.Store.Load()
	if err != nil || t == nil {
		return
	}
	a.tokens = t
	a.lastError = ""
	a.reloginFailed = time.Time{}
}

func normaliseUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

// username is the configured username, or the one the tokens were issued to.
func (a *Auth) usernameLocked() string {
	if u := normaliseUsername(a.cfg.Username); u != "" {
		return u
	}
	if a.tokens != nil {
		return a.tokens.Username
	}
	return ""
}

// deviceLocked returns the remembered device, if it belongs to the configured user. Callers hold a.mu.
func (a *Auth) deviceLocked() *Device {
	t := a.tokens
	if t == nil || t.Device == nil || t.Device.Key == "" {
		return nil
	}
	if u := normaliseUsername(a.cfg.Username); u != "" && t.Username != "" && u != t.Username {
		return nil
	}
	return t.Device
}

// HasCredentials reports whether the server can try to get a token: saved tokens, or a password to log in with.
func (a *Auth) HasCredentials() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reloadLocked()
	t := a.tokens
	return t != nil && (t.IDToken != "" || t.RefreshToken != "" || (t.Device != nil && a.cfg.Password != ""))
}

// Status describes the login without revealing any tokens or secrets.
func (a *Auth) Status() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reloadLocked()
	t := a.tokens
	if t == nil {
		t = &Tokens{}
	}
	now := a.now()
	out := map[string]any{
		"logged_in":                    t.IDToken != "" || t.RefreshToken != "",
		"username":                     a.usernameLocked(),
		"password_configured":          a.cfg.Password != "",
		"token_file":                   a.cfg.TokenFile,
		"device_remembered":            a.deviceLocked() != nil,
		"has_refresh_token":            t.RefreshToken != "",
		"token_valid":                  t.IDToken != "" && t.ExpiresAt > now.UnixMilli(),
		"can_log_in_again_without_sms": a.deviceLocked() != nil && a.cfg.Password != "",
	}
	if d := a.deviceLocked(); d != nil {
		out["device_name"] = d.Name
		if d.RegisteredAt != "" {
			out["device_registered"] = d.RegisteredAt
		}
	}
	if t.ExpiresAt > 0 {
		out["token_expires"] = time.UnixMilli(t.ExpiresAt).UTC().Format(time.RFC3339)
	}
	if t.ObtainedAt != "" {
		out["last_login_or_refresh"] = t.ObtainedAt
	}
	if a.pending != nil && now.Sub(a.pending.created) < pendingLoginTTL {
		out["login_waiting_for_sms_code"] = true
	}
	if a.lastError != "" {
		out["error"] = a.lastError
	}
	if a.persistWarning != "" {
		out["warning"] = a.persistWarning
	}
	return out
}

// IDToken returns a valid ID token (what Hive's API takes), refreshing or logging in again first when needed.
func (a *Auth) IDToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	a.reloadLocked()
	if t := a.tokens; t != nil && t.IDToken != "" && time.UnixMilli(t.ExpiresAt).Add(-expiryMargin).After(a.now()) {
		a.mu.Unlock()
		return t.IDToken, nil
	}
	a.mu.Unlock()
	return a.Refresh(ctx, "")
}

// Refresh gets new tokens: with the refresh token if it still works,
// otherwise by logging in again as the remembered device. rejected is the
// token the API just refused (if any); when another caller has already
// replaced it, the newer token is returned instead.
func (a *Auth) Refresh(ctx context.Context, rejected string) (string, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()

	a.mu.Lock()
	a.reloadLocked()
	t := a.tokens
	if t != nil && t.IDToken != "" && t.IDToken != rejected && time.UnixMilli(t.ExpiresAt).Add(-expiryMargin).After(a.now()) {
		a.mu.Unlock()
		return t.IDToken, nil
	}
	var refreshToken string
	var device *Device
	if t != nil {
		refreshToken = t.RefreshToken
		device = a.deviceLocked()
	}
	a.mu.Unlock()

	if refreshToken != "" {
		c, err := a.cognito(ctx)
		if err != nil {
			return "", a.fail(err)
		}
		params := map[string]string{"REFRESH_TOKEN": refreshToken}
		if device != nil {
			params["DEVICE_KEY"] = device.Key
		}
		res, err := c.initiateAuth(ctx, "REFRESH_TOKEN_AUTH", params)
		if err == nil && res.AuthenticationResult != nil {
			return a.accept(c, res.AuthenticationResult, nil, false)
		}
		if err == nil {
			err = fmt.Errorf("unexpected %s challenge when refreshing the login", res.ChallengeName)
		}
		if !isCognito(err, "NotAuthorizedException") {
			return "", a.fail(fmt.Errorf("could not refresh the Hive login: %w", err))
		}
		// The refresh token has expired or been revoked. Forget it, so it
		// isn't tried again, and log in again below.
		a.mu.Lock()
		if a.tokens != nil && a.tokens.RefreshToken == refreshToken {
			t := *a.tokens
			t.RefreshToken, t.IDToken, t.AccessToken = "", "", ""
			a.tokens = &t
			if a.Store.Save(&t) == nil {
				a.fileMod = a.modTime()
			}
		}
		a.mu.Unlock()
	}
	return a.relogin(ctx)
}

// relogin logs in again without anyone at the keyboard, which only works
// without an SMS code: as the remembered device, or for an account that
// didn't ask for a code last time. Callers hold refreshMu.
func (a *Auth) relogin(ctx context.Context) (string, error) {
	a.mu.Lock()
	username, password := a.usernameLocked(), a.cfg.Password
	device := a.deviceLocked()
	noMFA := a.tokens != nil && a.tokens.NoMFA
	failed := a.reloginFailed
	a.mu.Unlock()

	switch {
	case username == "" || password == "":
		if device != nil {
			return "", a.fail(errors.New("the Hive login has expired, and logging in again needs the account password. Set HIVE_PASSWORD (and HIVE_USERNAME): the remembered device means no SMS code is needed"))
		}
		return "", ErrNotLoggedIn
	case device == nil && !noMFA:
		// A full login would text the user a code nobody is waiting for.
		return "", ErrNotLoggedIn
	case !failed.IsZero() && a.now().Sub(failed) < reloginBackoff:
		return "", a.fail(fmt.Errorf("logging in to Hive failed recently; not retrying until %s to avoid locking the account. Check the password, or run `hive-mcp login` again", failed.Add(reloginBackoff).UTC().Format(time.RFC3339)))
	}
	c, err := a.cognito(ctx)
	if err != nil {
		return "", a.fail(err)
	}
	res, _, err := a.srpLogin(ctx, c, username, password, device)
	if err == nil && res.AuthenticationResult == nil {
		err = fmt.Errorf("hive asked for %s, which needs you", describeChallenge(res.ChallengeName))
		if res.ChallengeName == "SMS_MFA" {
			err = errors.New("hive asked for an SMS code: this server is no longer a remembered device. Run `hive-mcp login` again")
		}
	}
	if err != nil {
		a.mu.Lock()
		a.reloginFailed = a.now()
		a.mu.Unlock()
		return "", a.fail(loginError(err))
	}
	return a.accept(c, res.AuthenticationResult, &Tokens{Username: username, NoMFA: device == nil}, true)
}

func (a *Auth) fail(err error) error {
	a.mu.Lock()
	a.lastError = err.Error()
	a.mu.Unlock()
	return err
}

// cognito returns a Cognito client for the configured pool, the pool the
// saved tokens belong to, or the pool named on Hive's sign-in page.
func (a *Auth) cognito(ctx context.Context) (*cognito, error) {
	pool, client := a.cfg.PoolID, a.cfg.ClientID
	if pool == "" {
		a.mu.Lock()
		if t := a.tokens; t != nil && t.PoolID != "" && t.ClientID != "" {
			pool, client = t.PoolID, t.ClientID
		} else if a.ids[0] != "" {
			pool, client = a.ids[0], a.ids[1]
		}
		a.mu.Unlock()
	}
	if pool == "" {
		var err error
		if pool, client, err = discoverCognito(ctx, a.http, a.cfg.SSOURL); err != nil {
			return nil, err
		}
		a.mu.Lock()
		a.ids = [2]string{pool, client}
		a.mu.Unlock()
	}
	u := a.cfg.CognitoURL
	if u == "" {
		u = "https://cognito-idp." + config.PoolRegion(pool) + ".amazonaws.com"
	}
	return &cognito{http: a.http, url: strings.TrimRight(u, "/"), poolID: pool, clientID: client}, nil
}

// srpLogin runs USER_SRP_AUTH, and DEVICE_SRP_AUTH when Hive recognises the
// remembered device. It returns either tokens or the next challenge (such as SMS_MFA).
func (a *Auth) srpLogin(ctx context.Context, c *cognito, username, password string, device *Device) (*authResponse, string, error) {
	s, err := newSRPSession()
	if err != nil {
		return nil, "", err
	}
	params := map[string]string{"USERNAME": username, "SRP_A": s.SRPA()}
	if device != nil {
		params["DEVICE_KEY"] = device.Key
	}
	res, err := c.initiateAuth(ctx, "USER_SRP_AUTH", params)
	if err != nil {
		return nil, "", err
	}
	if res.ChallengeName != "PASSWORD_VERIFIER" {
		return nil, "", fmt.Errorf("hive's login asked for %s, which hive-mcp doesn't support", describeChallenge(res.ChallengeName))
	}
	userID := res.ChallengeParameters["USER_ID_FOR_SRP"]
	if userID == "" {
		userID = username
	}
	claim, err := s.claim(c.poolName(), userID, password, res.ChallengeParameters, a.now())
	if err != nil {
		return nil, "", err
	}
	claim["USERNAME"] = userID
	if device != nil {
		claim["DEVICE_KEY"] = device.Key
	}
	res, err = c.respond(ctx, "PASSWORD_VERIFIER", res.Session, claim)
	if err != nil {
		return nil, "", err
	}
	if res.ChallengeName != "DEVICE_SRP_AUTH" {
		return res, userID, nil
	}
	if device == nil {
		return nil, "", errors.New("hive asked for device authentication, but this server has no remembered device")
	}
	ds, err := newSRPSession()
	if err != nil {
		return nil, "", err
	}
	res, err = c.respond(ctx, "DEVICE_SRP_AUTH", res.Session, map[string]string{"USERNAME": userID, "SRP_A": ds.SRPA(), "DEVICE_KEY": device.Key})
	if err != nil {
		return nil, "", err
	}
	if res.ChallengeName != "DEVICE_PASSWORD_VERIFIER" {
		return nil, "", fmt.Errorf("unexpected %s challenge during device authentication", describeChallenge(res.ChallengeName))
	}
	claim, err = ds.claim(device.GroupKey, device.Key, device.Password, res.ChallengeParameters, a.now())
	if err != nil {
		return nil, "", err
	}
	claim["USERNAME"] = res.ChallengeParameters["USERNAME"]
	if claim["USERNAME"] == "" {
		claim["USERNAME"] = userID
	}
	claim["DEVICE_KEY"] = device.Key
	res, err = c.respond(ctx, "DEVICE_PASSWORD_VERIFIER", res.Session, claim)
	return res, userID, err
}

func describeChallenge(name string) string {
	switch name {
	case "":
		return "nothing"
	case "SMS_MFA":
		return "an SMS code (SMS_MFA)"
	case "SOFTWARE_TOKEN_MFA":
		return "an authenticator app code (SOFTWARE_TOKEN_MFA)"
	case "NEW_PASSWORD_REQUIRED":
		return "a new password (NEW_PASSWORD_REQUIRED): log in to the Hive app or website once to set it"
	}
	return name
}

// loginError adds a hint to Cognito errors that people can act on.
func loginError(err error) error {
	switch {
	case isCognito(err, "UserNotFoundException"):
		return fmt.Errorf("hive doesn't recognise that username (HIVE_USERNAME should be the email address you log in to the Hive app with): %w", err)
	case isCognito(err, "NotAuthorizedException"):
		var ce *CognitoError
		errors.As(err, &ce)
		if strings.Contains(strings.ToLower(ce.Message), "attempts exceeded") {
			return fmt.Errorf("hive has temporarily blocked logins after too many attempts; wait a while before trying again: %w", err)
		}
		return fmt.Errorf("hive rejected the login. Check HIVE_USERNAME and HIVE_PASSWORD (log in to the Hive app with them to be sure): %w", err)
	case isCognito(err, "ResourceNotFoundException"):
		return fmt.Errorf("hive doesn't recognise this server's remembered device; run `hive-mcp login` again: %w", err)
	case isCognito(err, "PasswordResetRequiredException"):
		return fmt.Errorf("hive needs you to reset the password in the Hive app first: %w", err)
	case isCognito(err, "TooManyRequestsException", "LimitExceededException"):
		return fmt.Errorf("hive's login service is rate limiting; wait a few minutes and try again: %w", err)
	}
	return err
}

// StartLogin logs in with the username and password. With a remembered
// device, that's usually the end of it. Otherwise Hive texts a code to the
// account's phone, and CompleteLogin finishes with it.
func (a *Auth) StartLogin(ctx context.Context) (*LoginResult, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.mu.Lock()
	a.reloadLocked()
	username, password := normaliseUsername(a.cfg.Username), a.cfg.Password
	device := a.deviceLocked()
	a.mu.Unlock()
	if username == "" || password == "" {
		return nil, errors.New("set HIVE_USERNAME (the email address you use in the Hive app) and HIVE_PASSWORD before logging in")
	}
	c, err := a.cognito(ctx)
	if err != nil {
		return nil, err
	}
	res, userID, err := a.srpLogin(ctx, c, username, password, device)
	if err != nil && device != nil && deviceUnknown(err) {
		// The remembered device may have been forgotten: try once as a new one.
		device = nil
		res, userID, err = a.srpLogin(ctx, c, username, password, nil)
	}
	if err != nil {
		return nil, loginError(err)
	}
	switch {
	case res.AuthenticationResult != nil:
		if _, err := a.accept(c, res.AuthenticationResult, &Tokens{Username: username, NoMFA: device == nil}, device != nil); err != nil {
			return nil, err
		}
		out := a.loginResult(username)
		out.UsedDevice = device != nil
		return out, nil
	case res.ChallengeName == "SMS_MFA":
		a.mu.Lock()
		a.pending = &pendingLogin{cognito: c, session: res.Session, userID: userID, username: username, created: a.now()}
		a.mu.Unlock()
		return &LoginResult{CodeRequired: true, CodeSentTo: res.ChallengeParameters["CODE_DELIVERY_DESTINATION"], Username: username}, nil
	}
	return nil, fmt.Errorf("hive's login asked for %s, which hive-mcp doesn't support", describeChallenge(res.ChallengeName))
}

var codeRe = regexp.MustCompile(`^\d{4,8}$`)

// Errors from CompleteLogin that can be fixed by entering the code again.
var (
	ErrCodeFormat = errors.New("the code should be the digits from the text message Hive sent")
	ErrWrongCode  = errors.New("that code isn't right. Check the text message and try again")
)

// CompleteLogin answers the SMS code challenge from StartLogin, then registers this server as a remembered device.
func (a *Auth) CompleteLogin(ctx context.Context, code string) (*LoginResult, error) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if !codeRe.MatchString(code) {
		return nil, ErrCodeFormat
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.mu.Lock()
	p := a.pending
	a.mu.Unlock()
	if p == nil {
		return nil, errors.New("there is no login waiting for a code. Start one first (hive_auth_start or `hive-mcp login`)")
	}
	if a.now().Sub(p.created) > pendingLoginTTL {
		a.mu.Lock()
		a.pending = nil
		a.mu.Unlock()
		return nil, errors.New("the code has expired (codes work for about three minutes). Start the login again")
	}
	res, err := p.cognito.respond(ctx, "SMS_MFA", p.session, map[string]string{"SMS_MFA_CODE": code, "USERNAME": p.userID})
	if err != nil {
		if isCognito(err, "CodeMismatchException") {
			return nil, ErrWrongCode
		}
		if isCognito(err, "NotAuthorizedException", "ExpiredCodeException") {
			a.mu.Lock()
			a.pending = nil
			a.mu.Unlock()
			return nil, fmt.Errorf("hive didn't accept the code, and the login has to start again (the session may have expired): %w", err)
		}
		return nil, loginError(err)
	}
	if res.AuthenticationResult == nil {
		return nil, fmt.Errorf("hive's login asked for %s after the SMS code, which hive-mcp doesn't support", describeChallenge(res.ChallengeName))
	}
	a.mu.Lock()
	a.pending = nil
	a.mu.Unlock()
	if _, err := a.accept(p.cognito, res.AuthenticationResult, &Tokens{Username: p.username}, false); err != nil {
		return nil, err
	}
	return a.loginResult(p.username), nil
}

func (a *Auth) loginResult(username string) *LoginResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &LoginResult{
		LoggedIn: true, Username: username, DeviceRemembered: a.deviceLocked() != nil,
		TokenFile: a.cfg.TokenFile, Warning: a.persistWarning,
	}
}

// accept stores new tokens, registers a new device when Cognito offers one,
// and saves everything before handing out the ID token. login carries the
// username and flags of a fresh login; nil means a refresh. keepDevice keeps
// the remembered device when Cognito doesn't send a new one.
func (a *Auth) accept(c *cognito, r *authenticationResult, login *Tokens, keepDevice bool) (string, error) {
	if r.IdToken == "" {
		return "", a.fail(errors.New("hive's login service returned no ID token"))
	}
	a.mu.Lock()
	old := a.tokens
	a.mu.Unlock()
	if old == nil {
		old = &Tokens{}
	}
	t := &Tokens{
		Username: old.Username, PoolID: c.poolID, ClientID: c.clientID,
		IDToken: r.IdToken, AccessToken: r.AccessToken, RefreshToken: r.RefreshToken,
		ObtainedAt: a.now().UTC().Format(time.RFC3339), Device: old.Device, NoMFA: old.NoMFA,
	}
	if login != nil {
		t.Username, t.NoMFA = login.Username, login.NoMFA
		if !keepDevice {
			t.Device = nil
		}
	}
	if t.RefreshToken == "" {
		t.RefreshToken = old.RefreshToken // refreshes don't return a new one
	}
	expires := r.ExpiresIn
	if expires <= 0 {
		expires = 3600
	}
	t.ExpiresAt = a.now().Add(time.Duration(expires) * time.Second).UnixMilli()

	var warning string
	if m := r.NewDeviceMetadata; m != nil && m.DeviceKey != "" && m.DeviceGroupKey != "" && r.AccessToken != "" {
		if d, err := a.registerDevice(c, r.AccessToken, m.DeviceKey, m.DeviceGroupKey); err != nil {
			warning = "logged in, but could not register this server as a remembered device, so the next login may need an SMS code again: " + err.Error()
		} else {
			t.Device = d
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens = t
	a.lastError = ""
	a.reloginFailed = time.Time{}
	if err := a.Store.Save(t); err != nil {
		// Carry on with the in-memory tokens, but a restart would need a new login.
		a.persistWarning = fmt.Sprintf("could not save tokens to %s: %v. They will be lost on restart; mount a writable volume there", a.cfg.TokenFile, err)
		log.Printf("hive-mcp: %s", a.persistWarning)
	} else {
		a.persistWarning = ""
		a.fileMod = a.modTime()
	}
	if warning != "" {
		log.Printf("hive-mcp: %s", warning)
		if a.persistWarning == "" {
			a.persistWarning = warning
		}
	}
	return t.IDToken, nil
}

// registerDevice confirms and remembers a new device with a random password.
func (a *Auth) registerDevice(c *cognito, accessToken, key, groupKey string) (*Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	password, verifier, err := newDeviceSecret(groupKey, key)
	if err != nil {
		return nil, err
	}
	name := a.cfg.DeviceName
	if name == "" {
		name = "hive-mcp"
	}
	if err := c.confirmDevice(ctx, accessToken, key, name, verifier); err != nil {
		return nil, err
	}
	if err := c.rememberDevice(ctx, accessToken, key); err != nil {
		return nil, err
	}
	return &Device{Key: key, GroupKey: groupKey, Password: password, Name: name, RegisteredAt: a.now().UTC().Format(time.RFC3339)}, nil
}

// Logout asks Hive to forget the remembered device (best effort) and deletes the token file.
func (a *Auth) Logout(ctx context.Context) (forgotDevice bool, err error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.mu.Lock()
	a.reloadLocked()
	t := a.tokens
	a.mu.Unlock()
	if t != nil && t.Device != nil && t.AccessToken != "" && t.PoolID != "" {
		c := &cognito{http: a.http, url: a.cfg.CognitoURL, poolID: t.PoolID, clientID: t.ClientID}
		if c.url == "" {
			c.url = "https://cognito-idp." + config.PoolRegion(t.PoolID) + ".amazonaws.com"
		}
		if time.UnixMilli(t.ExpiresAt).After(a.now()) && c.forgetDevice(ctx, t.AccessToken, t.Device.Key) == nil {
			forgotDevice = true
		}
	}
	if err := a.Store.Delete(); err != nil {
		return forgotDevice, err
	}
	a.mu.Lock()
	a.tokens, a.pending, a.lastError = nil, nil, ""
	a.fileMod = time.Time{}
	a.mu.Unlock()
	return forgotDevice, nil
}

// deviceUnknown reports whether Cognito rejected the remembered device itself (rather than the password).
func deviceUnknown(err error) bool {
	var ce *CognitoError
	if !errors.As(err, &ce) {
		return false
	}
	return ce.Type == "ResourceNotFoundException" || (ce.Type == "NotAuthorizedException" && strings.Contains(strings.ToLower(ce.Message), "device"))
}
