// Package handlers wires HTTP routes, the middleware chain, and template
// rendering for the dashboard. The middleware order (sleep-guard → IP
// allowlist → session auth) is the heart of the security model: any one
// of them can short-circuit a request before it reaches a handler.
package handlers

import (
	"crypto/rand"
	"embed"
	"html/template"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/csrf"

	"github.com/nerney/ptv/internal/auth"
	"github.com/nerney/ptv/internal/autobrrdefs"
	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/defs"
	"github.com/nerney/ptv/internal/logger"
	"github.com/nerney/ptv/internal/netacl"
	"github.com/nerney/ptv/internal/prowlarr"
)

const (
	// sessionCookieName is the name of the single auth cookie. It carries
	// only an opaque session ID; the derived key lives in process memory.
	sessionCookieName = "ptdash_sid"
	csrfCookieName    = "_ptv_csrf"

	// setupWindow caps how long the dashboard accepts setup requests on
	// first boot. If the user doesn't complete /setup within this window,
	// the process flips into sleep mode (every request returns 503) until
	// the container is restarted. This blocks slow-burn brute-force probing
	// of the initial wide-open state without triggering Docker's auto-restart.
	setupWindow = 5 * time.Minute
)

// Handler bundles every dependency a route needs. Methods on Handler back
// every route in NewRouter.
type Handler struct {
	store         *config.Store
	syncer        *defs.Syncer
	autobrrSyncer *autobrrdefs.Syncer
	log           *logger.Logger
	acl           *netacl.ACL
	sessions      *auth.Manager
	limiter       *auth.RateLimiter
	validateFn    func(typeID, url, apiKey string) (*config.UserStats, error)

	templates map[string]*template.Template
	fs        embed.FS

	// sleeping flips to true if the setup window expires without setup
	// completing. The sleep-guard middleware reads it on every request.
	sleeping atomic.Bool

	// prowlarr schema cache — populated by warmProwlarrSchemas.
	pSchemasMu sync.RWMutex
	pSchemas   map[string]prowlarr.IndexerSchema

	pMetadataMu  sync.RWMutex
	pAppProfiles cachedAppProfiles
	pTags        cachedTags
}

