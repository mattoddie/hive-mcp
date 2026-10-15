package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type rawRequestArgs struct {
	Method string         `json:"method,omitempty" jsonschema:"HTTP method: GET (default), POST, PUT or DELETE"`
	Path   string         `json:"path" jsonschema:"Path relative to https://beekeeper.hivehome.com/1.0, for example /nodes/all or /holiday-mode"`
	Query  map[string]any `json:"query,omitempty" jsonschema:"Query string parameters"`
	Body   any            `json:"body,omitempty" jsonschema:"JSON request body for POST, PUT and DELETE"`
}

func registerRaw(c *Context) {
	cl := c.Client
	desc := "Call any path of Hive's API (the undocumented \"beekeeper\" API the Hive app uses) directly, for anything the other tools don't cover, such as /nodes/all, /devices, /products or /actions. "
	if c.AllowWrites {
		desc += "All methods are allowed. Changes go straight to Hive with no checks, so confirm them with the user first."
	} else {
		desc += "Only GET is allowed because writes are disabled (HIVE_ALLOW_WRITES=false)."
	}
	add(c, Def{
		Toolset: "raw", Name: "hive_api_request", Title: "Raw Hive API request",
		Description: desc,
		ReadOnly:    boolPtr(!c.AllowWrites), Destructive: c.AllowWrites,
		Enums: map[string][]string{"method": {"GET", "POST", "PUT", "DELETE"}},
	}, func(ctx context.Context, a rawRequestArgs) (any, error) {
		method := strings.ToUpper(a.Method)
		if method == "" {
			method = "GET"
		}
		p, err := url.PathUnescape(a.Path)
		if err != nil {
			return nil, errors.New("path is not validly percent-encoded")
		}
		p = strings.TrimPrefix(p, "/1.0")
		if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.ContainsAny(p, "?#\\") || strings.Contains(p, "//") || strings.Contains(p, "/./") {
			return nil, errors.New("path must start with /, must not contain '..', './' or '//' segments, and must not include a query string (use query)")
		}
		switch {
		case method == "GET":
		case !c.AllowWrites:
			return nil, errors.New("writes are disabled. Set HIVE_ALLOW_WRITES=true to allow requests other than GET")
		case method != "POST" && method != "PUT" && method != "DELETE":
			return nil, errors.New("method must be GET, POST, PUT or DELETE")
		}
		full := url.PathEscape(p)
		full = strings.ReplaceAll(full, "%2F", "/")
		if len(a.Query) > 0 {
			q := url.Values{}
			for k, v := range a.Query {
				q.Set(k, toString(v))
			}
			full += "?" + q.Encode()
		}
		var out any
		err = cl.Do(ctx, method, full, a.Body, &out)
		if method != "GET" {
			cl.Invalidate()
		}
		return out, err
	})
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
