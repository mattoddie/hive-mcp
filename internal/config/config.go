// Package config reads hive-mcp's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Toolsets are groups of tools that can be switched on and off with HIVE_TOOLSETS.
var Toolsets = []string{"home", "heating", "hotwater", "devices", "raw"}

// Defaults. Hive's sign-in page (DefaultSSOURL) names the AWS Cognito user
// pool and app client its website logs in with, and hive-mcp reads them from
// there at login, as pyhiveapi and Home Assistant do. HIVE_COGNITO_POOL_ID
// and HIVE_COGNITO_CLIENT_ID skip that lookup.
const (
	DefaultAPIURL = "https://beekeeper.hivehome.com/1.0"
	DefaultSSOURL = "https://sso.hivehome.com"
)

// Hive holds the Hive account and API settings.
type Hive struct {
	Username string
	Password string // needed for `hive-mcp login`, and to log in again when the refresh token runs out
	// APIURL is the base URL of Hive's "beekeeper" API, e.g. https://beekeeper-uk.hivehome.com/1.0.
	APIURL string
	// SSOURL is Hive's sign-in page, which names the Cognito pool and client.
	SSOURL string
	// CognitoURL overrides the AWS Cognito endpoint, which otherwise follows
	// the pool's region (https://cognito-idp.<region>.amazonaws.com).
	CognitoURL string
	PoolID     string // empty: read from SSOURL
	ClientID   string // empty: read from SSOURL
	DeviceName string // the name this server's remembered device gets in the Hive account
	TokenFile  string // tokens and the remembered device's secrets
	Timeout    time.Duration
	// CacheTTL is how long a read of every node is reused before asking Hive again.
	CacheTTL time.Duration
}

// Tools controls which tools are registered.
type Tools struct {
	AllowWrites bool
	Toolsets    map[string]bool
}

// AllTools enables every tool, for documentation and tests.
func AllTools() Tools {
	t := Tools{AllowWrites: true, Toolsets: map[string]bool{}}
	for _, ts := range Toolsets {
		t.Toolsets[ts] = true
	}
	return t
}

// HTTP holds the Streamable HTTP transport settings.
type HTTP struct {
	Host         string
	Port         int
	AuthToken    string
	AllowedHosts []string
}

// Config is the whole configuration.
type Config struct {
	Hive      Hive
	Tools     Tools
	Transport string // "stdio" or "http"
	HTTP      HTTP
}

// Env looks up a variable; os.LookupEnv in production, a map in tests.
type Env func(string) (string, bool)

// MapEnv adapts a map for tests.
func MapEnv(m map[string]string) Env {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// read returns a trimmed variable, honouring the <NAME>_FILE convention for Docker secrets.
func read(env Env, name string) (string, error) {
	if file, ok := env(name + "_FILE"); ok && file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("could not read %s_FILE (%s): %w", name, file, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	v, _ := env(name)
	return strings.TrimSpace(v), nil
}

func boolValue(name, v string, fallback bool) (bool, error) {
	if v == "" {
		return fallback, nil
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false, got %q", name, v)
}

func positiveInt(name, v string, fallback int) (int, error) {
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, v)
	}
	return n, nil
}

func nonNegativeInt(name, v string, fallback int) (int, error) {
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be zero or a positive integer, got %q", name, v)
	}
	return n, nil
}

func checkURL(name, v string) (string, error) {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not a valid URL: %s", name, v)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("%s must use https:// (or http://), got %s", name, v)
	}
	return strings.TrimRight(v, "/"), nil
}

var poolIDRe = regexp.MustCompile(`^([a-z]{2}(?:-[a-z]+)+-\d)_[A-Za-z0-9]+$`)

// PoolRegion returns the AWS region of a Cognito user pool ID ("eu-west-1"
// for "eu-west-1_AbC123"), or "" if it isn't a pool ID.
func PoolRegion(poolID string) string {
	if m := poolIDRe.FindStringSubmatch(poolID); m != nil {
		return m[1]
	}
	return ""
}