// NewRouter builds the fully-wired HTTP handler tree:
//
//	chi.Recoverer            (recover from panics, 500 on crash)
//	  noCache                (Cache-Control: no-store on every response)
//	    sleepGuard           (503 if the setup window elapsed)
//	      ipAllowGuard       (403 unless client IP is allowlisted)
//	        /setup, /login, /logout         (public-but-IP-gated)
//	        authGuard                       (session check)
//	          /, /config/**, /refresh/**    (require active session)
//
// Pre-init bootstrap: when /config/.initialized is missing, the ACL is
// flipped into preInit mode (allows any IP) and a goroutine arms the
// setupWindow timer.
func NewRouter(store *config.Store, syncer *defs.Syncer, autobrrSyncer *autobrrdefs.Syncer, fs embed.FS) http.Handler {
	log := logger.New()
	h := &Handler{
		store:         store,
		syncer:        syncer,
		autobrrSyncer: autobrrSyncer,
		log:           log,
		acl:           netacl.New(),
		limiter:       auth.NewRateLimiter(),
		fs:            fs,
	}

	// onExpire fires when a session is torn down (logout, idle/absolute
	// timeout, or single-session sweep on next login). Zeroing the derived
	// key is the whole point of the auth flow.
	h.sessions = auth.NewManager(func() {
		store.Lock()
		log.Info("AUTH", "Session ended — derived key zeroed")
	})

	h.templates = h.parseTemplates()

	// On a previously-initialized boot the plaintext netacl.json is on
	// disk; load it now so the allowlist applies to /login itself, before
	// any password verification can happen.
	if store.IsInitialized() {
		h.acl.SetPreInit(false)
		h.reloadACL()
	} else {
		h.armSetupWindow()
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(noCache)
	r.Use(h.sleepGuard)
	r.Use(h.ipAllowGuard)
	r.Use(csrfMiddleware())

	// Static assets — IP-gated, but no session required (CSS shouldn't 302).
	r.Handle("/static/*", http.FileServer(http.FS(fs)))

	// Setup / login / logout sit outside the auth group: the chicken-and-egg
	// pages that gate everything else. They're still IP-restricted.
	r.Get("/setup", h.setupPage)
	r.Post("/setup", h.setupSubmit)
	r.Get("/login", h.loginPage)
	r.Post("/login", h.loginSubmit)
	r.Post("/logout", h.logoutSubmit)

	// Anything below this group requires a valid session AND a confirmed
	// network configuration. The confirmation gate redirects to
	// /config/network until SaveNetACL has been called at least once —
	// "save once before you can use anything else" per setup spec.
	r.Group(func(r chi.Router) {
		r.Use(h.authGuard)
		r.Use(h.networkConfirmedGuard)

		r.Get("/", h.dashboard)

		// Config landing → drill-in sub-pages
		r.Get("/config", h.configLanding)
		r.Get("/config/app", redirectTo("/config/app/network"))
		r.Get("/config/app/network", h.networkPage)
		r.Post("/config/app/network", h.networkSubmit)
		r.Get("/config/integrations", h.integrationsPage)

		// Tracker add/config screens
		r.Get("/trackers/add", h.trackerAddPage)
		r.Get("/tracker/{idx}/config", h.trackerConfigPage)
		r.Post("/tracker/{idx}/config", h.configTrackerUpdate)
		r.Post("/tracker/{idx}/config/prowlarr", h.configTrackerProwlarrPost)
		r.Get("/tracker/{idx}/config/prowlarr/diff", h.trackerProwlarrDiffPage)
		r.Post("/tracker/{idx}/config/prowlarr/diff", h.trackerProwlarrDiffPush)
		r.Post("/tracker/{idx}/config/autobrr", h.trackerAutobrrConfigPost)
		r.Post("/trackers/add", h.configAdd)
		r.Post("/tracker/{idx}/config/ptv", h.configTrackerUpdate)
		r.Post("/tracker/{idx}/config/delete", h.configTrackerDelete)

		// Legacy tracker paths kept as action aliases.
		r.Get("/config/trackers", redirectTo("/trackers/add"))
		r.Post("/config/add", h.configAdd)
		r.Post("/config/tracker/{idx}/update", h.configTrackerUpdate)
		r.Post("/config/tracker/{idx}/delete", h.configTrackerDelete)
		r.Post("/config/tracker/{idx}/prowlarr/add", h.configTrackerProwlarrAdd)
		r.Post("/config/tracker/{idx}/prowlarr/toggle", h.configTrackerProwlarrToggle)
		r.Post("/config/tracker/{idx}/prowlarr/remove", h.configTrackerProwlarrRemove)
		r.Post("/config/tracker/{idx}/prowlarr", h.configTrackerProwlarrPost)
		r.Post("/config/tracker/{idx}/autobrr/add", h.configTrackerAutobrrAdd)
		r.Post("/config/tracker/{idx}/autobrr/toggle", h.configTrackerAutobrrToggle)
		r.Post("/config/tracker/{idx}/autobrr/remove", h.configTrackerAutobrrRemove)

		// Prowlarr integration — global settings/import plus dashboard sync.
		r.Get("/config/integrations/prowlarr", h.configProwlarrPage)
		r.Post("/config/integrations/prowlarr", h.configProwlarrPost)
		r.Post("/config/integrations/prowlarr/enable", h.configProwlarrEnable)
		r.Post("/config/integrations/prowlarr/disable", h.configProwlarrDisable)
		r.Get("/config/integrations/prowlarr/import", h.importPage)
		r.Post("/config/integrations/prowlarr/import", h.importSubmit)
		r.Get("/sync/prowlarr", h.prowlarrSyncPage)
		r.Post("/sync/prowlarr", h.prowlarrSyncSubmit)
		r.Get("/sync/autobrr", h.autobrrSyncPage)
		r.Post("/sync/autobrr", h.autobrrSyncSubmit)
		r.Get("/config/prowlarr", redirectTo("/config/integrations/prowlarr"))
		r.Get("/config/prowlarr/import", redirectTo("/config/integrations/prowlarr/import"))
		r.Get("/config/prowlarr/sync", redirectTo("/sync/prowlarr"))

		// Autobrr integration — global settings/import.
		r.Get("/config/integrations/autobrr", h.configAutobrrPage)
		r.Post("/config/integrations/autobrr", h.configAutobrrPost)
		r.Post("/config/integrations/autobrr/enable", h.configAutobrrEnable)
		r.Post("/config/integrations/autobrr/disable", h.configAutobrrDisable)
		r.Get("/config/integrations/autobrr/import", h.importAutobrrPage)
		r.Post("/config/integrations/autobrr/import", h.importAutobrrSubmit)
		r.Get("/config/autobrr", redirectTo("/config/integrations/autobrr"))
		r.Get("/config/autobrr/import", redirectTo("/config/integrations/autobrr/import"))

		// Network tab — IP allowlist + reverse-proxy host
		r.Get("/config/network", redirectTo("/config/app/network"))
		r.Post("/config/network", h.networkSubmit)

		// Stats refresh from UNIT3D — all + per-card
		r.Post("/refresh", h.refresh)
		r.Post("/refresh/{idx}", h.refreshOne)
	})

	return r
}

// armSetupWindow starts the 5-minute timer that flips the process into
// sleep mode if the user hasn't completed setup. Running in a goroutine
// because NewRouter must return immediately so the HTTP listener can start.
func (h *Handler) armSetupWindow() {
	go func() {
		t := time.NewTimer(setupWindow)
		defer t.Stop()
		<-t.C
		// Re-check at fire time: setup may have completed during the wait.
		if !h.store.IsInitialized() {
			h.sleeping.Store(true)
			h.log.Err("AUTH", "Setup window expired — entering 503 sleep mode")
		}
	}()
}

// ---------- middleware ---------------------------------------------------

// noCache prevents browsers/proxies from caching authenticated pages.
// Cheap, applied to every response.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// sleepGuard short-circuits every request with a bare 503 once the setup
// window has elapsed. No body, no info — Docker healthchecks against
// other endpoints will fail and the operator restarts manually.
func (h *Handler) sleepGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.sleeping.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ipAllowGuard is the network-level gate. It:
//   - resolves the actual client IP (honoring X-Forwarded-For only from
//     trusted proxies);
//   - rejects the request with a bare 403 if the resolved IP isn't on
//     the allowlist;
//   - on success, stashes the resolved IP in the request context so the
//     handlers can read it without re-parsing RemoteAddr.
func (h *Handler) ipAllowGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, ok := h.acl.ClientIP(r)
		if !ok {
			// Untrusted-proxy XFF missing, or RemoteAddr unparseable.
			http.Error(w, "", http.StatusForbidden)
			return
		}
		if !h.acl.Allowed(ip) {
			h.log.Info("ACL", "blocked "+ip+" "+r.Method+" "+r.URL.Path)
			http.Error(w, "", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(withClientIP(r.Context(), ip)))
	})
}

