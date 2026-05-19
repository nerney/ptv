package handlers

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gorilla/csrf"
)

func TestCSRFMiddlewareRejectsMissingToken(t *testing.T) {
	protected := csrfMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/config/app/network", nil))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestCSRFMiddlewareAllowsValidToken(t *testing.T) {
	protected := csrfMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `<form method="POST">%s</form>`, csrf.TemplateField(r))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	getRR := httptest.NewRecorder()
	protected.ServeHTTP(getRR, httptest.NewRequest(http.MethodGet, "/config/app/network", nil))

	token := csrfTokenFromBody(t, getRR.Body.String())
	form := url.Values{csrfTokenFormField: {token}}
	post := httptest.NewRequest(http.MethodPost, "/config/app/network", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "https://example.com/config/app/network")
	for _, c := range getRR.Result().Cookies() {
		post.AddCookie(c)
	}

	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, post)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%q", rr.Code, http.StatusNoContent, rr.Body.String())
	}
}

const csrfTokenFormField = "gorilla.csrf.Token"

var csrfTokenRE = regexp.MustCompile(`name="` + csrfTokenFormField + `" value="([^"]+)"`)

func csrfTokenFromBody(t *testing.T, body string) string {
	t.Helper()
	m := csrfTokenRE.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("csrf token not found in %q", body)
	}
	return html.UnescapeString(m[1])
}
