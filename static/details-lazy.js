// Lazily materializes form content for collapsible <details data-lazy> blocks.
//
// Why: keeping API-key inputs inside the live DOM (even inside collapsed
// <details>) triggers Chrome's "sensitive form not secure" warning on every
// form submission across the page when served over plain HTTP.
//
// Template content is inert — the browser parses but does not activate it,
// so credential heuristics ignore it. On toggle we clone the <template>
// into the details and remove it again on close.

(function () {
  function activate(details) {
    if (details.dataset.lazyMounted === "1") return;
    var tpl = details.querySelector(":scope > template");
    if (!tpl) return;
    var frag = tpl.content.cloneNode(true);
    details.appendChild(frag);
    details.dataset.lazyMounted = "1";
  }

  function deactivate(details) {
    if (details.dataset.lazyMounted !== "1") return;
    details.querySelectorAll(":scope > :not(summary):not(template)").forEach(function (n) {
      n.remove();
    });
    details.dataset.lazyMounted = "0";
  }

  function init() {
    document.querySelectorAll("details[data-lazy]").forEach(function (d) {
      d.addEventListener("toggle", function () {
        if (d.open) activate(d);
        else deactivate(d);
      });
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
