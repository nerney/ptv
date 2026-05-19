package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
)

// csrfFormField is the hidden form field name carrying the CSRF token.
const csrfFormField = "ptv.csrf.Token"

type csrfCtxKey struct{}

// csrfMiddleware implements the double-submit cookie pattern: a random token
// is stored in a cookie and embedded in every form as a hidden field. On
// mutating requests the middleware verifies the form value matches the cookie.
// No Origin or Referer check — those headers cause spurious failures behind
// Tailscale and other VPN/proxy topologies, and are redundant when the session
// cookie is SameSite=Strict (cross-origin requests cannot carry credentials).
func csrfMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			if c, err := r.Cookie(csrfCookieName); err == nil {
				token = c.Value
			}
			if token == "" {
				token = newCSRFToken()
				http.SetCookie(w, &http.Cookie{
					Name:     csrfCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
				})
			}

			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				// safe methods — no validation needed
			default:
				if r.FormValue(csrfFormField) != token {
					http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
					return
				}
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), csrfCtxKey{}, token)))
		})
	}
}

func newCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("csrf token generation failed: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(b)
}

func csrfToken(r *http.Request) string {
	v, _ := r.Context().Value(csrfCtxKey{}).(string)
	return v
}

func csrfTemplateField(r *http.Request) template.HTML {
	return template.HTML(fmt.Sprintf(
		`<input type="hidden" name="%s" value="%s">`,
		csrfFormField,
		template.HTMLEscapeString(csrfToken(r)),
	))
}
