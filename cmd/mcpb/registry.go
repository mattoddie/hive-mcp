package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

// registryCmd checks that server.json, the MCP Registry entry, describes the
// same settings as the bundle manifest: the OCI package's environment
// variables must match the manifest's server.mcp_config.env and the
// user_config settings behind them, so the two can't drift apart.
func registryCmd(args []string) error {
	fs := flag.NewFlagSet("registry", flag.ExitOnError)
	file := fs.String("f", "server.json", "MCP Registry server.json")
	manifestFile := fs.String("m", "mcpb/manifest.json", "bundle manifest")
	_ = fs.Parse(args)

	data, err := os.ReadFile(*manifestFile)
	if err != nil {
		return err
	}
	m, err := parseManifest(data)
	if err != nil {
		return err
	}
	data, err = os.ReadFile(*file)
	if err != nil {
		return err
	}
	if err := checkServerJSON(data, m); err != nil {
		return fmt.Errorf("%s doesn't match %s:\n%w", *file, *manifestFile, err)
	}
	fmt.Printf("%s matches %s.\n", *file, *manifestFile)
	return nil
}

type serverJSON struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	WebsiteURL string `json:"websiteUrl"`
	Packages   []struct {
		RegistryType string     `json:"registryType"`
		Identifier   string     `json:"identifier"`
		Env          []envInput `json:"environmentVariables"`
	} `json:"packages"`
}

type envInput struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	Default    string `json:"default"`
	Format     string `json:"format"`
	IsRequired bool   `json:"isRequired"`
	IsSecret   bool   `json:"isSecret"`
}

// checkServerJSON returns every mismatch between server.json and the manifest.
func checkServerJSON(data []byte, m *object) error {
	var s serverJSON
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("server.json: %w", err)
	}
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	name, _ := m.get("name").(string)
	if want := "io.github.mattoddie/" + name; s.Name != want {
		fail("name is %q, want %q", s.Name, want)
	}
	// Both carry the same placeholder; the release stamps the real version into each.
	if version, _ := m.get("version").(string); s.Version != version {
		fail("version is %q, want the manifest's %q", s.Version, version)
	}
	if home, _ := m.get("homepage").(string); s.WebsiteURL != home {
		fail("websiteUrl is %q, want the manifest's homepage %q", s.WebsiteURL, home)
	}

	var env []envInput
	oci, bundles := 0, 0
	for _, p := range s.Packages {
		switch p.RegistryType {
		case "oci":
			oci++
			env = p.Env
			if want := "ghcr.io/mattoddie/" + name + ":" + s.Version; p.Identifier != want {
				fail("the OCI package is %q, want %q", p.Identifier, want)
			}
		case "mcpb":
			bundles++
			prefix := "https://github.com/mattoddie/" + name + "/releases/download/v" + s.Version + "/" + name + "-" + s.Version + "-"
			if !strings.HasPrefix(p.Identifier, prefix) || !strings.HasSuffix(p.Identifier, ".mcpb") {
				fail("bundle %q should be %s<os>-<arch>.mcpb", p.Identifier, prefix)
			}
		}
	}
	if oci != 1 {
		fail("want one OCI package, got %d", oci)
	}
	if bundles == 0 {
		fail("no mcpb packages")
	}

	vars, _ := m.path("server", "mcp_config", "env").(*object)
	if vars == nil {
		return errors.New("manifest has no server.mcp_config.env")
	}
	byName := map[string]envInput{}
	for _, e := range env {
		byName[e.Name] = e
		if !slices.Contains(vars.keys, e.Name) {
			fail("%s isn't in the manifest's server.mcp_config.env", e.Name)
		}
	}
	for _, key := range vars.keys {
		e, ok := byName[key]
		if !ok {
			fail("%s is missing from the OCI package's environmentVariables", key)
			continue
		}
		value, _ := vars.get(key).(string)
		ref := userConfigRef.FindStringSubmatch(value)
		if ref == nil {
			// A fixed value, such as MCP_TRANSPORT=stdio.
			if e.Value != value {
				fail("%s has value %q, want the manifest's %q", key, e.Value, value)
			}
			continue
		}
		setting, _ := m.path("user_config", ref[1]).(*object)
		if setting == nil {
			fail("%s uses user_config.%s, which doesn't exist", key, ref[1])
			continue
		}
		if e.Value != "" {
			fail("%s has a fixed value, but the manifest lets the user set it (user_config.%s)", key, ref[1])
		}
		if want := setting.get("required") == true; e.IsRequired != want {
			fail("%s isRequired is %t, want %t like user_config.%s", key, e.IsRequired, want, ref[1])
		}
		if want := setting.get("sensitive") == true; e.IsSecret != want {
			fail("%s isSecret is %t, want %t like user_config.%s", key, e.IsSecret, want, ref[1])
		}
		def := ""
		if d, ok := setting.vals["default"]; ok {
			def = stringify(d)
			if s, ok := d.(string); ok {
				def = s
			}
		}
		if e.Default != def {
			fail("%s default is %q, want %q like user_config.%s", key, e.Default, def, ref[1])
		}
		format := e.Format
		if format == "" {
			format = "string"
		}
		if typ, _ := setting.get("type").(string); format != typ && !(typ == "string" && format == "filepath") {
			fail("%s format is %q, want %q like user_config.%s", key, format, typ, ref[1])
		}
	}
	return errors.Join(errs...)
}
