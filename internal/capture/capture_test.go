package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/ratelimit"
	"github.com/abood-lmao/relaybox/internal/redact"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
)

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) Publish(eventType string, _ any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, eventType)
}

type fixture struct {
	db       *storage.DB
	settings *settings.Service
	svc      *Service
	events   *recorder
}

func newFixture(t *testing.T, mutate func(*Options, *settings.Values)) *fixture {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	defaults := settings.Values{
		RetentionDays:       7,
		MaxStoredRequests:   1000,
		DefaultMaxBodyBytes: 1024,
		RedactHeaders:       redact.DefaultHeaders,
		RedactFields:        redact.DefaultFields,
	}
	events := &recorder{}
	opts := Options{DB: db, Events: events}
	if mutate != nil {
		mutate(&opts, &defaults)
	}
	set, _, err := settings.New(context.Background(), db, defaults, 4096)
	if err != nil {
		t.Fatal(err)
	}
	opts.Settings = set
	return &fixture{db: db, settings: set, svc: New(opts), events: events}
}

func (f *fixture) endpoint(t *testing.T, mutate func(*storage.NewEndpoint)) storage.Endpoint {
	t.Helper()
	in := storage.NewEndpoint{Name: "test", Enabled: true, Mock: mockresp.Default()}
	if mutate != nil {
		mutate(&in)
	}
	ep, err := f.db.CreateEndpoint(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

func (f *fixture) send(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.svc.ServeHTTP(w, r)
	return w
}

func (f *fixture) only(t *testing.T) storage.CapturedRequest {
	t.Helper()
	list, total, err := f.db.ListRequests(context.Background(), storage.RequestFilter{})
	if err != nil || total != 1 {
		t.Fatalf("expected exactly one stored request, got %d (%v)", total, err)
	}
	req, err := f.db.GetRequest(context.Background(), list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func (f *fixture) count(t *testing.T) int64 {
	t.Helper()
	n, err := f.db.CountRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCaptureStoresRequestAndReturnsMock(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)

	body := `{"event":"push","password":"hunter2"}`
	r := httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID+"?delivery=42&token=abc", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer s3cr3t")
	r.Header.Set("X-GitHub-Event", "push")
	w := f.send(r)

	if w.Code != http.StatusOK || w.Body.String() != mockresp.DefaultBody {
		t.Fatalf("response %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
		t.Error("hook responses must carry the sandbox CSP")
	}

	got := f.only(t)
	if got.Method != "POST" || got.Path != "/" || got.Source != storage.SourceCapture {
		t.Errorf("basic fields: %+v", got)
	}
	if got.Headers["Authorization"][0] != "Bearer [REDACTED]" || got.Headers["X-Github-Event"][0] != "push" {
		t.Errorf("headers: %v", got.Headers)
	}
	if got.Headers["Host"][0] == "" {
		t.Error("Host header should be recorded")
	}
	if got.RawQuery != "delivery=42&token=%5BREDACTED%5D" {
		t.Errorf("raw query = %q", got.RawQuery)
	}
	if string(got.Body) != `{"event":"push","password":"[REDACTED]"}` {
		t.Errorf("body = %s", got.Body)
	}
	if got.BodySize != int64(len(body)) || got.ContentLength == nil || *got.ContentLength != int64(len(body)) {
		t.Errorf("sizes: %d %v", got.BodySize, got.ContentLength)
	}
	if got.ResponseStatus != 200 || string(got.ResponseBody) != mockresp.DefaultBody {
		t.Errorf("response: %d %s", got.ResponseStatus, got.ResponseBody)
	}
	if got.RemoteAddr != "192.0.2.1" {
		t.Errorf("remote addr = %q", got.RemoteAddr)
	}
	if len(got.Redactions) != 3 {
		t.Errorf("redactions = %+v", got.Redactions)
	}
	if len(f.events.events) != 1 || f.events.events[0] != EventRequestCreated {
		t.Errorf("events = %v", f.events.events)
	}
}

func TestSubPathsAreCapturedVerbatim(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)
	w := f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID+"/github//push%20events", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := f.only(t).Path; got != "/github//push%20events" {
		t.Fatalf("path = %q", got)
	}
}

func TestUnknownEndpointsReturn404WithoutStoring(t *testing.T) {
	f := newFixture(t, nil)
	f.endpoint(t, nil)
	for _, path := range []string{
		"/hooks/aaaaaaaaaaaaaaaaaaaaaaaaaa",
		"/hooks/short",
		"/hooks/1",
		"/hooks/AAAAAAAAAAAAAAAAAAAAAAAAAA",
		"/hooks/",
		"/hooks/' OR '1'='1",
	} {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.URL.Path = path
		r.URL.RawPath = ""
		w := f.send(r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "sql") || !strings.Contains(w.Body.String(), "not_found") {
			t.Errorf("%s: body %s", path, w.Body.String())
		}
	}
	if f.count(t) != 0 {
		t.Fatal("nothing should be stored")
	}
}

func TestUnsupportedMethodIsRejected(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)
	w := f.send(httptest.NewRequest("PROPFIND", "/hooks/"+ep.PublicID, nil))
	if w.Code != http.StatusMethodNotAllowed || !strings.Contains(w.Header().Get("Allow"), "PATCH") {
		t.Fatalf("status %d allow %q", w.Code, w.Header().Get("Allow"))
	}
	if f.count(t) != 0 {
		t.Fatal("rejected requests should not be stored")
	}
}

func TestDisabledEndpointRejectsRequests(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, func(e *storage.NewEndpoint) { e.Enabled = false })
	w := f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, strings.NewReader("x")))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "endpoint_disabled") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if f.count(t) != 0 {
		t.Fatal("disabled endpoints must not capture")
	}
}

