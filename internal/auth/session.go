// Package auth provides the single-active-session model and the per-IP
// login rate limiter. Sessions live in memory only — restart = logged out.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"
)

const (
	// InactivityTimeout: the session expires if no validated request has
	// touched it for this long.
	InactivityTimeout = 5 * time.Minute

	// AbsoluteTimeout: hard cap on any single session, regardless of
	// activity. Forces a re-authentication periodically.
	AbsoluteTimeout = 60 * time.Minute

	// sessionIDBytes is the size of the random session token (256 bits).
	sessionIDBytes = 32
)

// Errors returned by Manager. Callers use errors.Is to distinguish.
var (
	ErrSessionActive = errors.New("session already active")
	ErrNoSession     = errors.New("no session")
	ErrExpired       = errors.New("session expired")
)

// session is the in-memory record for the (at most one) active session.
type session struct {
	id           string
	createdAt    time.Time
	lastActivity time.Time
}

// Manager enforces the one-active-session rule. All exported methods are
// goroutine-safe.
//
// The onExpire callback is invoked synchronously whenever a session ends —
// from End() (logout), Validate() (expiry), or Begin() (sweep on next
// login). Wiring it to store.Lock() is how the derived encryption key
// gets zeroed at the right moments.
type Manager struct {
	mu       sync.Mutex
	current  *session
	onExpire func()
}

// NewManager constructs a Manager. onExpire may be nil for tests.
func NewManager(onExpire func()) *Manager {
	return &Manager{onExpire: onExpire}
}

// Begin starts a new session and returns the opaque session ID. If a
// non-expired session already exists, Begin returns ErrSessionActive
// without touching state (the spec rejects second logins outright).
// The caller MUST verify the password before calling Begin.
func (m *Manager) Begin() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current != nil && !m.expiredLocked(m.current, time.Now()) {
		return "", ErrSessionActive
	}
	// A stale (expired) session may still be hanging around — sweep it.
	if m.current != nil {
		m.clearLocked()
	}

	id, err := generateID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	m.current = &session{id: id, createdAt: now, lastActivity: now}
	return id, nil
}

// Validate is called on every authenticated request. It:
//   - returns ErrNoSession if there is no session, or the provided id
//     doesn't match (constant-time compare);
//   - returns ErrExpired if either timeout has lapsed (and clears state);
//   - otherwise updates lastActivity and returns nil.
func (m *Manager) Validate(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == nil {
		return ErrNoSession
	}
	// Constant-time compare blocks timing side-channels on the session ID.
	if subtle.ConstantTimeCompare([]byte(m.current.id), []byte(id)) != 1 {
		return ErrNoSession
	}
	now := time.Now()
	if m.expiredLocked(m.current, now) {
		m.clearLocked()
		return ErrExpired
	}
	m.current.lastActivity = now
	return nil
}

// End destroys the current session. Idempotent — safe to call when
// there's nothing to end. Synchronous: onExpire returns before End does,
// so a logout request can rely on the derived key being wiped by the
// time the HTTP response is sent.
func (m *Manager) End() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearLocked()
}

// HasSession returns true if a non-expired session exists. It side-effect
// cleans up an expired session if it sees one — keeping state honest at
// query time means callers don't need a separate sweep loop.
func (m *Manager) HasSession() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return false
	}
	if m.expiredLocked(m.current, time.Now()) {
		m.clearLocked()
		return false
	}
	return true
}

// expiredLocked reports whether s has hit either timeout. Must be called
// with m.mu held.
func (m *Manager) expiredLocked(s *session, now time.Time) bool {
	if now.Sub(s.lastActivity) >= InactivityTimeout {
		return true
	}
	if now.Sub(s.createdAt) >= AbsoluteTimeout {
		return true
	}
	return false
}

// clearLocked drops the current session and fires onExpire synchronously.
// Must be called with m.mu held. onExpire is invoked while the lock is
// held: the callback is store.Lock() which uses a different mutex, so
// there's no deadlock risk. The synchronous semantics matter — callers
// downstream of End() expect the derived key to be wiped before they
// proceed (e.g. a logout returning 303 to /login).
func (m *Manager) clearLocked() {
	if m.current == nil {
		return
	}
	m.current = nil
	if m.onExpire != nil {
		m.onExpire()
	}
}

// generateID returns a base64url-encoded random token of sessionIDBytes
// raw bytes. No padding so it's URL-safe in a cookie.
func generateID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
