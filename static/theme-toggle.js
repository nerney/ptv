// theme-toggle.js
//
// Theme rules:
//   - On every page load: read localStorage("theme"). If present
//     ("light" or "dark") stamp it onto <html data-theme="...">.
//   - If absent: leave <html> untouched so CSS's prefers-color-scheme
//     media query decides (which falls back to the :root dark default
//     when the system has no preference).
//   - Clicking #theme-toggle flips to the opposite of whatever is
//     currently effective and persists the choice.
//
// The initial stamping happens via the inline <script> block in
// layout.html (loaded synchronously in <head>) so the page never
// renders in the wrong theme. This file is the click handler.

(function () {
  function currentEffectiveTheme() {
    var explicit = document.documentElement.getAttribute("data-theme");
    if (explicit === "light" || explicit === "dark") return explicit;
    // No explicit choice — derive from system pref. Defaults to dark
    // when the system has no preference (matchMedia returns false).
    return window.matchMedia("(prefers-color-scheme: light)").matches
      ? "light"
      : "dark";
  }

  function applyTheme(theme) {
    document.documentElement.setAttribute("data-theme", theme);
    try {
      localStorage.setItem("theme", theme);
    } catch (e) {
      // localStorage may be unavailable (private mode, disabled storage).
      // The choice still applies for this page load — silently OK.
    }
  }

  document.addEventListener("click", function (e) {
    var btn = e.target.closest && e.target.closest(".theme-toggle");
    if (!btn) return;
    var current = currentEffectiveTheme();
    applyTheme(current === "dark" ? "light" : "dark");
  });
})();
