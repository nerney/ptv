package handlers

import (
	"net/http"

	"github.com/nerney/ptv/internal/config"
)

type integrationsData struct {
	Config       config.Config
	FlashError   string
	FlashSuccess string
	Section      string
}

func (h *Handler) integrationsPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "integrations", integrationsData{
		Config:       h.store.Get(),
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
		Section:      "integrations",
	})
}

func redirectTo(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, path, http.StatusSeeOther)
	}
}
