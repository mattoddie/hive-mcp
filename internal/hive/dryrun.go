package hive

import (
	"context"
	"strings"
	"sync"
)

// Dry runs: a tool call with dry_run: true puts a *DryRun in its context.
// Client.Do then records each request that would change something instead
// of sending it, and answers as if Hive had accepted it with an empty body.
// GETs still go through, so the call validates against the live state.

// PlannedRequest is a request a dry run didn't send.
type PlannedRequest struct {
	Method string
	Path   string
	Query  string // the encoded query string, if any
	Body   any
	// Current is the state the request would change, when the tool gave it
	// with SetCurrent before making the request.
	Current any
}

// DryRun collects the requests of one dry-run call.
type DryRun struct {
	mu       sync.Mutex
	requests []PlannedRequest
	current  any
}

type dryRunKey struct{}

// WithDryRun returns a context in which changes are recorded, not sent.
func WithDryRun(ctx context.Context) (context.Context, *DryRun) {
	d := &DryRun{}
	return context.WithValue(ctx, dryRunKey{}, d), d
}

// IsDryRun reports whether ctx is a dry run.
func IsDryRun(ctx context.Context) bool {
	_, ok := ctx.Value(dryRunKey{}).(*DryRun)
	return ok
}

// SetCurrent gives, in a dry run, the current state that the next request
// would change, so the preview can show it and what would change. Outside a
// dry run it does nothing.
func SetCurrent(ctx context.Context, current any) {
	if d, ok := ctx.Value(dryRunKey{}).(*DryRun); ok {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.current = current
	}
}

// Requests returns the requests that would have been sent, in order.
func (d *DryRun) Requests() []PlannedRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]PlannedRequest{}, d.requests...)
}

func (d *DryRun) record(method, path string, body any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	path, query, _ := strings.Cut(path, "?")
	d.requests = append(d.requests, PlannedRequest{Method: method, Path: path, Query: query, Body: body, Current: d.current})
	d.current = nil
}
