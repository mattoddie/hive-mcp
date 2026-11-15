package hive

import "time"

// SetNow replaces the Auth's clock, so tests can step past backoffs and cooldowns.
func SetNow(a *Auth, now func() time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now = now
}

// SetClientNow replaces the Client's clock, so tests can step past the cache's timings.
func SetClientNow(c *Client, now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}
