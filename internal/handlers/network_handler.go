package handlers

import (
	"net/http"
	"strings"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/netacl"
)

// network_handler manages the IP allowlist + reverse-proxy settings.
// The NetACL is the ONLY config we persist in plaintext, so the boot
// path can enforce the allowlist on /login itself, before any password
// has been supplied.

type networkPageData struct {
	NetACL       config.NetACL
	FlashError   string
	FlashSuccess string
	Section      string
	ClientIP     string
	// FirstRun is true until the user has saved the network config at
	// least once. Triggers the "you must save before continuing" banner.
	FirstRun bool
}

// networkPage renders /config/network with the current allowlist
// pre-filled. ClientIP is surfaced so a user expanding the allowlist
// can copy their current address rather than try to remember it.
func (h *Handler) networkPage(w http.ResponseWriter, r *http.Request) {
	n := h.store.GetNetACL()
	h.render(w, r, "config_network", networkPageData{
		NetACL:       n,
		Section:      "app",
		ClientIP:     clientIP(r),
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
		FirstRun:     !n.Confirmed,
	})
}

// networkSubmit validates the submitted form, runs ACL.Reload (which
// parses CIDRs and resolves the proxy hostname), and only persists if
// validation succeeded. The "validate first, save second" order is
// deliberate — a typo in the textarea must not write a broken
// netacl.json that would lock the operator out.
//
// SaveNetACL sets Confirmed=true unconditionally, so a successful save
// is also what releases the user from the network-confirmation gate.
func (h *Handler) networkSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flash(w, r, "/config/app/network", "", "invalid form")
		return
	}

	cidrs := parseCIDRTextarea(r.FormValue("cidrs"))
	proxyHost := strings.TrimSpace(r.FormValue("proxy_host"))

	// Snapshot first-run state BEFORE saving so we know whether this save
	// completes the initial setup gate (Confirmed flips from false → true
	// inside SaveNetACL).
	firstRun := !h.store.GetNetACL().Confirmed

	res, err := h.acl.Reload(cidrs, proxyHost)
	if err != nil {
		flash(w, r, "/config/app/network", "", "Invalid network config: "+err.Error())
		return
	}

	saved := &config.NetACL{
		AllowedCIDRs: cidrs,
		ProxyHost:    proxyHost,
	}
	if err := h.store.SaveNetACL(saved); err != nil {
		flash(w, r, "/config/app/network", "", "Save failed: "+err.Error())
		return
	}

	h.log.Info("ACL", "Network config saved; ACL reloaded")

	// First-run save: send to the config landing so the user can keep
	// configuring. Subsequent saves loop back to the network page so
	// the user can keep editing the allowlist.
	target := "/config/app/network"
	if firstRun {
		target = "/config"
	}
	flash(w, r, target, buildNetworkFlashMsg(res), "")
}

// parseCIDRTextarea splits the textarea body into one CIDR per line,
// trimming whitespace and dropping blanks. We delegate actual CIDR
// parsing to netacl.Reload — easier to keep one validator.
func parseCIDRTextarea(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// buildNetworkFlashMsg composes the success-banner text, appending any
// non-fatal proxy resolution notes.
func buildNetworkFlashMsg(res netacl.ReloadResult) string {
	msg := "Network config saved."
	if res.ProxyNote != "" {
		msg += " (Proxy: " + res.ProxyNote + ")"
	}
	return msg
}