func defaultTokenFile(env Env) string {
	if dir, ok := env("XDG_CONFIG_HOME"); ok && dir != "" {
		return filepath.Join(dir, "hive-mcp", "tokens.json")
	}
	home, _ := env("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".config", "hive-mcp", "tokens.json")
}

// Load builds the configuration from the environment.
func Load(env Env) (*Config, error) {
	var firstErr error
	get := func(name string) string {
		v, err := read(env, name)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return v
	}
	or := func(v, fallback string) string {
		if v == "" {
			return fallback
		}
		return v
	}

	cfg := &Config{Transport: or(get("MCP_TRANSPORT"), "stdio")}
	if cfg.Transport != "stdio" && cfg.Transport != "http" {
		return nil, fmt.Errorf("MCP_TRANSPORT must be stdio or http, got %q", cfg.Transport)
	}

	var err error
	t := &cfg.Tools
	if t.AllowWrites, err = boolValue("HIVE_ALLOW_WRITES", get("HIVE_ALLOW_WRITES"), false); err != nil {
		return nil, err
	}
	t.Toolsets = map[string]bool{}
	for _, name := range strings.Split(or(get("HIVE_TOOLSETS"), "all"), ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		switch {
		case name == "":
		case name == "all":
			for _, ts := range Toolsets {
				t.Toolsets[ts] = true
			}
		case contains(Toolsets, name):
			t.Toolsets[name] = true
		default:
			return nil, fmt.Errorf("unknown toolset %q in HIVE_TOOLSETS; valid: all, %s", name, strings.Join(Toolsets, ", "))
		}
	}

	h := &cfg.Hive
	h.Username = get("HIVE_USERNAME")
	h.Password = get("HIVE_PASSWORD")
	if h.APIURL, err = checkURL("HIVE_API_URL", or(get("HIVE_API_URL"), DefaultAPIURL)); err != nil {
		return nil, err
	}
	if h.SSOURL, err = checkURL("HIVE_SSO_URL", or(get("HIVE_SSO_URL"), DefaultSSOURL)); err != nil {
		return nil, err
	}
	h.PoolID = get("HIVE_COGNITO_POOL_ID")
	if h.PoolID != "" && PoolRegion(h.PoolID) == "" {
		return nil, fmt.Errorf("HIVE_COGNITO_POOL_ID must look like eu-west-1_AbC123, got %q", h.PoolID)
	}
	h.ClientID = get("HIVE_COGNITO_CLIENT_ID")
	if (h.PoolID == "") != (h.ClientID == "") {
		return nil, fmt.Errorf("set both HIVE_COGNITO_POOL_ID and HIVE_COGNITO_CLIENT_ID, or neither")
	}
	if v := get("HIVE_COGNITO_URL"); v != "" {
		if h.CognitoURL, err = checkURL("HIVE_COGNITO_URL", v); err != nil {
			return nil, err
		}
	}
	h.DeviceName = or(get("HIVE_DEVICE_NAME"), "hive-mcp")
	h.TokenFile = or(get("HIVE_TOKEN_FILE"), defaultTokenFile(env))
	ms, err := positiveInt("HIVE_TIMEOUT_MS", get("HIVE_TIMEOUT_MS"), 30000)
	if err != nil {
		return nil, err
	}
	h.Timeout = time.Duration(ms) * time.Millisecond
	secs, err := nonNegativeInt("HIVE_CACHE_SECONDS", get("HIVE_CACHE_SECONDS"), 30)
	if err != nil {
		return nil, err
	}
	h.CacheTTL = time.Duration(secs) * time.Second

	hs := &cfg.HTTP
	hs.Host = or(get("MCP_HTTP_HOST"), "127.0.0.1")
	if hs.Port, err = positiveInt("MCP_HTTP_PORT", get("MCP_HTTP_PORT"), 8080); err != nil {
		return nil, err
	}
	hs.AuthToken = get("MCP_AUTH_TOKEN")
	for _, host := range strings.Split(get("MCP_ALLOWED_HOSTS"), ",") {
		if host = strings.TrimSpace(host); host != "" {
			hs.AllowedHosts = append(hs.AllowedHosts, strings.ToLower(host))
		}
	}
	unauth, err := boolValue("MCP_ALLOW_UNAUTHENTICATED", get("MCP_ALLOW_UNAUTHENTICATED"), false)
	if err != nil {
		return nil, err
	}
	// An HTTP server without a token would hand every enabled tool to anyone who can reach the port.
	if cfg.Transport == "http" && hs.AuthToken == "" && !unauth {
		return nil, errors.New("MCP_AUTH_TOKEN is required for the http transport (generate one with: openssl rand -hex 32); set MCP_ALLOW_UNAUTHENTICATED=true only behind something else that authenticates")
	}
	return cfg, firstErr
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
