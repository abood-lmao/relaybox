package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/abood-lmao/relaybox/internal/auth"
	"github.com/abood-lmao/relaybox/internal/config"
)

const token = "server-test-token-0123456789abcdef"

func baseConfig(t *testing.T) config.Config {
	return config.Config{
		Host: "127.0.0.1", Port: 8787, PublicBaseURL: "http://127.0.0.1:8787",
		DatabasePath: filepath.Join(t.TempDir(), "relaybox.db"),
		MaxBodyBytes: 1 << 20, RetentionDays: 7, MaxStoredRequests: 10000,
		LogFormat: "text", RateLimitRPS: 0, RateLimitBurst: 1,
	}
}

func remote(c *config.Config) {
	c.Host = "0.0.0.0"
	c.AllowRemoteDashboard = true
	c.DashboardToken = token
}

var uiFS = fstest.MapFS{
	"index.html":        {Data: []byte("<!doctype html><title>Relaybox</title><div id=root></div>")},
	"assets/app-1.js":   {Data: []byte("export {}")},
	"assets/app-1.css":  {Data: []byte("body{}")},
	"theme-init.js":     {Data: []byte("1")},
	"favicon.svg":       {Data: []byte("<svg/>")},
	"fonts/plex.woff2":  {Data: []byte("font")},
	"assets/plex.woff2": {Data: []byte("font")},
}

func openApp(t *testing.T, cfg config.Config) *App {
	t.Helper()
	app, err := Open(context.Background(), cfg, nil, uiFS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app
}

func newApp(t *testing.T, mods ...func(*config.Config)) *App {
	t.Helper()
	cfg := baseConfig(t)
	for _, m := range mods {
		m(&cfg)
	}
	return openApp(t, cfg)
}

type req struct {
	method, target, body string
	remote, host         string
	headers              map[string]string
}

// noHost sends a request without a Host header.
const noHost = "<none>"

func call(t *testing.T, h http.Handler, r req) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	method := r.method
	if method == "" {
		method = "GET"
	}
	hr := httptest.NewRequest(method, r.target, body)
	hr.RemoteAddr = "127.0.0.1:50000"
	if r.remote != "" {
		hr.RemoteAddr = r.remote
	}
	hr.Host = "127.0.0.1:8787"
	switch r.host {
	case "":
	case noHost:
		hr.Host = ""
	default:
		hr.Host = r.host
	}
	if r.body != "" {
		hr.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, hr)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("not a JSON error: %q", w.Body.String())
	}
	return e.Error.Code
}

type endpoint struct {
	ID       int64  `json:"id"`
	PublicID string `json:"public_id"`
	URL      string `json:"url"`
}

func createEndpoint(t *testing.T, h http.Handler, name string, headers map[string]string) endpoint {
	t.Helper()
	w := call(t, h, req{method: "POST", target: "/api/v1/endpoints", body: `{"name":"` + name + `"}`, headers: headers})
	if w.Code != 201 {
		t.Fatalf("create endpoint: %d %s", w.Code, w.Body.String())
	}
	var ep endpoint
	_ = json.Unmarshal(w.Body.Bytes(), &ep)
	return ep
}

func serve(t *testing.T, app *App) (baseURL string, stop func() error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, ln) }()
	var once sync.Once
	var result error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case result = <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("server did not stop")
			}
		})
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return "http://" + ln.Addr().String(), stop
}

func TestEndToEnd(t *testing.T) {
	app := newApp(t)
	base, stop := serve(t, app)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Post(base+"/api/v1/endpoints", "application/json", strings.NewReader(`{"name":"e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	var ep endpoint
	_ = json.NewDecoder(resp.Body).Decode(&ep)
	resp.Body.Close()
	if resp.StatusCode != 201 || ep.PublicID == "" {
		t.Fatalf("create: %d %+v", resp.StatusCode, ep)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sreq, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/v1/events", nil)
	stream, err := http.DefaultClient.Do(sreq)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	rd := bufio.NewReader(stream.Body)
	if line, _ := rd.ReadString('\n'); line != "retry: 3000\n" {
		t.Fatalf("stream start = %q", line)
	}

	resp, err = client.Post(base+"/hooks/"+ep.PublicID+"/payments?attempt=1", "application/json", strings.NewReader(`{"amount":42}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != `{"received":true}` {
		t.Fatalf("hook: %d %q", resp.StatusCode, body)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox") {
		t.Errorf("hook CSP = %q", csp)
	}

	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if line == "event: request.created\n" {
			break
		}
	}

	resp, err = client.Get(base + "/api/v1/requests?endpoint_id=" + itoa(ep.ID))
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Total int64 `json:"total"`
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&page)
	resp.Body.Close()
	if page.Total != 1 || page.Items[0].Path != "/payments" {
		t.Fatalf("list = %+v", page)
	}

	resp, err = client.Get(base + "/requests/1")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(html), "<title>Relaybox</title>") {
		t.Fatalf("SPA route: %d %q", resp.StatusCode, html)
	}

	// Shutting down closes the open event stream instead of waiting for it.
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("shutdown took %s", d)
	}
}