// networkConfirmedGuard redirects any auth-group request back to
// /config/app/network until the user has saved the network config at
// least once. The network page itself is exempt (otherwise the user
// would be stuck in an infinite redirect). Logout is exempt too so
// users can always get out.
func (h *Handler) networkConfirmedGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pathBypassesNetworkGate(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if h.store.GetNetACL().Confirmed {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "/config/app/network", http.StatusSeeOther)
	})
}

// pathBypassesNetworkGate names the paths that may be reached while
// the user hasn't confirmed network config yet.
func pathBypassesNetworkGate(p string) bool {
	return p == "/config/app/network" || p == "/config/network" || p == "/logout"
}

// authGuard requires an active session for any route it covers.
//   - No session cookie → redirect to /login (or /setup if uninitialized).
//   - Cookie present but session expired/invalid → clear cookie, redirect.
//   - Valid session → Validate() updates lastActivity, request proceeds.
func (h *Handler) authGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.store.IsInitialized() {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if err := h.sessions.Validate(c.Value); err != nil {
			clearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- cookie helpers ----------------------------------------------

// setSessionCookie writes the session ID into a hardened cookie:
// HttpOnly + SameSite=Strict block JS access and CSRF leaks; Secure is
// enabled automatically when serving over TLS. No MaxAge → session cookie
// (cleared when browser closes), matching the in-memory session model.
func setSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
	})
}

// clearSessionCookie expires the cookie immediately by sending MaxAge=-1.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// ---------- ACL reload ---------------------------------------------------

// reloadACL rebuilds the live allowlist from the plaintext NetACL on
// disk. Called from three places:
//   - boot (when /config/.initialized exists);
//   - immediately after successful setup;
//   - whenever /config/network is saved.
//
// Proxy DNS resolution happens here, not at request time — per spec
// ("Do NOT resolve at request time"). If a proxy hostname stops
// resolving, the trusted-proxy list silently empties until next save.
func (h *Handler) reloadACL() {
	n := h.store.GetNetACL()
	res, err := h.acl.Reload(n.AllowedCIDRs, n.ProxyHost)
	if err != nil {
		h.log.Err("ACL", "reload: "+err.Error())
		return
	}
	if len(res.ResolvedProxyIPs) > 0 {
		h.log.Info("ACL", "Trusted proxies: "+strings.Join(res.ResolvedProxyIPs, ", "))
	}
}

