// Package hivetest is a fake Hive for tests: Hive's sign-in page, an AWS
// Cognito user pool that really checks SRP proofs (including SMS codes and
// remembered devices), and the beekeeper API with a small home in it.
package hivetest

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mattoddie/hive-mcp/internal/config"
)

// Test account and pool.
const (
	PoolID   = "eu-west-1_TestPool1"
	ClientID = "test-client-id"
	Username = "sam@example.com"
	Password = "correct horse battery staple"
	UserID   = "0b1c2d3e-0000-4000-8000-123456789abc" // USER_ID_FOR_SRP
	SMSCode  = "123456"
)

// Request is a recorded API request.
type Request struct {
	Method string
	// Path is relative to the API root, e.g. /nodes/heating/abc.
	Path   string
	Query  url.Values
	Body   any // decoded JSON, or nil
	Header http.Header
}

// Handler answers a matched API request with a status and a JSON body.
type Handler func(r Request, match []string) (int, any)

type route struct {
	method  string
	pattern *regexp.Regexp
	fn      Handler
}

type device struct {
	group, key string
	verifier   *big.Int
	salt       *big.Int
	remembered bool
}

type session struct {
	stage       string // PASSWORD_VERIFIER, SMS_MFA, DEVICE_SRP_AUTH, DEVICE_PASSWORD_VERIFIER
	A, b, B, v  *big.Int
	secretBlock []byte
	deviceKey   string
}

// Mock serves the sign-in page at /sso, Cognito at /cognito and the API at /api.
type Mock struct {
	Server *httptest.Server

	mu       sync.Mutex
	requests []Request
	routes   []route
	nodes    map[string]any
	holiday  map[string]any
	sessions map[string]*session
	devices  map[string]*device
	id       map[string]bool
	access   map[string]bool
	refresh  map[string]string // refresh token -> device key it was issued with
	counter  int

	// MFA makes logins without a remembered device need an SMS code (default true).
	MFA bool
	// DeviceTracking makes Cognito offer a new device with each login that didn't use one (default true).
	DeviceTracking bool
	// ExpiresIn is the lifetime of new ID and access tokens in seconds (default 3600).
	ExpiresIn int
	// SMSSent counts SMS codes "sent".
	SMSSent int
	// Cognito records each Cognito call as "Action" or "RespondToAuthChallenge:CHALLENGE".
	Cognito []string
	// SSOHits counts requests for the sign-in page.
	SSOHits int
}

// New starts a mock with the sample home. Close it with m.Server.Close().
func New() *Mock {
	m := &Mock{
		sessions: map[string]*session{}, devices: map[string]*device{},
		id: map[string]bool{}, access: map[string]bool{}, refresh: map[string]string{},
		MFA: true, DeviceTracking: true, ExpiresIn: 3600,
	}
	m.SetNodes(SampleNodes())
	m.holiday = map[string]any{"active": false, "enabled": false, "start": nil, "end": nil, "temperature": 7.0}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	return m
}

// Config returns Hive settings pointing at the mock.
func (m *Mock) Config(tokenFile string) config.Hive {
	return config.Hive{
		Username: Username, Password: Password,
		APIURL: m.Server.URL + "/api", SSOURL: m.Server.URL + "/sso", CognitoURL: m.Server.URL + "/cognito",
		DeviceName: "hive-mcp-test", TokenFile: tokenFile, Timeout: 5 * time.Second, CacheTTL: 30 * time.Second,
	}
}

// IDToken issues a valid ID token without logging in, for tests that only need the API.
func (m *Mock) IDToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter++
	t := fmt.Sprintf("id-%d", m.counter)
	m.id[t] = true
	return t
}

// IssueRefreshToken issues a refresh token as if from a login without a device.
func (m *Mock) IssueRefreshToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter++
	t := fmt.Sprintf("refresh-%d", m.counter)
	m.refresh[t] = ""
	return t
}

// ExpireTokens makes every ID and access token invalid (as if an hour had passed).
func (m *Mock) ExpireTokens() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.id, m.access = map[string]bool{}, map[string]bool{}
}

// ExpireRefreshTokens makes every refresh token invalid (as if it had run out).
func (m *Mock) ExpireRefreshTokens() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh = map[string]string{}
}

// ForgetDevices forgets every remembered device.
func (m *Mock) ForgetDevices() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices = map[string]*device{}
}

// RememberedDevices counts confirmed, remembered devices.
func (m *Mock) RememberedDevices() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.devices {
		if d.remembered && d.verifier != nil {
			n++
		}
	}
	return n
}

