package auth

import (
	"sync"
	"time"
)

// RateLimiter is a per-IP failed-login throttle. After maxAttempts
// failures inside attemptWindow, that IP is locked for lockDuration —
// during which Allowed() returns false even if no further attempts are
// made. A successful login resets that IP's record.
//
// The "lock window" semantics (rather than an exponential backoff) are
// per spec: "5 attempts → temporary lock". Memory grows with distinct
// attacker IPs; for a single-user dashboard that's bounded.

const (
	maxAttempts   = 5
	attemptWindow = 5 * time.Minute
	lockDuration  = 5 * time.Minute
)

// attemptRecord tracks the failures from one IP. lockedAt is zero
// until the IP crosses maxAttempts; after that it's the timestamp
// the lock started (used to compute when it expires).
type attemptRecord struct {
	failures []time.Time
	lockedAt time.Time
}

// RateLimiter is goroutine-safe; one instance is shared across handlers.
type RateLimiter struct {
	mu      sync.Mutex
	records map[string]*attemptRecord
}

// NewRateLimiter returns a fresh, empty limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{records: map[string]*attemptRecord{}}
}

// Allowed returns true if ip may attempt a login right now.
// If a previous lock has expired, the record is dropped on this call —
// no separate sweep is needed.
func (r *RateLimiter) Allowed(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[ip]
	if rec == nil {
		return true
	}
	if !rec.lockedAt.IsZero() && time.Since(rec.lockedAt) < lockDuration {
		return false
	}
	if !rec.lockedAt.IsZero() {
		// Lock has elapsed — clear state so the IP starts fresh.
		delete(r.records, ip)
	}
	return true
}

// RecordFailure adds a failed-login timestamp for ip. Old failures
// outside attemptWindow are trimmed first, then the new one is appended;
// if the result hits maxAttempts the IP gets locked.
func (r *RateLimiter) RecordFailure(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[ip]
	if rec == nil {
		rec = &attemptRecord{}
		r.records[ip] = rec
	}
	now := time.Now()
	cutoff := now.Add(-attemptWindow)
	// Reuse the underlying array — trimming in place is cheap.
	kept := rec.failures[:0]
	for _, t := range rec.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	rec.failures = append(kept, now)
	if len(rec.failures) >= maxAttempts {
		rec.lockedAt = now
	}
}

// RecordSuccess clears any tracked failures for ip. A successful login
// "forgives" the prior failures from that source.
func (r *RateLimiter) RecordSuccess(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, ip)
}