// ---------- template parsing --------------------------------------------

// parseTemplates loads every page template once, at startup, from the
// embedded FS. Each entry is a fully-resolved template tree including
// the shared layout and any partials it needs — rendering is a hash lookup.
//
// Templates are embedded (not read from disk) so the binary is self-
// contained; this means a typo here panics at process start, which is
// the right time to find out.
func (h *Handler) parseTemplates() map[string]*template.Template {
	funcs := templateFuncs()

	const (
		layout      = "templates/layout.html"
		card        = "templates/partials/tracker_card.html"
		configNav   = "templates/partials/config_nav.html"
		prowlarrNav = "templates/partials/prowlarr_nav.html"
		autobrrNav  = "templates/partials/autobrr_nav.html"
		setting     = "templates/partials/setting_field.html"
	)

	parse := func(rootName string, files ...string) *template.Template {
		return template.Must(
			template.New(rootName).Funcs(funcs).ParseFS(h.fs, files...),
		)
	}

	return map[string]*template.Template{
		"dashboard":              parse("layout", layout, "templates/dashboard.html", card),
		"setup":                  parse("layout", layout, "templates/setup.html"),
		"login":                  parse("layout", layout, "templates/login.html"),
		"config_landing":         parse("layout", layout, "templates/config_landing.html"),
		"integrations":           parse("layout", layout, configNav, "templates/integrations.html"),
		"tracker_add":            parse("layout", layout, configNav, "templates/tracker_add.html"),
		"tracker_config_unified": parse("layout", layout, setting, "templates/tracker_config_unified.html"),
		"config_trackers":        parse("layout", layout, configNav, "templates/config_trackers.html"),
		"config_prowlarr":        parse("layout", layout, configNav, prowlarrNav, "templates/config_prowlarr.html"),
		"config_autobrr":         parse("layout", layout, configNav, autobrrNav, "templates/config_autobrr.html"),
		"config_network":         parse("layout", layout, configNav, "templates/config_network.html"),
		"import":                 parse("layout", layout, configNav, prowlarrNav, "templates/import.html"),
		"autobrr_import":         parse("layout", layout, configNav, autobrrNav, "templates/autobrr_import.html"),
		"prowlarr_sync":          parse("layout", layout, configNav, prowlarrNav, "templates/prowlarr_sync.html"),
		"autobrr_sync":           parse("layout", layout, "templates/autobrr_sync.html"),
		"tracker_prowlarr_diff":  parse("layout", layout, "templates/tracker_prowlarr_diff.html"),
		"tracker_cards":          parse("tracker_cards", "templates/partials/tracker_cards.html", card),
		"tracker_card":           parse("tracker_card", card),
	}
}

// render writes a full HTML page (layout + content) for the named template.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
	t, ok := h.templates[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", templateDataWithCSRF(r, data)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func newCSRFKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("csrf key generation failed: " + err.Error())
	}
	return key
}

func csrfMiddleware() func(http.Handler) http.Handler {
	return csrf.Protect(
		newCSRFKey(),
		csrf.CookieName(csrfCookieName),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteStrictMode),
		csrf.Secure(false),
	)
}

func templateDataWithCSRF(r *http.Request, data interface{}) interface{} {
	field := csrf.TemplateField(r)
	token := csrf.Token(r)
	if data == nil {
		return map[string]interface{}{"CSRFField": field, "CSRFToken": token}
	}
	v := reflect.ValueOf(data)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return map[string]interface{}{"CSRFField": field, "CSRFToken": token}
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String {
		out := make(map[string]interface{}, v.Len()+1)
		for _, key := range v.MapKeys() {
			out[key.String()] = v.MapIndex(key).Interface()
		}
		out["CSRFField"] = field
		out["CSRFToken"] = token
		return out
	}
	if v.Kind() != reflect.Struct {
		return struct {
			Data      interface{}
			CSRFField template.HTML
			CSRFToken string
		}{Data: data, CSRFField: field, CSRFToken: token}
	}
	out := make(map[string]interface{}, v.NumField()+1)
	out["CSRFField"] = field
	out["CSRFToken"] = token
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		out[sf.Name] = v.Field(i).Interface()
	}
	return out
}

// renderPartial writes only the named template (no surrounding layout) —
// used for htmx swap responses where the layout is already in the DOM.
func (h *Handler) renderPartial(w http.ResponseWriter, name string, data interface{}) {
	t, ok := h.templates[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
