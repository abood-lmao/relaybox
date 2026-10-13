package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	apidocs "github.com/abood-lmao/relaybox/api"
	"github.com/abood-lmao/relaybox/internal/auth"
	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/config"
	"github.com/abood-lmao/relaybox/internal/events"
	"github.com/abood-lmao/relaybox/internal/replay"
	"github.com/abood-lmao/relaybox/internal/retention"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
)

const testToken = "test-token-0123456789abcdefghij"

type harness struct {
	t      *testing.T
	cfg    config.Config
	db     *storage.DB
	api    *API
	h      http.Handler
	cap    *capture.Service
	broker *events.Broker
}

func testConfig() config.Config {
	return config.Config{
		Host: "127.0.0.1", Port: 8787, PublicBaseURL: "http://127.0.0.1:8787",
		MaxBodyBytes: 1 << 20, RetentionDays: 7, MaxStoredRequests: 10000,
		LogFormat: "text",
	}
}

func newHarness(t *testing.T, mods ...func(*config.Config)) *harness {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return harnessWithDB(t, db, mods...)
}

func harnessWithDB(t *testing.T, db *storage.DB, mods ...func(*config.Config)) *harness {
	t.Helper()
	cfg := testConfig()
	for _, m := range mods {
		m(&cfg)
	}
	ctx := context.Background()
	set, _, err := settings.New(ctx, db, settings.DefaultsFromConfig(cfg), cfg.MaxBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	broker := events.New(3, 100)
	t.Cleanup(broker.Close)
	capSvc := capture.New(capture.Options{DB: db, Settings: set, Events: broker})
	guard := replay.NewGuard(cfg.ReplayAllowedHosts)
	rep := replay.NewService(replay.ServiceOptions{
		DB: db, Capture: capSvc, Guard: guard,
		Sender:     replay.NewSender(guard, "relaybox-test", 5*time.Second),
		URLEnabled: cfg.URLReplayEnabled(),
	})
	a := New(Deps{
		Config: cfg, DB: db, Settings: set, Capture: capSvc, Replay: rep,
		Retention: retention.New(db, set, broker, nil), Events: broker, Auth: auth.New(cfg.DashboardToken),
	})
	return &harness{t: t, cfg: cfg, db: db, api: a, h: a.Handler(), cap: capSvc, broker: broker}
}

// do sends a request to the API. A non-nil body that is not a string or
// []byte is encoded as JSON.
func (h *harness) do(method, target string, body any, headers ...string) *httptest.ResponseRecorder {
	h.t.Helper()
	var rd io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case string:
		rd, contentType = strings.NewReader(b), "application/json"
	case []byte:
		rd, contentType = bytes.NewReader(b), "application/json"
	default:
		data, err := json.Marshal(b)
		if err != nil {
			h.t.Fatal(err)
		}
		rd, contentType = bytes.NewReader(data), "application/json"
	}
	r := httptest.NewRequest(method, target, rd)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func expectStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, want, w.Body.String())
	}
}

type apiError struct {
	Error errorDetail `json:"error"`
}

func expectError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) errorDetail {
	t.Helper()
	expectStatus(t, w, status)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("error content type = %q", ct)
	}
	e := decode[apiError](t, w).Error
	if e.Code != code {
		t.Fatalf("error code = %q, want %q (%s)", e.Code, code, e.Message)
	}
	return e
}

func (h *harness) createEndpoint(name string, extra map[string]any) EndpointView {
	h.t.Helper()
	body := map[string]any{"name": name}
	for k, v := range extra {
		body[k] = v
	}
	w := h.do("POST", "/api/v1/endpoints", body)
	expectStatus(h.t, w, http.StatusCreated)
	return decode[EndpointView](h.t, w)
}

