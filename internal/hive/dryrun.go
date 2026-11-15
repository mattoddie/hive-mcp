package hive

import (
	"context"
	"sync"
)

// Dry runs: a tool call with dry_run: true puts a *DryRun in its context.
// Client.Do then records each request that would change something instead
// of sending it, and answers as if Hive had accepted it with an empty body.
// GETs still go through, so the call validates against the live state.

// PlannedRequest is a request a dry run didn't send.
type PlannedRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

// DryRun collects the requests of one dry-run call.
type DryRun struct {
	mu       sync.Mutex
	requests []PlannedRequest
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

// Requests returns the requests that would have been sent, in order.
func (d *DryRun) Requests() []PlannedRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]PlannedRequest{}, d.requests...)
}

func (d *DryRun) record(r PlannedRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, r)
}
