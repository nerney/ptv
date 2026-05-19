package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/nerney/ptv/internal/auth"
	"github.com/nerney/ptv/internal/config"
)

// auth_handler covers the three credential-shaped endpoints:
//   /setup   — uninitialized → create user, seed allowlist with caller IP
//   /login   — initialized   → unlock vault, start session
//   /logout  — initialized   → end session (which zeroes the derived key)
//
// All three are wrapped by ipAllowGuard. Only /setup is reachable while
// the store is uninitialized; only /login is reachable when initialized
// and no session is active.

type setupPageData struct {
	Error    string
	ClientIP string // shown to user as the IP that will be seeded into the allowlist
}

type loginPageData struct {
	Error string
}

// ---------- /setup -------------------------------------------------------

// setupPage renders the first-boot account-creation form. Once the store
// is initialized this page redirects to /login — there is no "second user".
func (h *Handler) setupPage(w http.ResponseWriter, r *http.Request) {
	if h.store.IsInitialized() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h.render(w, r, "setup", setupPageData{ClientIP: clientIP(r)})
}

// setupSubmit validates the form, runs Store.Init (atomically writes
// key/config/metadata/marker), seeds the allowlist with the caller's IP
// as a /32, and auto-creates the first session so the user is logged in
// when they land on the dashboard.
func (h *Handler) setupSubmit(w http.ResponseWriter, r *http.Request) {
	if h.store.IsInitialized() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderSetupErr(w, r, "invalid form")
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")

	if msg, ok := validateSetupInput(username, password, confirm); !ok {
		h.renderSetupErr(w, r, msg)
		return
	}

	// The IP we capture here becomes the bootstrap allowlist entry. The
	// user can broaden it after login via /config/network.
	initialCIDR := clientIP(r) + "/32"
	if err := h.store.Init(password, username, initialCIDR); err != nil {
		h.log.Err("AUTH", "Init failed: "+err.Error())
		h.renderSetupErr(w, r, "Setup failed: "+err.Error())
		return
	}
	h.log.Info("AUTH", "Initialized as "+username+", initial allowlist: "+initialCIDR)

	// Flip ACL out of preInit and load the freshly-saved netacl.json.
	h.acl.SetPreInit(false)
	h.reloadACL()

	// Auto-create the session for the user that just set up. If Begin
	// fails (e.g., crypto/rand exhaustion), fall back to the login page.
	id, err := h.sessions.Begin()
	if err != nil {
		h.log.Err("AUTH", "Begin session post-setup: "+err.Error())
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	setSessionCookie(w, r, id)
	// Forward straight to the network page so the user immediately
	// reviews/expands the bootstrapped /32 allowlist. The
	// networkConfirmedGuard locks them on that page until first save.
	http.Redirect(w, r, "/config/app/network", http.StatusSeeOther)
}

// validateSetupInput enforces the minimal password policy. Returning
// (message, ok) keeps the caller's flow flat — one branch decides the
// error vs. happy path. ok=true means input is acceptable.
func validateSetupInput(username, password, confirm string) (string, bool) {
	switch {
	case username == "" || password == "":
		return "Username and password are required.", false
	case len(password) < 8:
		return "Password must be at least 8 characters.", false
	case password != confirm:
		return "Passwords do not match.", false
	default:
		return "", true
	}
}

func (h *Handler) renderSetupErr(w http.ResponseWriter, r *http.Request, msg string) {
	h.render(w, r, "setup", setupPageData{Error: msg, ClientIP: clientIP(r)})
}

// ---------- /login -------------------------------------------------------

// loginPage renders the password prompt. If a valid session already
// exists on this cookie, send the user home — but DON'T surface
// "session active" on the page itself, that would leak information to
// a different client visiting /login while the legitimate user is in.
func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if !h.store.IsInitialized() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if h.sessions.Validate(c.Value) == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	h.render(w, r, "login", loginPageData{})
}

// loginSubmit is the core unlock path:
//
//  1. Rate-limit check (5 failures/IP/5min → 429).
//  2. Reject second logins with a bare 403 (no body, no info).
//  3. Store.Unlock — decrypt-as-verification; auth-tag failure = bad password.
//  4. Verify the submitted username against the decrypted config.
//  5. Begin a new session, set cookie, redirect home.
//
// On any failure between unlock-success and session-create we MUST
// Lock the store again to wipe the derived key from memory.
func (h *Handler) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.store.IsInitialized() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	ip := clientIP(r)
	if !h.limiter.Allowed(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.render(w, r, "login", loginPageData{Error: "invalid form"})
		return
	}
	if h.sessions.HasSession() {
		// Bare 403 per spec — don't reveal "session active" to a
		// different client visiting /login.
		w.WriteHeader(http.StatusForbidden)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if username == "" || password == "" {
		h.limiter.RecordFailure(ip)
		h.render(w, r, "login", loginPageData{Error: "Incorrect username or password."})
		return
	}
	if err := h.store.Unlock(password); err != nil {
		h.limiter.RecordFailure(ip)
		if errors.Is(err, config.ErrBadPassword) {
			h.log.Err("AUTH", "Bad password from "+ip)
			h.render(w, r, "login", loginPageData{Error: "Incorrect username or password."})
			return
		}
		h.log.Err("AUTH", "Unlock failed: "+err.Error())
		h.render(w, r, "login", loginPageData{Error: "Login failed."})
		return
	}
	if h.store.Get().Username != username {
		h.store.Lock()
		h.limiter.RecordFailure(ip)
		h.log.Err("AUTH", "Bad username from "+ip)
		h.render(w, r, "login", loginPageData{Error: "Incorrect username or password."})
		return
	}
	h.limiter.RecordSuccess(ip)

	id, err := h.sessions.Begin()
	if err != nil {
		// Two paths: (a) race after HasSession check — another session
		// snuck in; (b) crypto/rand failure. Either way the store is now
		// unlocked but no session was started — Lock it before bailing.
		h.store.Lock()
		if errors.Is(err, auth.ErrSessionActive) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		h.log.Err("AUTH", "Begin session: "+err.Error())
		h.render(w, r, "login", loginPageData{Error: "Login failed."})
		return
	}
	setSessionCookie(w, r, id)
	h.log.Info("AUTH", "Logged in from "+ip)
	go h.warmProwlarrSchemas()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---------- /logout ------------------------------------------------------

// logoutSubmit ends the session, which fires the Manager.onExpire
// callback synchronously and wipes the derived key from memory before
// the HTTP response is sent.
func (h *Handler) logoutSubmit(w http.ResponseWriter, r *http.Request) {
	h.sessions.End()
	clearSessionCookie(w)
	h.log.Info("AUTH", "Logged out from "+clientIP(r))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
