// Package hive is a client for Hive's (British Gas's) smart home API: the
// "beekeeper" REST API that the Hive app uses, behind an AWS Cognito login.
// Hive doesn't document or support this API; it follows what the Hive app
// and the Home Assistant integration (pyhiveapi) do.
package hive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattoddie/hive-mcp/internal/config"
)

// UserAgent is sent with every API request; main adds the version.
var UserAgent = "hive-mcp"

// Error is an API error with its HTTP status.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

const maxRetries = 3

// maxResponseBytes bounds every response body read from Hive or Cognito.
const maxResponseBytes = 64 << 20

// readBody reads a response body, failing clearly instead of truncating it
// when it is larger than maxResponseBytes.
func readBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("the response is larger than %d MiB", maxResponseBytes>>20)
	}
	return data, nil
}

// Client calls Hive's beekeeper API (https://beekeeper.hivehome.com/1.0).
// It sends the Cognito ID token, refreshing it once when the API rejects it,
// retries when rate limited, and caches the "everything" read for a short
// time so a conversation doesn't hammer an API Hive never meant for us.
type Client struct {
	cfg  config.Hive
	http *http.Client
	Auth *Auth

	mu      sync.Mutex // guards the cache
	cache   *Nodes
	fetchMu sync.Mutex // one /nodes/all request at a time
	now     func() time.Time
}

// New creates a client and loads any saved tokens.
func New(cfg config.Hive) (*Client, error) {
	// Never follow redirects: requests carry tokens (in headers and in Cognito
	// bodies), and a redirect could send them somewhere else.
	hc := &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	auth, err := NewAuth(cfg, hc)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, http: hc, Auth: auth, now: time.Now}, nil
}

// Nodes returns everything on the account: products, devices, homes,
// holiday mode and the user. A copy younger than HIVE_CACHE_SECONDS is
// reused unless fresh is set.
func (c *Client) Nodes(ctx context.Context, fresh bool) (*Nodes, error) {
	if n := c.cached(fresh); n != nil {
		return n, nil
	}
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()
	// Another caller may have fetched while this one waited.
	if n := c.cached(fresh); n != nil {
		return n, nil
	}
	var raw Obj
	if err := c.Get(ctx, "/nodes/all?products=true&devices=true&actions=true", &raw); err != nil {
		return nil, err
	}
	n := &Nodes{Raw: raw, Fetched: c.now()}
	c.mu.Lock()
	c.cache = n
	c.mu.Unlock()
	return n, nil
}

func (c *Client) cached(fresh bool) *Nodes {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fresh || c.cache == nil || c.cfg.CacheTTL <= 0 || c.now().Sub(c.cache.Fetched) >= c.cfg.CacheTTL {
		return nil
	}
	return c.cache
}

// Invalidate forgets the cached nodes, after a change.
func (c *Client) Invalidate() {
	c.mu.Lock()
	c.cache = nil
	c.mu.Unlock()
}

// SetNode changes a product's state: POST /nodes/{type}/{id}.
func (c *Client) SetNode(ctx context.Context, typ, id string, body Obj) (any, error) {
	var out any
	err := c.Post(ctx, "/nodes/"+url.PathEscape(typ)+"/"+url.PathEscape(id), body, &out)
	c.Invalidate()
	return out, err
}

// Get fetches JSON into out (which may be nil).
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

// Post sends body as JSON.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, body, out)
}

// Do makes a JSON request and decodes the response into out. Empty bodies
// leave out untouched; bodies that aren't JSON are stored as a string when out is *any.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	refreshed := false
	for attempt := 0; ; attempt++ {
		token, err := c.Auth.IDToken(ctx)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.cfg.APIURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		// Hive's API takes the bare Cognito ID token, without "Bearer".
		req.Header.Set("Authorization", token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", UserAgent)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("%s %s: could not reach the Hive API: %w", method, path, err)
		}
		data, err := readBody(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("%s %s: could not read Hive's answer: %w", method, path, err)
		}
		if resp.StatusCode/100 == 2 {
			if out == nil || len(bytes.TrimSpace(data)) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				if p, ok := out.(*any); ok {
					*p = strings.TrimSpace(string(data))
					return nil
				}
				return fmt.Errorf("%s %s: unexpected response: %w", method, path, err)
			}
			return nil
		}
		// Only 401 means the token was refused; a 403 is about the request, and
		// refreshing (or logging in again) for it would only add login traffic.
		if resp.StatusCode == http.StatusUnauthorized && !refreshed {
			refreshed = true
			if _, err := c.Auth.Refresh(ctx, token); err != nil {
				return err
			}
			continue
		}
		if retryable(method, resp.StatusCode) && attempt < maxRetries {
			delay := time.Duration(500<<attempt) * time.Millisecond
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				delay = min(time.Duration(s)*time.Second, 10*time.Second)
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		return &Error{
			Status:  resp.StatusCode,
			Message: fmt.Sprintf("%s %s failed (HTTP %d): %s.%s", method, path, resp.StatusCode, errorMessage(resp.StatusCode, data), hint(resp.StatusCode)),
		}
	}
}

// retryable reports whether a failed request may be sent again. A 429 means
// Hive didn't act on it, so any method can be retried; after a 502 or 503 a
// POST may already have changed something, so only other methods are.
func retryable(method string, status int) bool {
	switch status {
	case http.StatusTooManyRequests:
		return true
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return method != http.MethodPost
	}
	return false
}

// errorMessage pulls a readable message out of an error body.
func errorMessage(status int, data []byte) string {
	var j Obj
	if json.Unmarshal(data, &j) != nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return truncate(s, 300)
		}
		return http.StatusText(status)
	}
	var parts []string
	for _, k := range []string{"message", "error", "description", "detail", "title"} {
		if s, ok := j[k].(string); ok && s != "" && !contains(parts, s) {
			parts = append(parts, s)
		}
	}
	for _, e := range Objs(j["errors"]) {
		if s := Str(e, "message"); s != "" {
			parts = append(parts, s)
		} else if s := Str(e, "title"); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return truncate(string(data), 300)
	}
	return strings.Join(parts, "; ")
}

func hint(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return " Hive rejected the login even after refreshing it. Check `hive-mcp status`, and run `hive-mcp login` again if needed."
	case http.StatusForbidden:
		return " Hive refused this request for this account; the device or feature may not belong to it."
	case http.StatusNotFound:
		return " The device may have been removed, or the ID is wrong; list devices again."
	case http.StatusTooManyRequests:
		return " Hive is rate limiting this account; wait a minute before trying again."
	}
	if status >= 500 {
		return " Hive's API had a problem; try again shortly. If it keeps failing, the API may have changed."
	}
	return ""
}

// IsNotLoggedIn reports whether err means the server needs a login.
func IsNotLoggedIn(err error) bool { return errors.Is(err, ErrNotLoggedIn) }
