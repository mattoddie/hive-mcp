// Command mcpb maintains the MCP Bundle manifest (mcpb/manifest.json) and packs
// the bundles that let Claude Desktop install hive-mcp in one click.
//
//	go run ./cmd/mcpb manifest [-check]
//	    Regenerate the manifest's tools and prompts from the server, started the
//	    way the manifest's default settings start it. With -check, fail instead
//	    if the committed manifest is out of date.
//
//	go run ./cmd/mcpb registry
//	    Check that server.json (the MCP Registry entry) declares the same
//	    environment variables, defaults and flags as the manifest.
//
//	go run ./cmd/mcpb pack -version 1.2.3 -goos darwin -binary path/to/hive-mcp -o out.mcpb
//	    Write a bundle for one platform: the manifest stamped with the version
//	    and platform, the icon, the licence and the binary.
//
// `make mcpb` builds the binaries and calls pack for every platform.
package main

import (
	"archive/zip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/tools"
)

const usage = `Usage:
  mcpb manifest [-check] [-f mcpb/manifest.json]
  mcpb registry [-f server.json] [-m mcpb/manifest.json]
  mcpb pack -version X.Y.Z -goos darwin|windows|linux -binary FILE -o OUT.mcpb [-dir mcpb] [-license LICENSE]
`

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		log.Fatal(usage)
	}
	var err error
	switch os.Args[1] {
	case "manifest":
		err = manifestCmd(os.Args[2:])
	case "registry":
		err = registryCmd(os.Args[2:])
	case "pack":
		err = packCmd(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", os.Args[1], usage)
	}
	if err != nil {
		log.Fatalf("mcpb: %v", err)
	}
}

func manifestCmd(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	check := fs.Bool("check", false, "fail if the manifest is out of date instead of writing it")
	file := fs.String("f", "mcpb/manifest.json", "manifest file")
	_ = fs.Parse(args)

	current, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	m, err := parseManifest(current)
	if err != nil {
		return err
	}
	if err := generate(m); err != nil {
		return err
	}
	text := stringify(m) + "\n"
	if *check {
		if string(current) != text {
			return fmt.Errorf("%s is out of date. Run `make docs` and commit the result", *file)
		}
		fmt.Printf("%s is up to date.\n", *file)
		return nil
	}
	if err := os.WriteFile(*file, []byte(text), 0o644); err != nil {
		return err
	}
	toolList, _ := m.get("tools").([]any)
	promptList, _ := m.get("prompts").([]any)
	fmt.Printf("Wrote %s (%d tools, %d prompts).\n", *file, len(toolList), len(promptList))
	return nil
}

func parseManifest(data []byte) (*object, error) {
	v, err := parseJSON(data)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	m, ok := v.(*object)
	if !ok {
		return nil, errors.New("manifest: not a JSON object")
	}
	return m, nil
}

var userConfigRef = regexp.MustCompile(`\$\{user_config\.([^}]+)\}`)

// defaultEnv is the environment the bundle host sets when the user fills in
// only the required settings and keeps every default: each
// ${user_config.key} replaced by that setting's default, or "" for a
// required setting without one.
func defaultEnv(m *object) (map[string]string, error) {
	env := map[string]string{}
	vars, _ := m.path("server", "mcp_config", "env").(*object)
	if vars == nil {
		return nil, errors.New("manifest has no server.mcp_config.env")
	}
	var missing error
	for _, name := range vars.keys {
		s, _ := vars.get(name).(string)
		env[name] = userConfigRef.ReplaceAllStringFunc(s, func(ref string) string {
			key := userConfigRef.FindStringSubmatch(ref)[1]
			setting, _ := m.path("user_config", key).(*object)
			if setting == nil {
				missing = fmt.Errorf("user_config.%s (used by %s) doesn't exist", key, name)
				return ""
			}
			def, ok := setting.vals["default"]
			if !ok {
				if setting.get("required") != true {
					missing = fmt.Errorf("user_config.%s (used by %s) is optional but has no default, so Claude Desktop would pass ${user_config.%s} through", key, name, key)
				}
				return ""
			}
			switch d := def.(type) {
			case string:
				return d
			default:
				return stringify(d)
			}
		})
	}
	return env, missing
}

