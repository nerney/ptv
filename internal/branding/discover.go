// Package branding scrapes a tracker's public landing page (the
// login screen, since UNIT3D redirects unauth GETs there) to extract:
//
//   - a favicon URL → downloaded + base64-encoded as a data URI
//   - a <meta name="theme-color"> hex value
//
// Both fields are best-effort. Discover returns whatever it found,
// and any HTTP/parse failure is non-fatal: the caller treats an
// empty Result as "nothing this attempt — try again next time".
//
// All network operations are bounded by a single context timeout so
// a slow tracker can't stall the caller for long.
package branding

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// totalTimeout caps the entire Discover call (HTML fetch + favicon fetch).
	totalTimeout = 8 * time.Second

	// maxHTMLBytes caps how much of the landing page we'll parse. UNIT3D
	// login HTML is small (~30 KiB); 256 KiB is generous.
	maxHTMLBytes = 256 * 1024

	// maxFaviconBytes caps the favicon download size to keep the encoded
	// config blob reasonable. 64 KiB covers any sane icon.
	maxFaviconBytes = 64 * 1024

	userAgent = "ptv/branding"
)

// Result is what Discover extracted. Either field (or both) may be empty
// — empty means "not found this attempt", not an error.
type Result struct {
	FaviconDataURI string // "data:image/png;base64,..." or ""
	ThemeColor     string // "#RRGGBB" or ""
}

// Discover fetches baseURL, scrapes the response HTML, and (if a favicon
// URL was found) downloads + encodes the favicon as a data URI.
//
// Error semantics:
//   - Network/HTTP errors on the landing-page fetch return an error.
//   - Everything else (no favicon in HTML, favicon download failed,
//     no theme-color tag) returns nil error with a partially-filled
//     Result. Callers treat "no error, empty fields" as a normal
//     "try again later" outcome.
func Discover(ctx context.Context, baseURL string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	html, err := fetchHTML(ctx, baseURL)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		ThemeColor: extractThemeColor(html),
	}

	if favURL := extractFaviconURL(html, baseURL); favURL != "" {
		if dataURI, err := fetchFavicon(ctx, favURL); err == nil {
			res.FaviconDataURI = dataURI
		}
	}
	return res, nil
}

// fetchHTML GETs baseURL and returns the response body (capped). We
// follow redirects (UNIT3D usually 302s GET / to /login or similar),
// which is the default http.Client behavior.
func fetchHTML(ctx context.Context, baseURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", errors.New("HTTP " + resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// themeColorRE matches both attribute orderings:
//
//	<meta name="theme-color" content="#1a2b3c">
//	<meta content="#1a2b3c" name="theme-color">
//
// The hex value is validated separately by the caller.
var themeColorRE = regexp.MustCompile(
	`(?i)<meta\s+(?:[^>]*\sname\s*=\s*["']theme-color["'][^>]*\scontent\s*=\s*["']([^"']+)["']` +
		`|[^>]*\scontent\s*=\s*["']([^"']+)["'][^>]*\sname\s*=\s*["']theme-color["'])`)

// hexColorRE accepts #RGB and #RRGGBB. We don't promote the short form
// — most modern themes use full hex anyway.
var hexColorRE = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// extractThemeColor pulls the first valid hex color from a
// <meta name="theme-color"> tag. Returns "" if absent or malformed.
func extractThemeColor(html string) string {
	m := themeColorRE.FindStringSubmatch(html)
	if m == nil {
		return ""
	}
	// Capture group 1 = name-then-content order, group 2 = the reverse.
	val := strings.TrimSpace(m[1])
	if val == "" {
		val = strings.TrimSpace(m[2])
	}
	if !hexColorRE.MatchString(val) {
		return ""
	}
	return val
}

// fullURLFaviconRE finds the first absolute URL anywhere in the HTML
// whose path contains "favicon" (case-insensitive). Matches things like
//
//	href="https://tracker.tld/favicon.ico"
//	src='https://cdn.tracker.tld/static/img/favicon-32x32.png'
var fullURLFaviconRE = regexp.MustCompile(
	`(?i)https?://[^\s"'<>)]*favicon[^\s"'<>)]*`)

// relFaviconRE finds href="..." or src="..." values containing "favicon"
// when no absolute URL was present — we resolve them against baseURL.
var relFaviconRE = regexp.MustCompile(
	`(?i)(?:href|src)\s*=\s*["']([^"']*favicon[^"']*)["']`)

// extractFaviconURL returns the first usable favicon URL — preferring
// an absolute URL, falling back to a relative path resolved against base.
// Returns "" if nothing usable is found.
func extractFaviconURL(html, baseURL string) string {
	if m := fullURLFaviconRE.FindString(html); m != "" {
		return m
	}
	m := relFaviconRE.FindStringSubmatch(html)
	if m == nil {
		return ""
	}
	rel := m[1]
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	relURL, err := url.Parse(rel)
	if err != nil {
		return ""
	}
	return base.ResolveReference(relURL).String()
}

// fetchFavicon downloads favURL, caps the body size, and returns a
// data: URI suitable for an <img src="...">. The MIME type comes from
// the Content-Type header, falling back to image/x-icon (the .ico default).
func fetchFavicon(ctx context.Context, favURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", favURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", errors.New("favicon HTTP " + resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFaviconBytes))
	if err != nil || len(body) == 0 {
		return "", errors.New("empty favicon body")
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/x-icon"
	}
	// Strip any charset/parameters; data URIs prefer the bare type.
	if i := strings.Index(ct, ";"); i > 0 {
		ct = strings.TrimSpace(ct[:i])
	}

	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(body), nil
}
