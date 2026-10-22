package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The committed server.json matches the committed manifest, and the check
// catches each kind of drift.
func TestCheckServerJSON(t *testing.T) {
	m := readManifest(t)
	data, err := os.ReadFile("../../server.json")
	if os.IsNotExist(err) {
		t.Skip("server.json is not available")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := checkServerJSON(data, m); err != nil {
		t.Fatalf("committed server.json: %v", err)
	}

	for name, tc := range map[string]struct {
		edit func(s map[string]any)
		want string
	}{
		"missing variable": {func(s map[string]any) {
			p := oci(s)
			env := p["environmentVariables"].([]any)
			p["environmentVariables"] = env[:len(env)-1]
		}, "HIVE_AUDIT_LOG is missing"},
		"extra variable": {func(s map[string]any) {
			p := oci(s)
			p["environmentVariables"] = append(p["environmentVariables"].([]any), map[string]any{"name": "HIVE_NEW"})
		}, "HIVE_NEW isn't in the manifest"},
		"default":            {func(s map[string]any) { envVar(s, "HIVE_MAX_TEMP")["default"] = "24" }, `HIVE_MAX_TEMP default is "24", want "25"`},
		"secret":             {func(s map[string]any) { delete(envVar(s, "HIVE_PASSWORD"), "isSecret") }, "HIVE_PASSWORD isSecret is false"},
		"required":           {func(s map[string]any) { envVar(s, "HIVE_ALLOW_WRITES")["isRequired"] = true }, "HIVE_ALLOW_WRITES isRequired is true"},
		"format":             {func(s map[string]any) { delete(envVar(s, "HIVE_ALLOW_WRITES"), "format") }, `HIVE_ALLOW_WRITES format is "string", want "boolean"`},
		"transport":          {func(s map[string]any) { envVar(s, "MCP_TRANSPORT")["value"] = "http" }, `MCP_TRANSPORT has value "http"`},
		"fixed user setting": {func(s map[string]any) { envVar(s, "HIVE_TOOLSETS")["value"] = "home" }, "HIVE_TOOLSETS has a fixed value"},
		"image":              {func(s map[string]any) { oci(s)["identifier"] = "ghcr.io/mattoddie/hive-mcp:latest" }, "the OCI package is"},
		"version":            {func(s map[string]any) { s["version"] = "1.0.0" }, `version is "1.0.0"`},
	} {
		t.Run(name, func(t *testing.T) {
			var s map[string]any
			if err := json.Unmarshal(data, &s); err != nil {
				t.Fatal(err)
			}
			tc.edit(s)
			edited, _ := json.Marshal(s)
			err := checkServerJSON(edited, m)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func oci(s map[string]any) map[string]any {
	for _, p := range s["packages"].([]any) {
		if p := p.(map[string]any); p["registryType"] == "oci" {
			return p
		}
	}
	panic("no OCI package")
}

func envVar(s map[string]any, name string) map[string]any {
	for _, e := range oci(s)["environmentVariables"].([]any) {
		if e := e.(map[string]any); e["name"] == name {
			return e
		}
	}
	panic("no " + name)
}