func TestOversizedBodyGets413AndKeepsPrefix(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil) // default limit 1024 from settings
	big := bytes.Repeat([]byte("a"), 5000)
	w := f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, bytes.NewReader(big)))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", w.Code)
	}
	got := f.only(t)
	if !got.BodyTruncated || len(got.Body) != 1024 || got.ResponseStatus != 413 {
		t.Fatalf("truncated=%v len=%d status=%d", got.BodyTruncated, len(got.Body), got.ResponseStatus)
	}
	if got.ContentLength == nil || *got.ContentLength != 5000 {
		t.Fatalf("content length = %v", got.ContentLength)
	}
}

func TestEndpointBodyLimitOverridesDefaultUpToCeiling(t *testing.T) {
	f := newFixture(t, nil)
	small := int64(10)
	ep := f.endpoint(t, func(e *storage.NewEndpoint) { e.MaxBodyBytes = &small })
	if w := f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, strings.NewReader("12345678901"))); w.Code != 413 {
		t.Fatalf("status %d", w.Code)
	}
	huge := int64(1 << 30)
	ep2 := f.endpoint(t, func(e *storage.NewEndpoint) { e.Name = "huge"; e.MaxBodyBytes = &huge })
	body := bytes.Repeat([]byte("b"), 4097) // ceiling is 4096
	if w := f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep2.PublicID, bytes.NewReader(body))); w.Code != 413 {
		t.Fatalf("ceiling not enforced: status %d", w.Code)
	}
}

func TestHeadAndNoContentResponses(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)
	w := f.send(httptest.NewRequest(http.MethodHead, "/hooks/"+ep.PublicID, nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD: %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Length") != "17" {
		t.Errorf("HEAD Content-Length = %q", w.Header().Get("Content-Length"))
	}

	mock := mockresp.Config{Status: 204, Headers: map[string]string{}}
	if _, err := f.db.UpdateEndpoint(context.Background(), ep.ID, storage.EndpointUpdate{Mock: &mock}); err != nil {
		t.Fatal(err)
	}
	w = f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, strings.NewReader("{}")))
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("204: %d %q", w.Code, w.Body.String())
	}
}

func TestMockChangesApplyImmediately(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)
	mock := mockresp.Config{
		Status:      202,
		ContentType: "text/plain",
		Headers:     map[string]string{"X-Request-Handled": "yes"},
		Body:        "queued",
	}
	if _, err := f.db.UpdateEndpoint(context.Background(), ep.ID, storage.EndpointUpdate{Mock: &mock}); err != nil {
		t.Fatal(err)
	}
	w := f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, nil))
	if w.Code != 202 || w.Body.String() != "queued" || w.Header().Get("X-Request-Handled") != "yes" {
		t.Fatalf("got %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if w.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("content type = %q", w.Header().Get("Content-Type"))
	}
}

func TestRateLimitRejectsWithoutStoring(t *testing.T) {
	f := newFixture(t, func(o *Options, _ *settings.Values) { o.Limiter = ratelimit.New(0.001, 2, 100) })
	ep := f.endpoint(t, nil)
	for i := 0; i < 2; i++ {
		if w := f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, nil)); w.Code != 200 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, nil))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d retry-after %q", w.Code, w.Header().Get("Retry-After"))
	}
	if f.count(t) != 2 {
		t.Fatalf("stored %d requests", f.count(t))
	}
}

// deletingBody removes the endpoint while the body is being read, so the
// insert that follows fails on the foreign key.
type deletingBody struct {
	f  *fixture
	id int64
}

func (d deletingBody) Read([]byte) (int, error) {
	_, _ = d.f.db.DeleteEndpoint(context.Background(), d.id)
	return 0, io.EOF
}

func TestStorageFailureNeverReturnsMockResponse(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)
	r := httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, nil)
	r.Body = io.NopCloser(deletingBody{f: f, id: ep.ID})
	r.ContentLength = -1
	w := f.send(r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "capture_failed") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "FOREIGN") || strings.Contains(w.Body.String(), "constraint") {
		t.Fatal("internal error details leaked")
	}
}

