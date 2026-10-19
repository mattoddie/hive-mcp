// Command hive-mcp is a Model Context Protocol server for Hive smart heating.
//
//	hive-mcp              serve MCP (stdio, or Streamable HTTP with MCP_TRANSPORT=http)
//	hive-mcp login        log in to Hive once with an SMS code and remember this server
//	hive-mcp logout       forget this server's remembered device and delete the tokens
//	hive-mcp status       show the login status
//	hive-mcp healthcheck  check the HTTP server is up (for Docker HEALTHCHECK)
//	hive-mcp version      print the version
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
	"github.com/mattoddie/hive-mcp/internal/httpserver"
	"github.com/mattoddie/hive-mcp/internal/tools"
)

func main() {
	// Logs go to stderr: stdout is the MCP channel in stdio mode.
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
	hive.UserAgent = "hive-mcp/" + tools.Version + " (+https://github.com/mattoddie/hive-mcp)"
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "login":
		err = login()
	case "logout":
		err = logout()
	case "status":
		err = status()
	case "healthcheck":
		err = healthcheck()
	case "version", "--version", "-v":
		fmt.Println(tools.Version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if err != nil {
		log.Printf("hive-mcp: %v", err)
		os.Exit(1)
	}
}

const usage = `Usage: hive-mcp [command]

Commands:
  serve        Serve MCP (default). stdio, or Streamable HTTP with MCP_TRANSPORT=http
  login        Log in to Hive with HIVE_USERNAME, HIVE_PASSWORD and an SMS code, and remember this server
  logout       Forget this server's remembered device and delete HIVE_TOKEN_FILE
  status       Show whether the server is logged in
  healthcheck  Exit 0 if the HTTP server answers /health (for Docker HEALTHCHECK)
  version      Print the version

Configuration is by environment variable; see https://hive-mcp.mattoddie.dev/docs/configuration/
`

func load() (*config.Config, *hive.Client, error) {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return nil, nil, err
	}
	client, err := hive.New(cfg.Hive)
	if err != nil {
		return nil, nil, err
	}
	return cfg, client, nil
}

func serve() error {
	cfg, client, err := load()
	if err != nil {
		return err
	}
	writes := "disabled"
	if cfg.Tools.AllowWrites {
		writes = "ENABLED"
	}
	login := "not logged in (run `hive-mcp login`)"
	if client.Auth.HasCredentials() {
		login = "token file " + cfg.Hive.TokenFile
		if st := client.Auth.Status(); st["device_remembered"] == true {
			login += " (remembered device)"
		}
	}
	log.Printf("hive-mcp %s: %s, auth %s, writes %s, toolsets %d, transport %s", tools.Version, cfg.Hive.APIURL, login, writes, len(cfg.Tools.Toolsets), cfg.Transport)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := tools.NewServer(client, cfg.Tools, nil)
	if cfg.Transport == "stdio" {
		err := server.Run(ctx, &mcp.StdioTransport{})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	// No WriteTimeout: a tool call can legitimately take minutes.
	srv := &http.Server{
		Addr: httpserver.Addr(cfg.HTTP), Handler: httpserver.Handler(cfg.HTTP, server),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	return httpserver.Serve(srv, cfg.HTTP)
}

// login runs the first login from a terminal: password, then the SMS code
// Hive texts to the account's phone. Afterwards this server is a remembered
// device, so the server can log in again by itself without a code.
func login() error {
	cfg, client, err := load()
	if err != nil {
		return err
	}
	if cfg.Hive.Username == "" || cfg.Hive.Password == "" {
		return errors.New("set HIVE_USERNAME (the email address you use in the Hive app) and HIVE_PASSWORD first, for example in .env. The password is also needed later, so the server can log in again without an SMS code")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	fmt.Fprintf(os.Stderr, "Logging in to Hive as %s ...\n", strings.ToLower(strings.TrimSpace(cfg.Hive.Username)))
	res, err := client.Auth.StartLogin(ctx)
	if err != nil {
		return err
	}
	if res.CodeRequired {
		in := bufio.NewScanner(os.Stdin)
		dest := res.CodeSentTo
		if dest == "" {
			dest = "the phone number on your Hive account"
		}
		fmt.Fprintf(os.Stderr, "\nHive has sent a code by text message to %s.\n", dest)
		for attempt := 1; ; attempt++ {
			fmt.Fprint(os.Stderr, "Enter the code: ")
			if !in.Scan() {
				return errors.New("no code entered. In Docker, run the login with a terminal: `docker compose run --rm hive-mcp login` or `docker run -it ...`")
			}
			res, err = client.Auth.CompleteLogin(ctx, in.Text())
			if err == nil {
				break
			}
			if attempt == 3 || !(errors.Is(err, hive.ErrWrongCode) || errors.Is(err, hive.ErrCodeFormat)) {
				return err
			}
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}

	fmt.Fprintln(os.Stderr)
	if n, err := client.Nodes(ctx, true); err == nil {
		name := strings.TrimSpace(hive.Str(n.Raw, "user", "firstName") + " " + hive.Str(n.Raw, "user", "lastName"))
		fmt.Fprintf(os.Stderr, "Logged in to Hive as %s (%s): %d heating zone(s), %d device(s).\n", or(name, res.Username), res.Username,
			len(n.Products(hive.HeatingTypes...)), len(n.Devices()))
	} else {
		fmt.Fprintf(os.Stderr, "Logged in to Hive as %s, but reading the home failed: %v\n", res.Username, err)
	}
	fmt.Fprintf(os.Stderr, "Tokens saved to %s.\n", res.TokenFile)
	switch {
	case res.UsedDevice:
		fmt.Fprintln(os.Stderr, "This server was already a remembered device, so no SMS code was needed.")
	case res.DeviceRemembered:
		fmt.Fprintf(os.Stderr, "This server is now a remembered device (%q), so it can log in again without an SMS code.\n", cfg.Hive.DeviceName)
	default:
		fmt.Fprintln(os.Stderr, "Hive didn't offer to remember this server, so when the login runs out it may need `hive-mcp login` again.")
	}
	if res.Warning != "" {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", res.Warning)
	}
	return nil
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func logout() error {
	cfg, client, err := load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := client.Auth.Logout(ctx)
	if err != nil {
		return err
	}
	if res.ForgotDevice {
		fmt.Fprintln(os.Stderr, "Hive has forgotten this server's remembered device.")
	} else {
		fmt.Fprintln(os.Stderr, "Couldn't ask Hive to forget the remembered device (the login had run out, or there wasn't one). You can remove it in the Hive app or website.")
	}
	if res.RevokedTokens {
		fmt.Fprintln(os.Stderr, "Hive has revoked this server's tokens.")
	}
	fmt.Fprintf(os.Stderr, "Deleted %s.\n", cfg.Hive.TokenFile)
	return nil
}

func status() error {
	_, client, err := load()
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(client.Auth.Status(), "", "  ")
	fmt.Println(string(b))
	return nil
}

// healthcheck asks the running server's /health, at the address and port it
// was configured with (including _FILE variables).
func healthcheck() error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	if cfg.Transport == "stdio" {
		return nil
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(httpserver.HealthURL(cfg.HTTP))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %s", resp.Status)
	}
	return nil
}
