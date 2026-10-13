// Applies the saved theme before the first paint. Loaded as a file rather
// than an inline script so the dashboard's Content-Security-Policy can
// forbid inline scripts.
(function () {
  var theme = "system";
  try {
    theme = localStorage.getItem("relaybox-theme") || "system";
  } catch {
    // Storage can be unavailable; fall back to the system preference.
  }
  var dark =
    theme === "dark" || (theme !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
})();