// generate fills in the manifest's tools and prompts from the server, started
// with the manifest's default settings. Listing tools and prompts never
// touches Hive or the token file, so the server gets no client.
func generate(m *object) error {
	env, err := defaultEnv(m)
	if err != nil {
		return err
	}
	cfg, err := config.Load(config.MapEnv(env))
	if err != nil {
		return fmt.Errorf("the manifest's default settings don't start the server: %w", err)
	}
	server := tools.NewServer(nil, cfg.Tools, nil)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		return err
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "mcpb", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return err
	}
	defer session.Close()

	toolList := []any{}
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			return err
		}
		toolList = append(toolList, newObject("name", t.Name, "description", t.Description))
	}
	listed := map[string]*mcp.Prompt{}
	for p, err := range session.Prompts(ctx, nil) {
		if err != nil {
			return err
		}
		listed[p.Name] = p
	}
	promptList := []any{}
	for _, name := range tools.PromptNames() {
		p := listed[name]
		if p == nil {
			continue
		}
		// The manifest's text shows the prompt with its required arguments as
		// ${arguments.name} placeholders; optional ones take their defaults.
		names := []string{}
		args := map[string]string{}
		for _, a := range p.Arguments {
			names = append(names, a.Name)
			if a.Required {
				args[a.Name] = "${arguments." + a.Name + "}"
			}
		}
		res, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
		if err != nil {
			return fmt.Errorf("prompt %s: %w", name, err)
		}
		text := ""
		for _, msg := range res.Messages {
			if tc, ok := msg.Content.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		promptList = append(promptList, newObject("name", name, "description", p.Description, "arguments", names, "text", text))
	}
	m.set("tools", toolList)
	// Allowing changes or the login tools, or choosing toolsets, changes the list at runtime.
	m.set("tools_generated", true)
	m.set("prompts", promptList)
	return nil
}

// platforms maps GOOS to the manifest's platform names (Node's process.platform).
var platforms = map[string]string{"darwin": "darwin", "windows": "win32", "linux": "linux"}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

func packCmd(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	version := fs.String("version", "", "version to stamp into the manifest (X.Y.Z, without the v)")
	goos := fs.String("goos", "", "the binary's GOOS: darwin, windows or linux")
	binary := fs.String("binary", "", "the hive-mcp binary built for that platform")
	out := fs.String("o", "", "the .mcpb file to write")
	dir := fs.String("dir", "mcpb", "directory with manifest.json and icon.png")
	license := fs.String("license", "LICENSE", "licence file to include")
	_ = fs.Parse(args)
	if *version == "" || *goos == "" || *binary == "" || *out == "" {
		return errors.New("pack needs -version, -goos, -binary and -o\n\n" + usage)
	}
	return pack(*version, *goos, *binary, *out, *dir, *license)
}

func pack(version, goos, binary, out, dir, license string) error {
	if !semver.MatchString(version) {
		return fmt.Errorf("version %q is not a semantic version like 1.2.3", version)
	}
	platform, ok := platforms[goos]
	if !ok {
		return fmt.Errorf("unsupported GOOS %q (want darwin, windows or linux)", goos)
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	m, err := parseManifest(data)
	if err != nil {
		return err
	}
	m.set("version", version)
	// One bundle per OS and architecture: the manifest can only choose a
	// command by OS, so each bundle carries one binary and says which OS it is for.
	compat, _ := m.get("compatibility").(*object)
	if compat == nil {
		compat = newObject()
		m.set("compatibility", compat)
	}
	compat.set("platforms", []any{platform})
	entry := "server/hive-mcp"
	if goos == "windows" {
		entry += ".exe"
	}
	server, _ := m.get("server").(*object)
	if server == nil {
		return errors.New("manifest has no server")
	}
	server.set("entry_point", entry)
	icon, _ := m.get("icon").(string)

	files := []struct {
		name, src string
		body      []byte
		mode      os.FileMode
	}{
		{name: "manifest.json", body: []byte(stringify(m) + "\n"), mode: 0o644},
		{name: "icon.png", src: filepath.Join(dir, icon), mode: 0o644},
		{name: "LICENSE", src: license, mode: 0o644},
		{name: entry, src: binary, mode: 0o755},
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	for _, file := range files {
		// A fixed timestamp keeps bundles reproducible.
		h := &zip.FileHeader{Name: path.Clean(file.name), Method: zip.Deflate, Modified: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
		h.SetMode(file.mode) // Unix permissions, so the binary stays executable when unpacked
		w, err := zw.CreateHeader(h)
		if err == nil {
			err = copyFile(w, file.src, file.body)
		}
		if err != nil {
			f.Close()
			return fmt.Errorf("%s: %w", file.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", out)
	return nil
}

func copyFile(w io.Writer, src string, body []byte) error {
	if src == "" {
		_, err := w.Write(body)
		return err
	}
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = io.Copy(w, r)
	return err
}