func TestDataSurvivesRestart(t *testing.T) {
	cfg := baseConfig(t)
	app, err := Open(context.Background(), cfg, nil, uiFS)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	ep := createEndpoint(t, h, "durable", nil)
	if w := call(t, h, req{method: "POST", target: "/hooks/" + ep.PublicID, body: `{"n":1}`}); w.Code != 200 {
		t.Fatalf("hook: %d", w.Code)
	}
	if w := call(t, h, req{method: "PATCH", target: "/api/v1/settings", body: `{"retention_days":30}`}); w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	app2 := openApp(t, cfg)
	h2 := app2.Handler()
	w := call(t, h2, req{target: "/api/v1/requests"})
	if !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("requests after restart: %s", w.Body.String())
	}
	w = call(t, h2, req{target: "/api/v1/settings"})
	if !strings.Contains(w.Body.String(), `"retention_days":30`) {
		t.Fatalf("settings after restart: %s", w.Body.String())
	}
	if w := call(t, h2, req{method: "POST", target: "/hooks/" + ep.PublicID, body: "{}"}); w.Code != 200 {
		t.Fatalf("endpoint URL changed across restart: %d", w.Code)
	}
}

func TestLocalModeGuard(t *testing.T) {
	app := newApp(t)
	h := app.Handler()
	ep := createEndpoint(t, h, "guarded", nil)

	denied := []req{
		{target: "/api/v1/endpoints", remote: "192.0.2.10:4000"},
		{target: "/api/v1/endpoints", remote: "[2001:db8::1]:4000"},
		{target: "/api/v1/endpoints", host: "attacker.example"},
		{target: "/api/v1/endpoints", host: "attacker.example:8787"},
		{target: "/api/v1/endpoints", host: "127.0.0.1.nip.io:8787"},
		{target: "/api/v1/endpoints", host: noHost},
		{target: "/api/v1/endpoints", headers: map[string]string{"X-Forwarded-For": "203.0.113.9"}},
		{target: "/api/v1/endpoints", headers: map[string]string{"Forwarded": "for=203.0.113.9"}},
		{target: "/api/v1/endpoints", headers: map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}},
		{method: "POST", target: "/api/v1/endpoints", body: `{"name":"x"}`, host: "attacker.example"},
	}
	for _, r := range denied {
		w := call(t, h, r)
		if w.Code != 403 || errorCode(t, w) != "local_only" {
			t.Errorf("%+v: %d %s", r, w.Code, w.Body.String())
		}
	}
	if w := call(t, h, req{target: "/", remote: "192.0.2.10:4000"}); w.Code != 403 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("UI from remote: %d %v", w.Code, w.Header())
	}

	for _, host := range []string{"127.0.0.1:8787", "localhost:8787", "LOCALHOST", "[::1]:8787", "127.0.0.2:8787"} {
		if w := call(t, h, req{target: "/api/v1/endpoints", host: host}); w.Code != 200 {
			t.Errorf("host %s: %d", host, w.Code)
		}
	}
	if w := call(t, h, req{target: "/api/v1/endpoints", remote: "[::1]:5000", host: "[::1]:8787"}); w.Code != 200 {
		t.Errorf("IPv6 loopback: %d", w.Code)
	}

	// Capture endpoints and health checks are public.
	if w := call(t, h, req{method: "POST", target: "/hooks/" + ep.PublicID, body: "{}", remote: "198.51.100.7:1234", host: "hooks.example.com",
		headers: map[string]string{"X-Forwarded-For": "203.0.113.1"}}); w.Code != 200 {
		t.Errorf("remote hook: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, req{target: "/healthz", remote: "198.51.100.7:1234", host: "x"}); w.Code != 200 {
		t.Errorf("remote healthz: %d", w.Code)
	}
	if w := call(t, h, req{target: "/hooks/../api/v1/endpoints", remote: "198.51.100.7:1234"}); w.Code == 200 {
		t.Errorf("path tricks reached the API: %d %s", w.Code, w.Body.String())
	}
}

