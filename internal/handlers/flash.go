package handlers

import (
	"net/http"
	"net/url"
)

// flash redirects to base with optional ?ok=... and/or ?err=... query
// parameters. Pages render those values as success/error banners.
//
// Using URL query parameters as a flash channel keeps the server stateless:
// no session-side flash storage, and the message survives one navigation
// (the redirect target). It's not robust against a refresh, which is the
// intended behavior — we don't want stale banners reappearing.
func flash(w http.ResponseWriter, r *http.Request, base, ok, errMsg string) {
	q := url.Values{}
	if ok != "" {
		q.Set("ok", ok)
	}
	if errMsg != "" {
		q.Set("err", errMsg)
	}
	target := base
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