// SetNodes replaces what GET /nodes/all returns.
func (m *Mock) SetNodes(n map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes = clone(n)
}

// Nodes returns a copy of the current state.
func (m *Mock) Nodes() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return clone(m.nodes)
}

// Product returns a copy of a product by ID.
func (m *Mock) Product(id string) map[string]any {
	for _, p := range m.Nodes()["products"].([]any) {
		if p.(map[string]any)["id"] == id {
			return p.(map[string]any)
		}
	}
	return nil
}

func clone(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// On adds an API route; pattern is a regular expression matched against the whole path (relative to /api). Later routes win.
func (m *Mock) On(method, pattern string, fn Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes = append([]route{{method, regexp.MustCompile("^" + pattern + "$"), fn}}, m.routes...)
}

// JSON is a handler that always returns v with status 200.
func JSON(v any) Handler {
	return func(Request, []string) (int, any) { return 200, v }
}

// Requests returns the recorded API requests.
func (m *Mock) Requests() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Request(nil), m.requests...)
}

// Count counts API requests with this method and path.
func (m *Mock) Count(method, path string) int {
	n := 0
	for _, r := range m.Requests() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// Last returns the last API request with this method (any method if ""), or nil.
func (m *Mock) Last(method string) *Request {
	reqs := m.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if method == "" || reqs[i].Method == method {
			return &reqs[i]
		}
	}
	return nil
}