func TestRemoteDashboardRequiresToken(t *testing.T) {
	app := newApp(t, remote)
	h := app.Handler()
	far := "198.51.100.20:3333"

	w := call(t, h, req{target: "/api/v1/endpoints", remote: far, host: "relaybox.example.com"})
	if w.Code != 401 || errorCode(t, w) != "unauthorized" || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("no auth: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	w = call(t, h, req{target: "/api/v1/endpoints", remote: "127.0.0.1:1", host: "localhost"})
	if w.Code != 401 {
		t.Errorf("token must be required from loopback too: %d", w.Code)
	}
	bearer := map[string]string{"Authorization": "Bearer " + token}
	if w := call(t, h, req{target: "/api/v1/endpoints", remote: far, host: "relaybox.example.com", headers: bearer}); w.Code != 200 {
		t.Errorf("bearer: %d", w.Code)
	}
	if w := call(t, h, req{target: "/api/v1/endpoints", remote: far, headers: map[string]string{"Authorization": "Bearer nope"}}); w.Code != 401 {
		t.Errorf("wrong bearer: %d", w.Code)
	}
	for _, open := range []string{"/", "/requests/5", "/assets/app-1.js", "/api/v1/auth/session", "/healthz"} {
		if w := call(t, h, req{target: open, remote: far, host: "relaybox.example.com"}); w.Code != 200 {
			t.Errorf("%s without auth: %d", open, w.Code)
		}
	}
	if w := call(t, h, req{target: "/api/v1/stats", remote: far}); w.Code != 401 {
		t.Errorf("stats without auth: %d", w.Code)
	}
	if w := call(t, h, req{target: "/api/v1/events", remote: far}); w.Code != 401 {
		t.Errorf("events without auth: %d", w.Code)
	}

	w = call(t, h, req{method: "POST", target: "/api/v1/auth/login", body: `{"token":"` + token + `"}`, remote: far, host: "relaybox.example.com"})
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var session string
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			session = c.Value
		}
	}
	cookie := map[string]string{"Cookie": auth.CookieName + "=" + session}
	if w := call(t, h, req{target: "/api/v1/endpoints", remote: far, host: "relaybox.example.com", headers: cookie}); w.Code != 200 {
		t.Errorf("cookie: %d", w.Code)
	}

	ep := createEndpoint(t, h, "remote", bearer)
	if w := call(t, h, req{method: "POST", target: "/hooks/" + ep.PublicID, body: "{}", remote: far}); w.Code != 200 {
		t.Errorf("hooks must not need the token: %d", w.Code)
	}
	if !strings.HasPrefix(ep.URL, "http://127.0.0.1:8787/hooks/") {
		t.Errorf("endpoint url = %q", ep.URL)
	}
}

