package hive

import "time"

// SetNow replaces the Auth's clock, so tests can step past backoffs and cooldowns.
func SetNow(a *Auth, now func() time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now = now
}
