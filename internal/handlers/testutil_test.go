package handlers

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/logger"
	"github.com/nerney/ptv/internal/prowlarr"
)

func newTestStore(t *testing.T, cfg config.Config) *config.Store {
	t.Helper()
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Init("test-password", "tester", "127.0.0.1/32"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := store.Save(&cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return store
}

func newTestHandler(t *testing.T, cfg config.Config) *Handler {
	t.Helper()
	tpl := template.Must(template.New("layout").Parse(
		`{{define "layout"}}{{.ValidationError}}|{{.SubmittedURL}}|{{.SubmittedAPIKey}}{{end}}`,
	))
	return &Handler{
		store: newTestStore(t, cfg),
		log:   logger.New(),
		templates: map[string]*template.Template{
			"tracker_config_unified": tpl,
		},
		pSchemas: map[string]prowlarr.IndexerSchema{},
	}
}

func withTrackerIdx(r *http.Request, idx string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("idx", idx)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func newFormRequest(method, target, form string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}