func TestDashboardSecretsAreNeverStored(t *testing.T) {
	const token = "dashboard-token-1234567890abcdef"
	f := newFixture(t, func(o *Options, v *settings.Values) {
		o.Secrets = []string{token}
		o.StripCookies = []string{"relaybox_session"}
		v.RedactHeaders = []string{} // even with header redaction off
	})
	ep := f.endpoint(t, nil)
	r := httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID+"?t="+token, strings.NewReader(`{"note":"`+token+`"}`))
	r.Header.Set("Cookie", "relaybox_session=abc; theme=dark")
	r.Header.Set("X-Forwarded-Token", token)
	f.send(r)

	got := f.only(t)
	if strings.Contains(got.RawQuery, token) || strings.Contains(string(got.Body), token) {
		t.Fatalf("token stored: %q %s", got.RawQuery, got.Body)
	}
	if got.Headers["Cookie"][0] != "theme=dark" {
		t.Fatalf("cookie = %q", got.Headers["Cookie"])
	}
	if got.Headers["X-Forwarded-Token"][0] != redact.Placeholder {
		t.Fatalf("header = %q", got.Headers["X-Forwarded-Token"])
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), token) {
		t.Fatal("token found somewhere in the stored request")
	}
}

func TestConfiguredDelayIsApplied(t *testing.T) {
	f := newFixture(t, nil)
	mock := mockresp.Default()
	mock.DelayMS = 60
	ep := f.endpoint(t, func(e *storage.NewEndpoint) { e.Mock = mock })
	start := time.Now()
	f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, nil))
	if time.Since(start) < 60*time.Millisecond {
		t.Fatal("response came back before the configured delay")
	}
	if f.only(t).Duration < 60*time.Millisecond {
		t.Fatal("recorded duration should include the delay")
	}
}

func TestBinaryInvalidJSONAndEmptyBodies(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)
	cases := []struct {
		ct   string
		body []byte
	}{
		{"application/octet-stream", []byte{0, 0xff, 0xfe, 0x80, '\n'}},
		{"application/json", []byte(`{"broken": `)},
		{"text/plain; charset=utf-8", []byte("hello\r\nworld")},
		{"", nil},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, bytes.NewReader(tc.body))
		if tc.body == nil {
			r = httptest.NewRequest(http.MethodGet, "/hooks/"+ep.PublicID, nil)
		}
		if tc.ct != "" {
			r.Header.Set("Content-Type", tc.ct)
		}
		if w := f.send(r); w.Code != 200 {
			t.Fatalf("%q: status %d", tc.ct, w.Code)
		}
	}
	list, _, _ := f.db.ListRequests(context.Background(), storage.RequestFilter{Ascending: true})
	for i, tc := range cases {
		got, _ := f.db.GetRequest(context.Background(), list[i].ID)
		if !bytes.Equal(got.Body, tc.body) && !(len(got.Body) == 0 && len(tc.body) == 0) {
			t.Errorf("case %d: stored %q, sent %q", i, got.Body, tc.body)
		}
	}
	empty, _ := f.db.GetRequest(context.Background(), list[3].ID)
	if empty.ContentLength != nil || empty.BodySize != 0 {
		t.Errorf("empty GET: length=%v size=%d", empty.ContentLength, empty.BodySize)
	}
}

func TestIngestAppliesEndpointRules(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)
	f.send(httptest.NewRequest(http.MethodPost, "/hooks/"+ep.PublicID, nil))
	orig := f.only(t).ID
	req, resp, err := f.svc.Ingest(context.Background(), ep, Inbound{
		Method:   "POST",
		Header:   http.Header{"Authorization": {"token abc"}},
		Body:     bytes.Repeat([]byte("x"), 2000),
		Source:   storage.SourceReplay,
		ReplayOf: &orig,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !req.BodyTruncated || len(req.Body) != 1024 || resp.Status != 413 {
		t.Fatalf("truncated=%v len=%d status=%d", req.BodyTruncated, len(req.Body), resp.Status)
	}
	if req.Headers["Authorization"][0] != "token [REDACTED]" || req.Path != "/" {
		t.Fatalf("headers=%v path=%q", req.Headers, req.Path)
	}
	stored, err := f.db.GetRequest(context.Background(), req.ID)
	if err != nil || stored.Source != storage.SourceReplay {
		t.Fatalf("stored: %+v %v", stored, err)
	}
}

func TestIngestBatchIsAtomic(t *testing.T) {
	f := newFixture(t, nil)
	ep := f.endpoint(t, nil)
	reqs, err := f.svc.IngestBatch(context.Background(), ep, []Inbound{
		{Method: "POST", Body: []byte("1"), Source: storage.SourceImport},
		{Method: "PUT", Body: []byte("2"), Source: storage.SourceImport},
	})
	if err != nil || len(reqs) != 2 {
		t.Fatalf("IngestBatch: %v", err)
	}
	if f.count(t) != 2 {
		t.Fatal("both requests should be stored")
	}
	if len(f.events.events) != 1 || f.events.events[0] != EventRequestsImported {
		t.Fatalf("events = %v", f.events.events)
	}

	gone := ep
	gone.ID = 9999
	if _, err := f.svc.IngestBatch(context.Background(), gone, []Inbound{{Method: "POST"}}); err == nil {
		t.Fatal("expected failure for a missing endpoint")
	}
	if f.count(t) != 2 {
		t.Fatal("failed batch must not store anything")
	}
}