// hook sends a request to an endpoint's capture URL and returns the response.
func (h *harness) hook(ep EndpointView, method, suffix, body string, headers ...string) *httptest.ResponseRecorder {
	h.t.Helper()
	r := httptest.NewRequest(method, "/hooks/"+ep.PublicID+suffix, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.cap.ServeHTTP(w, r)
	return w
}

func (h *harness) latestRequestID(ep EndpointView) int64 {
	h.t.Helper()
	ids, err := h.db.LatestRequestIDs(context.Background(), ep.ID, 1)
	if err != nil || len(ids) == 0 {
		h.t.Fatalf("no request stored: %v", err)
	}
	return ids[0]
}

func TestHealthChecks(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/healthz", nil)
	expectStatus(t, w, 200)
	if got := decode[map[string]string](t, w)["status"]; got != "ok" {
		t.Errorf("healthz status = %q", got)
	}
	w = h.do("GET", "/readyz", nil)
	expectStatus(t, w, 200)
	if got := decode[map[string]string](t, w)["database"]; got != "ok" {
		t.Errorf("readyz database = %q", got)
	}

	h.db.Close()
	w = h.do("GET", "/readyz", nil)
	expectStatus(t, w, http.StatusServiceUnavailable)
}

func TestRouterErrorsAreJSON(t *testing.T) {
	h := newHarness(t)
	expectError(t, h.do("GET", "/api/v1/nope", nil), 404, "not_found")
	expectError(t, h.do("GET", "/api/v2/endpoints", nil), 404, "not_found")
	w := h.do("PUT", "/api/v1/endpoints", "{}")
	e := expectError(t, w, 405, "method_not_allowed")
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Errorf("Allow = %q", allow)
	}
	if !strings.Contains(e.Message, "GET") {
		t.Errorf("message = %q", e.Message)
	}
	expectError(t, h.do("GET", "/api/v1/endpoints/abc", nil), 400, "invalid_id")
	expectError(t, h.do("GET", "/api/v1/endpoints/-1", nil), 400, "invalid_id")
}

func TestJSONBodyRules(t *testing.T) {
	h := newHarness(t)
	r := httptest.NewRequest("POST", "/api/v1/endpoints", strings.NewReader(`{"name":"a"}`))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	expectError(t, w, http.StatusUnsupportedMediaType, "unsupported_media_type")

	r = httptest.NewRequest("POST", "/api/v1/endpoints", strings.NewReader(`{"name":"a"}`))
	w = httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	expectError(t, w, http.StatusUnsupportedMediaType, "unsupported_media_type")

	expectError(t, h.do("POST", "/api/v1/endpoints", `{"name":"a","admin":true}`), 400, "invalid_json")
	expectError(t, h.do("POST", "/api/v1/endpoints", `{"name":"a"} {"name":"b"}`), 400, "invalid_json")
	expectError(t, h.do("POST", "/api/v1/endpoints", `{"name":`), 400, "invalid_json")
	expectError(t, h.do("POST", "/api/v1/endpoints", `{"name":5}`), 400, "invalid_json")
	expectError(t, h.do("POST", "/api/v1/endpoints", ``), 400, "invalid_json")
	huge := `{"name":"` + strings.Repeat("a", maxJSONBody) + `"}`
	expectError(t, h.do("POST", "/api/v1/endpoints", huge), 413, "payload_too_large")

	w = h.do("POST", "/api/v1/endpoints", "{\"name\":\"ok\"}\n")
	expectStatus(t, w, 201)
}