func (m *Mock) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/sso/":
		m.mu.Lock()
		m.SSOHits++
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><html><head><script>window.HiveSSOEnv="test",window.HiveSSOPoolId="%s",window.HiveSSOPublicCognitoClientId="%s";</script></head><body></body></html>`, PoolID, ClientID)
	case r.URL.Path == "/cognito/" && r.Method == http.MethodPost:
		m.cognito(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		m.api(w, r)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------- Cognito

// The SRP group and helpers, written out separately from the client's so
// the two check each other.
var (
	bigN, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3BE39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF6955817183995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6BF12FFA06D98A0864D87602733EC86A64521F2B18177B200CBBE117577A615D6C770988C0BAD946E208E24FA074E5AB3143DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF", 16)
	bigG    = big.NewInt(2)
	bigK    = hashInts(bigN, bigG)
)

// padBytes is the big-endian bytes of n with a leading zero byte when the top bit is set.
func padBytes(n *big.Int) []byte {
	b := n.Bytes()
	if len(b) == 0 || b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return b
}

func hashInts(ns ...*big.Int) *big.Int {
	h := sha256.New()
	for _, n := range ns {
		h.Write(padBytes(n))
	}
	return new(big.Int).SetBytes(h.Sum(nil))
}

func verifierFor(prefix, name, password string, salt *big.Int) *big.Int {
	inner := sha256.Sum256([]byte(prefix + name + ":" + password))
	h := sha256.New()
	h.Write(padBytes(salt))
	h.Write(inner[:])
	x := new(big.Int).SetBytes(h.Sum(nil))
	return new(big.Int).Exp(bigG, x, bigN)
}

func randInt(n int) *big.Int {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return new(big.Int).SetBytes(b)
}

// startSRP creates a session with the server's B for verifier v.
func (m *Mock) startSRP(stage string, A, v *big.Int, deviceKey string) (string, *session) {
	b := randInt(32)
	B := new(big.Int).Add(new(big.Int).Mul(bigK, v), new(big.Int).Exp(bigG, b, bigN))
	B.Mod(B, bigN)
	s := &session{stage: stage, A: A, b: b, B: B, v: v, secretBlock: randInt(48).Bytes(), deviceKey: deviceKey}
	m.counter++
	id := fmt.Sprintf("session-%d", m.counter)
	m.sessions[id] = s
	return id, s
}

var timestampRe = regexp.MustCompile(`^(Mon|Tue|Wed|Thu|Fri|Sat|Sun) (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) [1-9]\d? \d\d:\d\d:\d\d UTC \d{4}$`)

// checkProof verifies a PASSWORD_CLAIM_SIGNATURE the server's way: S = (A * v^u)^b.
func checkProof(s *session, prefix, name string, resp map[string]string) bool {
	if !timestampRe.MatchString(resp["TIMESTAMP"]) || resp["PASSWORD_CLAIM_SECRET_BLOCK"] != base64.StdEncoding.EncodeToString(s.secretBlock) {
		return false
	}
	u := hashInts(s.A, s.B)
	S := new(big.Int).Exp(new(big.Int).Mul(s.A, new(big.Int).Exp(s.v, u, bigN)), s.b, bigN)
	prk := hmac.New(sha256.New, padBytes(u))
	prk.Write(padBytes(S))
	kdf := hmac.New(sha256.New, prk.Sum(nil))
	kdf.Write([]byte("Caldera Derived Key\x01"))
	key := kdf.Sum(nil)[:16]
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(prefix + name))
	mac.Write(s.secretBlock)
	mac.Write([]byte(resp["TIMESTAMP"]))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(resp["PASSWORD_CLAIM_SIGNATURE"]))
}

func cognitoErr(w http.ResponseWriter, typ, msg string) {
	writeJSON(w, 400, map[string]string{"__type": typ, "message": msg})
}

func (m *Mock) tokens(deviceKey string, offerDevice bool) map[string]any {
	m.counter++
	n := m.counter
	id, access, refresh := fmt.Sprintf("id-%d", n), fmt.Sprintf("access-%d", n), fmt.Sprintf("refresh-%d", n)
	m.id[id], m.access[access], m.refresh[refresh] = true, true, deviceKey
	res := map[string]any{"IdToken": id, "AccessToken": access, "RefreshToken": refresh, "ExpiresIn": m.ExpiresIn, "TokenType": "Bearer"}
	if offerDevice && m.DeviceTracking {
		key := fmt.Sprintf("eu-west-1_dev%d", n)
		m.devices[key] = &device{key: key, group: fmt.Sprintf("-grp%d", n)}
		m.refresh[refresh] = key // the refresh token is bound to the new device
		res["NewDeviceMetadata"] = map[string]string{"DeviceKey": key, "DeviceGroupKey": m.devices[key].group}
	}
	return map[string]any{"AuthenticationResult": res, "ChallengeParameters": map[string]string{}}
}

func (m *Mock) cognito(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.")
	var in struct {
		AuthFlow                   string
		ClientId                   string
		AuthParameters             map[string]string
		ChallengeName              string
		ChallengeResponses         map[string]string
		Session                    string
		AccessToken                string
		DeviceKey                  string
		DeviceName                 string
		DeviceSecretVerifierConfig struct {
			PasswordVerifier string
			Salt             string
		}
		DeviceRememberedStatus string
	}
	if r.Header.Get("Content-Type") != "application/x-amz-json-1.1" || json.NewDecoder(r.Body).Decode(&in) != nil {
		cognitoErr(w, "SerializationException", "bad request")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := action
	if action == "RespondToAuthChallenge" {
		rec += ":" + in.ChallengeName
	}
	m.Cognito = append(m.Cognito, rec)

	switch action {
	case "InitiateAuth", "RespondToAuthChallenge":
		if in.ClientId != ClientID {
			cognitoErr(w, "ResourceNotFoundException", "User pool client does not exist.")
			return
		}
	}
	pool := strings.SplitN(PoolID, "_", 2)[1]

	switch {
	case action == "InitiateAuth" && in.AuthFlow == "USER_SRP_AUTH":
		if in.AuthParameters["USERNAME"] != Username {
			cognitoErr(w, "UserNotFoundException", "User does not exist.")
			return
		}
		A, ok := new(big.Int).SetString(in.AuthParameters["SRP_A"], 16)
		if !ok || new(big.Int).Mod(A, bigN).Sign() == 0 {
			cognitoErr(w, "InvalidParameterException", "bad SRP_A")
			return
		}
		salt := randInt(16)
		id, s := m.startSRP("PASSWORD_VERIFIER", A, verifierFor(pool, UserID, Password, salt), in.AuthParameters["DEVICE_KEY"])
		writeJSON(w, 200, map[string]any{"ChallengeName": "PASSWORD_VERIFIER", "Session": id, "ChallengeParameters": map[string]string{
			"USER_ID_FOR_SRP": UserID, "USERNAME": UserID, "SALT": salt.Text(16), "SRP_B": s.B.Text(16),
			"SECRET_BLOCK": base64.StdEncoding.EncodeToString(s.secretBlock),
		}})

	case action == "InitiateAuth" && in.AuthFlow == "REFRESH_TOKEN_AUTH":
		dk, ok := m.refresh[in.AuthParameters["REFRESH_TOKEN"]]
		if !ok || dk != in.AuthParameters["DEVICE_KEY"] {
			cognitoErr(w, "NotAuthorizedException", "Invalid Refresh Token")
			return
		}
		res := m.tokens(dk, false)
		delete(res["AuthenticationResult"].(map[string]any), "RefreshToken") // Cognito doesn't rotate them
		writeJSON(w, 200, res)

	case action == "RespondToAuthChallenge":
		s := m.sessions[in.Session]
		delete(m.sessions, in.Session)
		if s == nil || s.stage != in.ChallengeName {
			cognitoErr(w, "NotAuthorizedException", "Invalid session for the user, session is expired.")
			return
		}
		resp := in.ChallengeResponses
		switch in.ChallengeName {
		case "PASSWORD_VERIFIER":
			if resp["USERNAME"] != UserID || !checkProof(s, pool, UserID, resp) {
				cognitoErr(w, "NotAuthorizedException", "Incorrect username or password.")
				return
			}
			if d := m.devices[resp["DEVICE_KEY"]]; d != nil && d.remembered && d.verifier != nil && resp["DEVICE_KEY"] == s.deviceKey {
				id, _ := m.startSRP("DEVICE_SRP_AUTH", nil, d.verifier, d.key)
				writeJSON(w, 200, map[string]any{"ChallengeName": "DEVICE_SRP_AUTH", "Session": id, "ChallengeParameters": map[string]string{}})
				return
			}
			if resp["DEVICE_KEY"] != "" && m.devices[resp["DEVICE_KEY"]] == nil {
				cognitoErr(w, "ResourceNotFoundException", "Device does not exist.")
				return
			}
			if m.MFA {
				m.SMSSent++
				m.counter++
				id := fmt.Sprintf("session-%d", m.counter)
				m.sessions[id] = &session{stage: "SMS_MFA"}
				writeJSON(w, 200, map[string]any{"ChallengeName": "SMS_MFA", "Session": id, "ChallengeParameters": map[string]string{
					"CODE_DELIVERY_DELIVERY_MEDIUM": "SMS", "CODE_DELIVERY_DESTINATION": "+*******1234", "USER_ID_FOR_SRP": UserID,
				}})
				return
			}
			writeJSON(w, 200, m.tokens("", true))
		case "SMS_MFA":
			if resp["USERNAME"] != UserID {
				cognitoErr(w, "NotAuthorizedException", "Incorrect username.")
				return
			}
			if resp["SMS_MFA_CODE"] != SMSCode {
				// A wrong code can be retried with the same session.
				m.sessions[in.Session] = s
				cognitoErr(w, "CodeMismatchException", "Invalid code received for user")
				return
			}
			writeJSON(w, 200, m.tokens("", true))
		case "DEVICE_SRP_AUTH":
			d := m.devices[resp["DEVICE_KEY"]]
			A, ok := new(big.Int).SetString(resp["SRP_A"], 16)
			if d == nil || d.key != s.deviceKey || !ok || resp["USERNAME"] != UserID {
				cognitoErr(w, "NotAuthorizedException", "Invalid device.")
				return
			}
			id, ns := m.startSRP("DEVICE_PASSWORD_VERIFIER", A, d.verifier, d.key)
			writeJSON(w, 200, map[string]any{"ChallengeName": "DEVICE_PASSWORD_VERIFIER", "Session": id, "ChallengeParameters": map[string]string{
				"USERNAME": UserID, "SALT": d.salt.Text(16), "SRP_B": ns.B.Text(16), "SECRET_BLOCK": base64.StdEncoding.EncodeToString(ns.secretBlock),
			}})
		case "DEVICE_PASSWORD_VERIFIER":
			d := m.devices[resp["DEVICE_KEY"]]
			if d == nil || d.key != s.deviceKey || resp["USERNAME"] != UserID || !checkProof(s, d.group, d.key, resp) {
				cognitoErr(w, "NotAuthorizedException", "Incorrect device password.")
				return
			}
			writeJSON(w, 200, m.tokens(d.key, false))
		default:
			cognitoErr(w, "InvalidParameterException", "unsupported challenge")
		}

	case action == "ConfirmDevice":
		d := m.devices[in.DeviceKey]
		if !m.access[in.AccessToken] || d == nil {
			cognitoErr(w, "NotAuthorizedException", "Access Token has expired")
			return
		}
		v, err1 := base64.StdEncoding.DecodeString(in.DeviceSecretVerifierConfig.PasswordVerifier)
		salt, err2 := base64.StdEncoding.DecodeString(in.DeviceSecretVerifierConfig.Salt)
		if err1 != nil || err2 != nil || len(v) == 0 || len(salt) == 0 || in.DeviceName == "" {
			cognitoErr(w, "InvalidParameterException", "bad verifier")
			return
		}
		d.verifier, d.salt = new(big.Int).SetBytes(v), new(big.Int).SetBytes(salt)
		writeJSON(w, 200, map[string]any{"UserConfirmationNecessary": true})

	case action == "UpdateDeviceStatus":
		d := m.devices[in.DeviceKey]
		if !m.access[in.AccessToken] || d == nil || d.verifier == nil {
			cognitoErr(w, "NotAuthorizedException", "Access Token has expired")
			return
		}
		d.remembered = in.DeviceRememberedStatus == "remembered"
		writeJSON(w, 200, map[string]any{})

	case action == "ForgetDevice":
		if !m.access[in.AccessToken] {
			cognitoErr(w, "NotAuthorizedException", "Access Token has expired")
			return
		}
		delete(m.devices, in.DeviceKey)
		writeJSON(w, 200, map[string]any{})

	default:
		cognitoErr(w, "InvalidParameterException", "unsupported action "+action)
	}
}

// ---------------------------------------------------------------- API

func (m *Mock) api(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api")
	m.mu.Lock()
	authorised := m.id[r.Header.Get("Authorization")]
	m.mu.Unlock()
	if !authorised {
		writeJSON(w, 401, map[string]string{"error": "NOT_AUTHORIZED"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	req := Request{Method: r.Method, Path: path, Query: r.URL.Query(), Header: r.Header.Clone()}
	if len(bytes.TrimSpace(body)) > 0 {
		_ = json.Unmarshal(body, &req.Body)
	}
	m.mu.Lock()
	m.requests = append(m.requests, req)
	routes := append([]route(nil), m.routes...)
	m.mu.Unlock()

	for _, rt := range routes {
		if rt.method != r.Method {
			continue
		}
		if match := rt.pattern.FindStringSubmatch(path); match != nil {
			status, out := rt.fn(req, match)
			if out == nil {
				w.WriteHeader(status)
				return
			}
			writeJSON(w, status, out)
			return
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && path == "/nodes/all":
		if r.URL.Query().Get("products") != "true" || r.URL.Query().Get("devices") != "true" {
			writeJSON(w, 400, map[string]string{"error": "missing products/devices"})
			return
		}
		out := clone(m.nodes)
		out["holidayMode"] = m.holiday
		writeJSON(w, 200, out)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/nodes/"):
		parts := strings.Split(strings.TrimPrefix(path, "/nodes/"), "/")
		changes, _ := req.Body.(map[string]any)
		if len(parts) != 2 || changes == nil {
			writeJSON(w, 400, map[string]string{"error": "bad request"})
			return
		}
		for _, p := range m.nodes["products"].([]any) {
			p := p.(map[string]any)
			if p["id"] == parts[1] && p["type"] == parts[0] {
				applyChanges(p, changes)
				writeJSON(w, 200, []any{clone(p)})
				return
			}
		}
		writeJSON(w, 404, map[string]string{"error": "NOT_FOUND"})
	case path == "/holiday-mode":
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, m.holiday)
		case http.MethodPost:
			b, _ := req.Body.(map[string]any)
			m.holiday = map[string]any{"active": false, "enabled": true, "start": b["start"], "end": b["end"], "temperature": b["temperature"]}
			writeJSON(w, 200, m.holiday)
		case http.MethodDelete:
			m.holiday = map[string]any{"active": false, "enabled": false, "start": nil, "end": nil, "temperature": m.holiday["temperature"]}
			writeJSON(w, 200, map[string]any{})
		}
	default:
		writeJSON(w, 404, map[string]string{"error": "NOT_FOUND", "message": "Not found: " + path})
	}
}

// applyChanges mimics how Hive applies a state change, closely enough for tests.
func applyChanges(p, changes map[string]any) {
	state := p["state"].(map[string]any)
	props := p["props"].(map[string]any)
	if mode, ok := changes["mode"].(string); ok {
		if mode == "BOOST" && state["mode"] != "BOOST" {
			prev := map[string]any{"mode": state["mode"]}
			if t, ok := state["target"]; ok {
				prev["target"] = t
			}
			props["previous"] = prev
		}
		if mode != "BOOST" {
			state["boost"] = nil
		}
	}
	for k, v := range changes {
		state[k] = v
	}
	if p["type"] == "hotwater" {
		switch state["mode"] {
		case "MANUAL", "BOOST":
			state["status"] = "ON"
		case "OFF":
			state["status"] = "OFF"
		}
	}
}
