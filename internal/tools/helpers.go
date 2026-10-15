package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mattoddie/hive-mcp/internal/hive"
)

// Hive's heating and TRV limits, as the Hive app and pyhiveapi apply them.
const (
	minTemp  = 5.0
	maxTemp  = 32.0
	tempStep = 0.5
)

var tempRange = [2]float64{minTemp, maxTemp}

// checkTemperature validates a target temperature: 5–32 °C in steps of 0.5.
func checkTemperature(name string, t float64) error {
	if math.IsNaN(t) || t < minTemp || t > maxTemp {
		return fmt.Errorf("%s must be between %g and %g °C, got %g", name, minTemp, maxTemp, t)
	}
	if r := t / tempStep; math.Abs(r-math.Round(r)) > 1e-9 {
		return fmt.Errorf("%s must be in steps of %g °C (for example 20 or 20.5), got %g", name, tempStep, t)
	}
	return nil
}

// find picks one product (or device) from list by ID or name. An empty ref
// is fine when there's only one. Names match case-insensitively, exactly or
// as a unique part of the name.
func find(list []Obj, ref, what string) (Obj, error) {
	ref = strings.TrimSpace(ref)
	if len(list) == 0 {
		return nil, fmt.Errorf("there is no %s on this Hive account", what)
	}
	names := func() string {
		var n []string
		for _, o := range list {
			n = append(n, fmt.Sprintf("%q", hive.Name(o)))
		}
		return strings.Join(n, ", ")
	}
	if ref == "" {
		if len(list) == 1 {
			return list[0], nil
		}
		return nil, fmt.Errorf("there is more than one %s; say which: %s", what, names())
	}
	for _, o := range list {
		if hive.Str(o, "id") == ref {
			return o, nil
		}
	}
	q := strings.ToLower(ref)
	var partial []Obj
	for _, o := range list {
		name := strings.ToLower(hive.Name(o))
		if name == q {
			return o, nil
		}
		if strings.Contains(name, q) {
			partial = append(partial, o)
		}
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	if len(partial) > 1 {
		return nil, fmt.Errorf("%q matches more than one %s; say which: %s", ref, what, names())
	}
	return nil, fmt.Errorf("no %s called %q; the choices are %s", what, ref, names())
}

// parseWhen reads a date or time in the home's time zone: RFC 3339, "2006-01-02T15:04", "2006-01-02 15:04" or "2006-01-02" (midnight).
func parseWhen(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date or time; use YYYY-MM-DD or YYYY-MM-DDTHH:MM (the home's local time)", s)
}

// change sends a state change to a product and reports what was sent and how it looks afterwards.
func change(ctx context.Context, cl *hive.Client, p Obj, body Obj, summarize func(*hive.Nodes, Obj) Obj) (any, error) {
	typ, id := hive.Str(p, "type"), hive.Str(p, "id")
	if _, err := cl.SetNode(ctx, typ, id, body); err != nil {
		return nil, err
	}
	out := Obj{"done": true, "name": hive.Name(p), "sent": body}
	n, err := cl.Nodes(ctx, true)
	if err != nil {
		out["note"] = "The change was accepted, but reading the new state failed: " + err.Error()
		return out, nil
	}
	for _, q := range n.Products() {
		if hive.Str(q, "id") == id {
			out["now"] = summarize(n, q)
		}
	}
	out["note"] = "Hive can take a few seconds to show a change everywhere, and the boiler or device a little longer to respond."
	return out, nil
}

var errNoChange = errors.New("nothing to change: give at least one setting")

type rawArgs struct {
	Raw bool `json:"raw,omitempty" jsonschema:"Return Hive's full objects instead of compact summaries (much larger)"`
}

type freshArgs struct {
	Fresh bool `json:"fresh,omitempty" jsonschema:"Ask Hive again instead of using a reading from the last few seconds (HIVE_CACHE_SECONDS)"`
}

type rawFreshArgs struct {
	rawArgs
	freshArgs
}
