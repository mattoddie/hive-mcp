// Package httpserver serves MCP over Streamable HTTP, with bearer auth, a
// Host/Origin allow-list and a health endpoint.
package httpserver

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
)

const maxBodyBytes = 1 << 20 // tool arguments are small

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func rpcError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32000, "message": message}, "id": nil})
}

func tokenMatches(header, token string) bool {
	presented, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		presented, ok = strings.CutPrefix(header, "bearer ")
	}
	return ok && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presented)), []byte(token)) == 1
}

// bracket puts an IPv6 address in brackets, as it's written in a Host header
// or an allow-list entry.
func bracket(hostname string) string {
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}
	return hostname
}

// hostAllowed reports whether a Host header is on the allow-list. An entry
// without a port matches the host on any port (the server's port varies
// behind Docker); an entry with a port matches only that port.
func hostAllowed(allowed []string, host string) bool {
	host = strings.ToLower(host)
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = bracket(h)
	}
	return slices.Contains(allowed, host) || slices.Contains(allowed, hostname)
}

// originAllowed reports whether an Origin header is on the allow-list. An
// entry without a port matches only the scheme's default port (80 or 443);
// a page on another port needs an explicit host:port entry. "null", empty
// and malformed origins are refused.
func originAllowed(allowed []string, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" || u.User != nil || u.Path != "" || u.RawQuery != "" {
		return false
	}
	hostname := bracket(strings.ToLower(u.Hostname()))
	defaultPort := map[string]string{"http": "80", "https": "443"}[u.Scheme]
	if port := u.Port(); port != "" && port != defaultPort {
		return slices.Contains(allowed, hostname+":"+port)
	}
	return slices.Contains(allowed, hostname) || slices.Contains(allowed, hostname+":"+defaultPort)
}

// Handler serves /mcp (stateless Streamable HTTP, so the container can be restarted or scaled freely) and /health.
func Handler(cfg config.HTTP, server *mcp.Server) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: len(cfg.AllowedHosts) > 0, // MCP_ALLOWED_HOSTS replaces the built-in localhost check
		MaxRequestBodyBytes:        maxBodyBytes,
	})
	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if len(cfg.AllowedHosts) > 0 {
			if !hostAllowed(cfg.AllowedHosts, r.Host) {
				rpcError(w, http.StatusForbidden, "Host "+strings.ToLower(r.Host)+" is not allowed")
				return
			}
			// Browsers send Origin on cross-site requests; refuse pages served
			// from anywhere else (DNS rebinding, cross-site requests).
			if origins, sent := r.Header["Origin"]; sent {
				if len(origins) != 1 || !originAllowed(cfg.AllowedHosts, origins[0]) {
					rpcError(w, http.StatusForbidden, "Origin "+strings.Join(origins, ", ")+" is not allowed")
					return
				}
			}
		}
		if cfg.AuthToken != "" && !tokenMatches(r.Header.Get("Authorization"), cfg.AuthToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			rpcError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		if r.Method != http.MethodPost {
			// Stateless mode: no server-initiated SSE stream and no sessions to delete.
			w.Header().Set("Allow", "POST")
			rpcError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		if err != nil {
			rpcError(w, http.StatusBadRequest, "Could not read the request body")
			return
		}
		if len(body) > maxBodyBytes {
			rpcError(w, http.StatusRequestEntityTooLarge, "Request body too large")
			return
		}
		if !json.Valid(body) {
			rpcError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		mcpHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found. The MCP endpoint is /mcp"})
	})
	return mux
}

// Addr is the listen address.
func Addr(cfg config.HTTP) string {
	return net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

// HealthURL is the /health URL for the healthcheck command: the configured
// address, or loopback when the server listens on every address.
func HealthURL(cfg config.HTTP) string {
	host := cfg.Host
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(cfg.Port)) + "/health"
}

// Serve listens until the server is shut down.
func Serve(srv *http.Server, cfg config.HTTP) error {
	auth := ""
	if cfg.AuthToken != "" {
		auth = " (bearer auth enabled)"
	}
	log.Printf("hive-mcp listening on http://%s/mcp%s", srv.Addr, auth)
	if cfg.AuthToken == "" {
		log.Printf("WARNING: MCP_ALLOW_UNAUTHENTICATED=true: anyone who can reach this port can use every enabled tool. Only run it like this behind something else that authenticates every request.")
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
