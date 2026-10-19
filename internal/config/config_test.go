package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load(MapEnv(map[string]string{"HOME": "/home/x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != "stdio" || cfg.HTTP.Port != 8080 || cfg.HTTP.Host != "127.0.0.1" {
		t.Errorf("transport defaults: %+v %+v", cfg.Transport, cfg.HTTP)
	}
	h := cfg.Hive
	if h.APIURL != DefaultAPIURL || h.SSOURL != DefaultSSOURL || h.CognitoURL != "" {
		t.Errorf("URLs: %s %s %s", h.APIURL, h.SSOURL, h.CognitoURL)
	}
	if h.PoolID != "" || h.ClientID != "" || h.DeviceName != "hive-mcp" {
		t.Errorf("cognito: %+v", h)
	}
	if h.Timeout != 30*time.Second || h.CacheTTL != 30*time.Second {
		t.Errorf("timeout/cache: %s %s", h.Timeout, h.CacheTTL)
	}
	if h.TokenFile != filepath.Join("/home/x", ".config", "hive-mcp", "tokens.json") {
		t.Errorf("token file: %s", h.TokenFile)
	}
	if cfg.Tools.MaxTemp != 25 {
		t.Errorf("max temp: %g", cfg.Tools.MaxTemp)
	}
	if cfg.Tools.AllowWrites || cfg.Tools.AllowLoginTools || len(cfg.Tools.Toolsets) != len(Toolsets) {
		t.Errorf("tools: %+v", cfg.Tools)
	}
}

func TestCognitoOverrides(t *testing.T) {
	cfg, err := Load(MapEnv(map[string]string{"HIVE_COGNITO_POOL_ID": "us-east-2_Abc123", "HIVE_COGNITO_CLIENT_ID": "abc", "HIVE_CACHE_SECONDS": "0"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hive.PoolID != "us-east-2_Abc123" || PoolRegion(cfg.Hive.PoolID) != "us-east-2" || PoolRegion("nope") != "" {
		t.Errorf("pool: %s", cfg.Hive.PoolID)
	}
	if cfg.Hive.CacheTTL != 0 {
		t.Errorf("cache can be turned off: %s", cfg.Hive.CacheTTL)
	}
}

func TestValidation(t *testing.T) {
	for env, want := range map[string]string{
		"HIVE_ALLOW_WRITES=maybe":       "must be true or false",
		"HIVE_ALLOW_LOGIN_TOOLS=maybe":  "must be true or false",
		"HIVE_MAX_TEMP=40":              "HIVE_MAX_TEMP must be",
		"HIVE_MAX_TEMP=20.3":            "steps of 0.5",
		"HIVE_MAX_TEMP=warm":            "HIVE_MAX_TEMP must be",
		"MCP_TRANSPORT=sse":             "stdio or http",
		"HIVE_TOOLSETS=heating,nope":    `unknown toolset "nope"`,
		"HIVE_API_URL=ftp://x":          "https://",
		"HIVE_TIMEOUT_MS=-1":            "positive integer",
		"HIVE_CACHE_SECONDS=soon":       "zero or a positive integer",
		"MCP_HTTP_PORT=http":            "positive integer",
		"HIVE_COGNITO_POOL_ID=nope":     "eu-west-1_",
		"HIVE_COGNITO_CLIENT_ID=abc":    "or neither",
		"HIVE_SSO_URL=ftp://x":          "https://",
		"HIVE_PASSWORD_FILE=/nonexist":  "HIVE_PASSWORD_FILE",
		"HIVE_COGNITO_URL=not a url":    "not a valid URL",
		"MCP_AUTH_TOKEN_FILE=/nonexist": "MCP_AUTH_TOKEN_FILE",
	} {
		k, v, _ := strings.Cut(env, "=")
		_, err := Load(MapEnv(map[string]string{k: v}))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", env, err, want)
		}
	}
}

func TestToolsetsAndSecretsFromFiles(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(MapEnv(map[string]string{
		"HIVE_TOOLSETS":      " Heating, hotwater ",
		"HIVE_USERNAME":      "sam@example.com",
		"HIVE_PASSWORD_FILE": secret,
		"HIVE_PASSWORD":      "ignored",
		"MCP_ALLOWED_HOSTS":  "MCP.example.com, localhost:8080",
		"XDG_CONFIG_HOME":    "/xdg",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools.Toolsets) != 2 || !cfg.Tools.Toolsets["heating"] || !cfg.Tools.Toolsets["hotwater"] {
		t.Errorf("toolsets: %v", cfg.Tools.Toolsets)
	}
	if cfg.Hive.Password != "s3cret" || cfg.Hive.Username != "sam@example.com" {
		t.Errorf("password from file: %q", cfg.Hive.Password)
	}
	if strings.Join(cfg.HTTP.AllowedHosts, ",") != "mcp.example.com,localhost:8080" {
		t.Errorf("allowed hosts: %v", cfg.HTTP.AllowedHosts)
	}
	if cfg.Hive.TokenFile != "/xdg/hive-mcp/tokens.json" {
		t.Errorf("token file: %s", cfg.Hive.TokenFile)
	}
}

func TestHTTPNeedsAuthToken(t *testing.T) {
	base := map[string]string{"HOME": "/home/x", "MCP_TRANSPORT": "http"}
	if _, err := Load(MapEnv(base)); err == nil || !strings.Contains(err.Error(), "MCP_AUTH_TOKEN is required") {
		t.Fatalf("http without a token: got %v", err)
	}
	base["MCP_ALLOW_UNAUTHENTICATED"] = "true"
	if _, err := Load(MapEnv(base)); err == nil || !strings.Contains(err.Error(), "needs MCP_ALLOWED_HOSTS") {
		t.Fatalf("unauthenticated without an allow-list: %v", err)
	}
	base["MCP_ALLOWED_HOSTS"] = "hive-mcp.lan"
	if cfg, err := Load(MapEnv(base)); err != nil || !cfg.HTTP.AllowUnauthenticated {
		t.Fatalf("explicit opt-out: %v", err)
	}
	delete(base, "MCP_ALLOW_UNAUTHENTICATED")
	delete(base, "MCP_ALLOWED_HOSTS")
	base["MCP_AUTH_TOKEN"] = "short-token"
	if _, err := Load(MapEnv(base)); err == nil || !strings.Contains(err.Error(), "at least 16") {
		t.Fatalf("short token: %v", err)
	}
	base["MCP_AUTH_TOKEN"] = "a-long-enough-secret-token"
	if _, err := Load(MapEnv(base)); err != nil {
		t.Fatalf("with a token: %v", err)
	}
}