func TestEndpointLifecycle(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("  Stripe test  ", nil)
	if ep.Name != "Stripe test" || !ep.Enabled {
		t.Fatalf("created %+v", ep)
	}
	if ep.URL != "http://127.0.0.1:8787/hooks/"+ep.PublicID || len(ep.PublicID) != 26 {
		t.Errorf("url = %q", ep.URL)
	}
	if ep.MockResponse.Status != 200 || ep.EffectiveMaxBodyBytes != 1<<20 || ep.MaxBodyBytes != nil {
		t.Errorf("defaults: %+v", ep)
	}

	e := expectError(t, h.do("POST", "/api/v1/endpoints", map[string]any{"name": "STRIPE TEST"}), 422, "validation_failed")
	if e.Fields["name"] == "" {
		t.Errorf("duplicate name fields = %v", e.Fields)
	}
	e = expectError(t, h.do("POST", "/api/v1/endpoints", map[string]any{"name": "   "}), 422, "validation_failed")
	if e.Fields["name"] == "" {
		t.Errorf("empty name fields = %v", e.Fields)
	}
	expectError(t, h.do("POST", "/api/v1/endpoints", map[string]any{"name": "bad\nname"}), 422, "validation_failed")
	expectError(t, h.do("POST", "/api/v1/endpoints", map[string]any{"name": strings.Repeat("é", 81)}), 422, "validation_failed")

	e = expectError(t, h.do("POST", "/api/v1/endpoints", map[string]any{
		"name":           "bad mock",
		"mock_response":  map[string]any{"status": 99, "headers": map[string]string{"Set-Cookie": "a=b"}},
		"max_body_bytes": 10,
	}), 422, "validation_failed")
	for _, f := range []string{"mock_response.status", "mock_response.headers", "max_body_bytes"} {
		if e.Fields[f] == "" {
			t.Errorf("missing field error %s in %v", f, e.Fields)
		}
	}

	w := h.do("GET", "/api/v1/endpoints", nil)
	expectStatus(t, w, 200)
	list := decode[struct{ Items []EndpointView }](t, w)
	if len(list.Items) != 1 || list.Items[0].ID != ep.ID {
		t.Fatalf("list = %+v", list)
	}

	path := "/api/v1/endpoints/" + itoa(ep.ID)
	w = h.do("PATCH", path, map[string]any{
		"name": "Renamed", "enabled": false, "max_body_bytes": 2048,
		"mock_response": map[string]any{"status": 202, "content_type": "text/plain", "body": "queued", "headers": map[string]string{"X-Mock": "1"}},
	})
	expectStatus(t, w, 200)
	up := decode[EndpointView](t, w)
	if up.Name != "Renamed" || up.Enabled || up.EffectiveMaxBodyBytes != 2048 || up.MockResponse.Status != 202 || up.MockResponse.Headers["X-Mock"] != "1" {
		t.Fatalf("updated %+v", up)
	}
	w = h.do("PATCH", path, map[string]any{"enabled": true})
	expectStatus(t, w, 200)
	if got := decode[EndpointView](t, w); got.MaxBodyBytes == nil || *got.MaxBodyBytes != 2048 || !got.Enabled {
		t.Fatalf("absent max_body_bytes changed it: %+v", got)
	}
	w = h.do("PATCH", path, `{"max_body_bytes":null}`)
	expectStatus(t, w, 200)
	if got := decode[EndpointView](t, w); got.MaxBodyBytes != nil || got.EffectiveMaxBodyBytes != 1<<20 {
		t.Fatalf("null max_body_bytes: %+v", got)
	}
	expectError(t, h.do("PATCH", path, `{"max_body_bytes":"big"}`), 422, "validation_failed")
	expectError(t, h.do("PATCH", "/api/v1/endpoints/999", map[string]any{"enabled": true}), 404, "not_found")

	if w := h.hook(ep, "POST", "", "{}"); w.Code != 202 || w.Body.String() != "queued" {
		t.Fatalf("hook = %d %q", w.Code, w.Body.String())
	}
	oldID := ep.PublicID
	w = h.do("POST", path+"/rotate-url", nil)
	expectStatus(t, w, 200)
	rotated := decode[EndpointView](t, w)
	if rotated.PublicID == oldID || !strings.HasSuffix(rotated.URL, rotated.PublicID) {
		t.Fatalf("rotate: %+v", rotated)
	}
	if w := h.hook(ep, "POST", "", "{}"); w.Code != 404 {
		t.Errorf("old URL still works: %d", w.Code)
	}
	if w := h.hook(rotated, "POST", "", "{}"); w.Code != 202 {
		t.Errorf("new URL = %d", w.Code)
	}

	w = h.do("GET", path, nil)
	expectStatus(t, w, 200)
	if got := decode[EndpointView](t, w); got.RequestCount != 2 || got.LastRequestAt == nil {
		t.Errorf("count after captures: %+v", got)
	}

	w = h.do("DELETE", path, nil)
	expectStatus(t, w, 200)
	if got := decode[map[string]int](t, w)["deleted_requests"]; got != 2 {
		t.Errorf("deleted_requests = %d", got)
	}
	expectError(t, h.do("GET", path, nil), 404, "not_found")
	expectError(t, h.do("DELETE", path, nil), 404, "not_found")
}