func TestCapturedRequestsNeverStoreDashboardCredentials(t *testing.T) {
	app := newApp(t, remote)
	h := app.Handler()
	bearer := map[string]string{"Authorization": "Bearer " + token}
	ep := createEndpoint(t, h, "creds", bearer)

	w := call(t, h, req{method: "POST", target: "/hooks/" + ep.PublicID + "?t=" + token, body: `{"note":"` + token + `"}`,
		headers: map[string]string{"Cookie": auth.CookieName + "=abc.def; theme=dark", "X-Token": token}})
	if w.Code != 200 {
		t.Fatalf("hook: %d", w.Code)
	}
	w = call(t, h, req{target: "/api/v1/requests/1", headers: bearer})
	if w.Code != 200 {
		t.Fatalf("detail: %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, token) || strings.Contains(body, "abc.def") || strings.Contains(body, auth.CookieName) {
		t.Fatalf("dashboard credentials were stored: %s", body)
	}
	if !strings.Contains(body, `["Cookie","theme=`) {
		t.Errorf("other cookies should be kept (with redacted values): %s", body)
	}
}

func TestCrossOriginProtection(t *testing.T) {
	app := newApp(t)
	h := app.Handler()

	w := call(t, h, req{method: "POST", target: "/api/v1/endpoints", body: `{"name":"csrf"}`, headers: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	if w.Code != 403 || errorCode(t, w) != "cross_origin_rejected" {
		t.Fatalf("cross-site POST: %d %s", w.Code, w.Body.String())
	}
	w = call(t, h, req{method: "DELETE", target: "/api/v1/requests?scope=all", headers: map[string]string{"Origin": "http://attacker.example"}})
	if w.Code != 403 {
		t.Fatalf("cross-origin DELETE: %d %s", w.Code, w.Body.String())
	}
	w = call(t, h, req{method: "POST", target: "/api/v1/endpoints", body: `{"name":"same"}`, headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://127.0.0.1:8787"}})
	if w.Code != 201 {
		t.Fatalf("same-origin POST: %d %s", w.Code, w.Body.String())
	}
	var ep endpoint
	_ = json.Unmarshal(w.Body.Bytes(), &ep)

	// Webhook senders may be browsers on other sites; capture accepts them.
	w = call(t, h, req{method: "POST", target: "/hooks/" + ep.PublicID, body: "{}", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://example.org"}})
	if w.Code != 200 {
		t.Errorf("cross-site hook: %d", w.Code)
	}
	w = call(t, h, req{target: "/api/v1/endpoints", headers: map[string]string{"Origin": "https://attacker.example"}})
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("the API must not send CORS headers")
	}
}

func TestSecurityHeaders(t *testing.T) {
	app := newApp(t)
	h := app.Handler()
	for _, target := range []string{"/", "/api/v1/endpoints", "/assets/app-1.js"} {
		w := call(t, h, req{target: target})
		hd := w.Header()
		if hd.Get("Content-Security-Policy") != dashboardCSP || hd.Get("X-Content-Type-Options") != "nosniff" ||
			hd.Get("X-Frame-Options") != "DENY" || hd.Get("Referrer-Policy") != "no-referrer" ||
			hd.Get("Cross-Origin-Opener-Policy") != "same-origin" {
			t.Errorf("%s headers = %v", target, hd)
		}
	}
	if !strings.Contains(dashboardCSP, "script-src 'self'") || strings.Contains(dashboardCSP, "unsafe-inline") || strings.Contains(dashboardCSP, "unsafe-eval") {
		t.Errorf("CSP = %q", dashboardCSP)
	}
}

func TestCapturedMarkupIsInert(t *testing.T) {
	app := newApp(t)
	h := app.Handler()
	w := call(t, h, req{method: "POST", target: "/api/v1/endpoints",
		body: `{"name":"html","mock_response":{"status":200,"content_type":"text/html","body":"<script>alert(document.domain)</script>"}}`})
	var ep endpoint
	_ = json.Unmarshal(w.Body.Bytes(), &ep)

	w = call(t, h, req{target: "/hooks/" + ep.PublicID, headers: map[string]string{"X-Evil": "<img src=x onerror=alert(1)>"}})
	if !strings.HasPrefix(w.Header().Get("Content-Security-Policy"), "sandbox") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("HTML mock response must be sandboxed: %v", w.Header())
	}

	w = call(t, h, req{target: "/api/v1/requests/1"})
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("detail content type = %q", w.Header().Get("Content-Type"))
	}
	if bytes.Contains(w.Body.Bytes(), []byte("<img")) {
		t.Errorf("JSON responses should escape markup: %s", w.Body.String())
	}
	w = call(t, h, req{target: "/api/v1/requests/1/body?part=response"})
	if w.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("body download headers = %v", w.Header())
	}
}

func TestPanicsBecome500(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	h := observe(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/x", nil))
	if w.Code != 500 || errorCode(t, w) != "internal_error" || strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("panic response: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(logs.String(), "boom") {
		t.Errorf("panic not logged: %s", logs.String())
	}
}

func TestRequestLogsHideSecrets(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := baseConfig(t)
	app, err := Open(context.Background(), cfg, log, uiFS)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	h := app.Handler()
	ep := createEndpoint(t, h, "logs", nil)
	call(t, h, req{method: "POST", target: "/hooks/" + ep.PublicID + "/x?api_key=supersecret", body: `{"password":"hunter2"}`})
	out := logs.String()
	for _, secret := range []string{ep.PublicID, "supersecret", "hunter2"} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "/hooks/"+ep.PublicID[:4]+".../x") {
		t.Errorf("expected a masked hook path in the log:\n%s", out)
	}
}

func TestMethodNotAllowedThroughServer(t *testing.T) {
	app := newApp(t)
	h := app.Handler()
	w := call(t, h, req{method: "PUT", target: "/api/v1/settings", body: "{}"})
	if w.Code != 405 || errorCode(t, w) != "method_not_allowed" {
		t.Fatalf("PUT settings: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, req{method: "POST", target: "/healthz"}); w.Code != 405 {
		t.Errorf("POST healthz: %d", w.Code)
	}
}

func TestOpenFailsOnUnwritableDatabase(t *testing.T) {
	cfg := baseConfig(t)
	cfg.DatabasePath = t.TempDir() // a directory, not a file
	if _, err := Open(context.Background(), cfg, nil, nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestMaskPath(t *testing.T) {
	cases := map[string]string{
		"/hooks/abcdefghijklmnopqrstuvwxyz":     "/hooks/abcd...",
		"/hooks/abcdefghijklmnopqrstuvwxyz/a/b": "/hooks/abcd.../a/b",
		"/hooks/ab":                             "/hooks/ab",
		"/api/v1/requests":                      "/api/v1/requests",
	}
	for in, want := range cases {
		if got := maskPath(in); got != want {
			t.Errorf("maskPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
