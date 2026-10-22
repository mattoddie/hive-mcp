package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, file, body string) string {
	t.Helper()
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// readManifest reads the committed manifest. The Docker build only copies the
// Go sources, so tests that need it skip there; CI runs them outside Docker.
func readManifest(t *testing.T) *object {
	t.Helper()
	data, err := os.ReadFile("../../mcpb/manifest.json")
	if os.IsNotExist(err) {
		t.Skip("mcpb/manifest.json is not available")
	}
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPack(t *testing.T) {
	dir := t.TempDir()
	bin := write(t, filepath.Join(dir, "hive-mcp.exe"), "binary")
	license := write(t, filepath.Join(dir, "LICENSE"), "MIT")
	write(t, filepath.Join(dir, "icon.png"), "png")
	write(t, filepath.Join(dir, "manifest.json"), `{"manifest_version":"0.3","name":"hive-mcp","version":"0.0.0-dev","icon":"icon.png",
		"server":{"type":"binary","entry_point":"server/hive-mcp"},"compatibility":{"platforms":["darwin","win32","linux"]}}`)
	out := filepath.Join(dir, "out", "x.mcpb")
	if err := pack("1.2.3-rc.1", "windows", bin, out, dir, license); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		files[f.Name] = f
	}
	if want := []string{"manifest.json", "icon.png", "LICENSE", "server/hive-mcp.exe"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries: %v, want %v", names, want)
	}
	if mode := files["server/hive-mcp.exe"].Mode().Perm(); mode != 0o755 {
		t.Errorf("binary mode %v", mode)
	}
	r, _ := files["manifest.json"].Open()
	data, _ := io.ReadAll(r)
	m, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if v := m.get("version"); v != "1.2.3-rc.1" {
		t.Errorf("version %v", v)
	}
	if p := stringify(m.path("compatibility", "platforms")); p != "[\n  \"win32\"\n]" {
		t.Errorf("platforms %s", p)
	}
	if e := m.path("server", "entry_point"); e != "server/hive-mcp.exe" {
		t.Errorf("entry_point %v", e)
	}
	// The other keys keep their order.
	if !reflect.DeepEqual(m.keys, []string{"manifest_version", "name", "version", "icon", "server", "compatibility"}) {
		t.Errorf("keys %v", m.keys)
	}

	for _, bad := range [][2]string{{"v1.2.3", "linux"}, {"1.2", "linux"}, {"1.2.3", "plan9"}} {
		if err := pack(bad[0], bad[1], bin, out, dir, license); err == nil {
			t.Errorf("pack(%q, %q) succeeded", bad[0], bad[1])
		}
	}
}

// The manifest's settings, at their defaults, must start the server read-only:
// every variable it sets comes from a user_config entry that exists, and every
// optional entry has a default.
func TestDefaultEnv(t *testing.T) {
	m := readManifest(t)
	env, err := defaultEnv(m)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"MCP_TRANSPORT": "stdio", "HIVE_USERNAME": "", "HIVE_ALLOW_WRITES": "false", "HIVE_ALLOW_LOGIN_TOOLS": "true", "HIVE_MAX_TEMP": "25", "HIVE_TOOLSETS": "", "HIVE_AUDIT_LOG": ""} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	delete(m.path("user_config", "toolsets").(*object).vals, "default")
	if _, err := defaultEnv(m); err == nil {
		t.Error("an optional setting without a default was accepted")
	}
	m.set("user_config", newObject())
	if _, err := defaultEnv(m); err == nil {
		t.Error("a reference to a missing user_config entry was accepted")
	}
}

// Parsing and writing the committed manifest changes nothing.
func TestRoundTrip(t *testing.T) {
	data, err := os.ReadFile("../../mcpb/manifest.json")
	if os.IsNotExist(err) {
		t.Skip("mcpb/manifest.json is not available")
	}
	m := readManifest(t)
	if got := stringify(m) + "\n"; got != string(data) {
		t.Error("the manifest doesn't survive a round trip")
	}
	v, err := parseJSON([]byte(`{"b":[1.50,{"x":"<a & b>"}],"a":{},"c":[],"d":null,"e":true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"b\": [\n    1.50,\n    {\n      \"x\": \"<a & b>\"\n    }\n  ],\n  \"a\": {},\n  \"c\": [],\n  \"d\": null,\n  \"e\": true\n}"
	if got := stringify(v); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := parseJSON([]byte(`{} {}`)); err == nil {
		t.Error("trailing data accepted")
	}
}