func TestMockPreview(t *testing.T) {
	h := newHarness(t)
	w := h.do("POST", "/api/v1/mock-preview", map[string]any{
		"mock_response": map[string]any{"status": 201, "content_type": "application/json", "body": `{"ok":true}`},
	})
	expectStatus(t, w, 200)
	got := decode[ResponseView](t, w)
	if got.Status != 201 || got.StatusText != "Created" || got.Body.Data != `{"ok":true}` || got.Body.Kind != KindJSON {
		t.Fatalf("preview = %+v", got)
	}
	if !slices.ContainsFunc(got.Headers, func(p [2]string) bool { return p[0] == "Content-Security-Policy" }) {
		t.Errorf("preview should include server headers: %v", got.Headers)
	}

	w = h.do("POST", "/api/v1/mock-preview", map[string]any{"mock_response": map[string]any{"status": 200}, "method": "HEAD"})
	expectStatus(t, w, 200)
	if got := decode[ResponseView](t, w); got.Body.Data != "" {
		t.Errorf("HEAD preview has a body: %q", got.Body.Data)
	}
	e := expectError(t, h.do("POST", "/api/v1/mock-preview", map[string]any{"mock_response": map[string]any{"status": 200}, "method": "TRACE"}), 422, "validation_failed")
	if e.Fields["method"] == "" {
		t.Errorf("fields = %v", e.Fields)
	}
	e = expectError(t, h.do("POST", "/api/v1/mock-preview", map[string]any{
		"mock_response": map[string]any{"status": 200, "content_type": "application/json", "body": "{bad"},
	}), 422, "validation_failed")
	if e.Fields["mock_response.body"] == "" {
		t.Errorf("fields = %v", e.Fields)
	}
}

func TestMetaAndStats(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/api/v1/meta", nil)
	expectStatus(t, w, 200)
	meta := decode[map[string]any](t, w)
	if meta["name"] != "Relaybox" || meta["hook_url_prefix"] != "http://127.0.0.1:8787/hooks/" ||
		meta["auth_required"] != false || meta["url_replay_enabled"] != true {
		t.Fatalf("meta = %v", meta)
	}

	ep := h.createEndpoint("one", nil)
	h.hook(ep, "POST", "", `{"a":1}`)
	h.hook(ep, "GET", "/x", "")
	w = h.do("GET", "/api/v1/stats", nil)
	expectStatus(t, w, 200)
	type statsView struct {
		TotalRequests int64            `json:"total_requests"`
		Last24h       int64            `json:"last_24h"`
		Endpoints     int64            `json:"endpoints"`
		StatusClasses map[string]int64 `json:"status_classes"`
		Hourly        []struct{ Count int64 }
		TopEndpoints  []struct{ Name string } `json:"top_endpoints"`
		Recent        []RequestSummaryView
	}
	stats := decode[statsView](t, w)
	if stats.TotalRequests != 2 || stats.Last24h != 2 || stats.Endpoints != 1 || stats.StatusClasses["2xx"] != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(stats.Hourly) != 24 || len(stats.TopEndpoints) != 1 || len(stats.Recent) != 2 || stats.Recent[0].Method != "GET" {
		t.Fatalf("stats lists = %+v", stats)
	}
}

func TestInternalErrorsAreGeneric(t *testing.T) {
	h := newHarness(t)
	h.db.Close()
	e := expectError(t, h.do("GET", "/api/v1/endpoints", nil), 500, "internal_error")
	if strings.Contains(strings.ToLower(e.Message), "sql") || strings.Contains(e.Message, "database is closed") {
		t.Errorf("internal detail leaked: %q", e.Message)
	}
}

// TestOpenAPICoversRoutes keeps api/openapi.yaml in step with the router.
func TestOpenAPICoversRoutes(t *testing.T) {
	h := newHarness(t)
	documented := map[string]bool{}
	pathRe := regexp.MustCompile(`^  (/\S*):\s*$`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete|head|options):`)
	current := ""
	sc := bufio.NewScanner(bytes.NewReader(apidocs.OpenAPI))
	for sc.Scan() {
		line := sc.Text()
		if line == "components:" {
			break
		}
		if m := pathRe.FindStringSubmatch(line); m != nil {
			current = m[1]
		} else if m := methodRe.FindStringSubmatch(line); m != nil && current != "" {
			documented[strings.ToUpper(m[1])+" "+current] = true
		}
	}
	for _, route := range h.api.routes {
		if !documented[route] {
			t.Errorf("route %q is not documented in api/openapi.yaml", route)
		}
		delete(documented, route)
	}
	for route := range documented {
		if !strings.Contains(route, " /hooks/") {
			t.Errorf("api/openapi.yaml documents %q, which is not a route", route)
		}
	}

	w := h.do("GET", "/api/v1/openapi.yaml", nil)
	expectStatus(t, w, 200)
	if !strings.HasPrefix(w.Body.String(), "openapi: 3.1.0") {
		t.Errorf("openapi body starts with %q", w.Body.String()[:20])
	}
	var schema map[string]any
	if err := json.Unmarshal(apidocs.FixtureSchema, &schema); err != nil {
		t.Fatalf("fixture schema is not valid JSON: %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
