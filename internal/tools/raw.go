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
	desc := "Call any path of Hive's API (the undocumented \"beekeeper\" API the Hive app uses) directly, for anything the other tools don't cover, such as /nodes/all, /devices, /products or /actions. The account's user profile (name, email, phone, address) is redacted from responses. "
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
		// The account's own profile endpoints return the profile at the top level.
		if lp := strings.ToLower(p); lp == "/user" || strings.HasPrefix(lp, "/user/") || strings.HasPrefix(lp, "/users") {
			out = redactProfiles(out)
		}
		return redactPII(out), err
	})
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// profileKeep are the fields of the account's user profile that raw output
// keeps; everything else (name, email, phone, address, postcode, ...) is
// personal data the model doesn't need.
var profileKeep = map[string]bool{
	"id": true, "temperatureUnit": true, "timezone": true, "timeZone": true,
	"locale": true, "language": true, "country": true, "countryCode": true,
}

// redactProfile masks a user profile object.
func redactProfile(u Obj) Obj {
	out := make(Obj, len(u))
	for k, v := range u {
		if profileKeep[k] || v == nil {
			out[k] = v
		} else {
			out[k] = "[redacted]"
		}
	}
	return out
}

// redactPII copies v with every "user" object masked by redactProfile. It
// never changes v, which may be the shared cached reading.
func redactPII(v any) any {
	switch x := v.(type) {
	case Obj:
		out := make(Obj, len(x))
		for k, val := range x {
			if k == "user" || k == "users" {
				out[k] = redactProfiles(val)
			} else {
				out[k] = redactPII(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactPII(val)
		}
		return out
	}
	return v
}

// redactProfiles masks a profile, or a list of them.
func redactProfiles(v any) any {
	switch x := v.(type) {
	case Obj:
		return redactProfile(x)
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactProfiles(val)
		}
		return out
	}
	return v
}

// redactObj is redactPII for one object from a raw: true listing.
func redactObj(o Obj) Obj {
	r, _ := redactPII(o).(Obj)
	return r
}
