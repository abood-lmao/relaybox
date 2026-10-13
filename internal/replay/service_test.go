package replay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/redact"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/validate"
)

type env struct {
	db  *storage.DB
	svc *Service
	cap *capture.Service
	ep  storage.Endpoint
}

func newEnv(t *testing.T, allowed []string, urlEnabled bool) *env {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	set, _, err := settings.New(context.Background(), db, settings.Values{
		DefaultMaxBodyBytes: 1024, RedactHeaders: redact.DefaultHeaders, RedactFields: redact.DefaultFields,
	}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	capSvc := capture.New(capture.Options{DB: db, Settings: set})
	g := NewGuard(allowed)
	svc := NewService(ServiceOptions{
		DB: db, Capture: capSvc, Guard: g, Sender: NewSender(g, "relaybox-test", 5*time.Second), URLEnabled: urlEnabled,
	})
	ep, err := db.CreateEndpoint(context.Background(), storage.NewEndpoint{Name: "source", Enabled: true, Mock: mockresp.Default()})
	if err != nil {
		t.Fatal(err)
	}
	return &env{db: db, svc: svc, cap: capSvc, ep: ep}
}

func (e *env) capture(t *testing.T, body string, header http.Header) storage.CapturedRequest {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/hooks/"+e.ep.PublicID+"/events?x=1", strings.NewReader(body))
	for k, v := range header {
		r.Header[k] = v
	}
	e.cap.ServeHTTP(httptest.NewRecorder(), r)
	ids, err := e.db.LatestRequestIDs(context.Background(), e.ep.ID, 1)
	if err != nil || len(ids) == 0 {
		t.Fatalf("no request captured: %v", err)
	}
	req, err := e.db.GetRequest(context.Background(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestHeaderOptions(t *testing.T) {
	req := storage.CapturedRequest{
		Headers: map[string][]string{
			"Authorization":   {"Bearer [REDACTED]"},
			"Content-Type":    {"application/json"},
			"Content-Length":  {"10"},
			"X-Forwarded-For": {"1.2.3.4"},
			"X-Signature":     {"sha256=abc"},
		},
		Redactions: []redact.Redaction{{Location: "header", Name: "Authorization"}},
	}
	got := map[string]HeaderOption{}
	for _, o := range HeaderOptions(req) {
		got[o.Name] = o
	}
	if o := got["Authorization"]; o.Selected || !o.Redacted {
		t.Errorf("redacted header: %+v", o)
	}
	if o := got["Content-Length"]; o.Selected || o.Blocked == "" {
		t.Errorf("framing header: %+v", o)
	}
	if o := got["X-Forwarded-For"]; o.Selected || o.Blocked != "" {
		t.Errorf("proxy header: %+v", o)
	}
	if !got["Content-Type"].Selected || !got["X-Signature"].Selected {
		t.Error("ordinary headers should be selected")
	}
}

func TestReplayToURLRecordsHistory(t *testing.T) {
	var gotAuth, gotSig, gotBody, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotAuth, gotSig, gotBody, gotPath = r.Header.Get("Authorization"), r.Header.Get("X-Signature"), string(b), r.URL.RequestURI()
		w.WriteHeader(202)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	e := newEnv(t, []string{u.Host}, true)
	orig := e.capture(t, `{"a":1}`, http.Header{"Authorization": {"Bearer abc"}, "X-Signature": {"sha256=1"}})

	out, err := e.svc.Replay(context.Background(), orig.ID, Input{
		Target: TargetURL, URL: srv.URL + "/receive?token=secret", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Error("redacted Authorization header must not be forwarded by default")
	}
	if gotSig != "sha256=1" || gotBody != `{"a":1}` || gotPath != "/receive?token=secret" {
		t.Errorf("server saw sig=%q body=%q path=%q", gotSig, gotBody, gotPath)
	}
	a := out.Attempt
	if a.Outcome != storage.OutcomeSuccess || *a.ResponseStatus != 202 || out.Response == nil {
		t.Errorf("attempt %+v", a)
	}
	if strings.Contains(a.Destination, "token") || a.DestinationHost != "127.0.0.1" {
		t.Errorf("destination stored with query or wrong host: %q %q", a.Destination, a.DestinationHost)
	}
	history, _ := e.svc.History(context.Background(), orig.ID)
	if len(history) != 1 || history[0].ID != a.ID {
		t.Errorf("history = %+v", history)
	}
}

func TestReplayWithEditedBodyAndHeaders(t *testing.T) {
	var gotBody, gotType, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotType, gotMethod = string(b), r.Header.Get("Content-Type"), r.Method
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	e := newEnv(t, []string{u.Host}, true)
	orig := e.capture(t, `{"a":1}`, nil)
	_, err := e.svc.Replay(context.Background(), orig.ID, Input{
		Target: TargetURL, URL: srv.URL, Confirmed: true, Method: "patch",
		Headers: [][2]string{{"Content-Type", "text/plain"}}, Body: []byte("edited"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody != "edited" || gotType != "text/plain" || gotMethod != "PATCH" {
		t.Fatalf("server saw %s %q %q", gotMethod, gotType, gotBody)
	}
}

func TestReplayValidation(t *testing.T) {
	e := newEnv(t, nil, true)
	orig := e.capture(t, "x", nil)
	ctx := context.Background()

	if _, err := e.svc.Replay(ctx, orig.ID, Input{Target: TargetURL, URL: "https://example.com/"}); !errors.Is(err, ErrConfirmationRequired) {
		t.Errorf("unconfirmed: %v", err)
	}
	var verr validate.Errors
	if _, err := e.svc.Replay(ctx, orig.ID, Input{Target: TargetURL, URL: "http://127.0.0.1:9/", Confirmed: true}); !errors.As(err, &verr) || verr["url"] == "" {
		t.Errorf("loopback: %v", err)
	}
	_, err := e.svc.Replay(ctx, orig.ID, Input{
		Target: TargetURL, URL: "https://example.com/", Confirmed: true,
		Headers: [][2]string{{"Host", "evil"}, {"X-Bad", "a\r\nInjected: 1"}, {"Bad Name", "x"}},
		Method:  "TRACE",
	})
	if !errors.As(err, &verr) || verr["headers"] == "" || verr["method"] == "" {
		t.Errorf("header/method validation: %v", err)
	}
	if _, err := e.svc.Replay(ctx, orig.ID, Input{Target: "ftp"}); !errors.As(err, &verr) || verr["target"] == "" {
		t.Errorf("target: %v", err)
	}
	if _, err := e.svc.Replay(ctx, 9999, Input{Target: TargetURL}); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("missing request: %v", err)
	}
	history, _ := e.svc.History(ctx, orig.ID)
	if len(history) != 0 {
		t.Errorf("rejected input must not be recorded: %+v", history)
	}
}

func TestBlockedAtDialTimeIsRecordedAsFailure(t *testing.T) {
	e := newEnv(t, nil, true)
	orig := e.capture(t, "x", nil)
	// "localhost.example" passes URL validation but resolves to loopback.
	e.svc.guard.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	out, err := e.svc.Replay(context.Background(), orig.ID, Input{Target: TargetURL, URL: "http://localhost.example/", Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Attempt.Outcome != storage.OutcomeFailure || !strings.HasPrefix(out.Attempt.Summary, "Blocked:") {
		t.Fatalf("attempt %+v", out.Attempt)
	}
}

func TestURLReplayDisabled(t *testing.T) {
	e := newEnv(t, nil, false)
	orig := e.capture(t, "x", nil)
	if _, err := e.svc.Replay(context.Background(), orig.ID, Input{Target: TargetURL, URL: "https://example.com", Confirmed: true}); !errors.Is(err, ErrURLReplayDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestTruncatedBodyCannotBeReplayedAsIs(t *testing.T) {
	e := newEnv(t, nil, true)
	orig := e.capture(t, strings.Repeat("z", 3000), nil) // limit is 1024
	if !orig.BodyTruncated {
		t.Fatal("setup: body should be truncated")
	}
	if _, err := e.svc.Replay(context.Background(), orig.ID, Input{Target: TargetEndpoint, EndpointID: e.ep.ID}); !errors.Is(err, ErrTruncatedBody) {
		t.Fatalf("err = %v", err)
	}
}

func TestReplayIntoEndpoint(t *testing.T) {
	e := newEnv(t, nil, false) // endpoint replay works even when URL replay is off
	orig := e.capture(t, `{"n":1}`, http.Header{"X-Signature": {"abc"}})
	target, err := e.db.CreateEndpoint(context.Background(), storage.NewEndpoint{
		Name: "target", Enabled: true, Mock: mockresp.Config{Status: 201, ContentType: "text/plain", Body: "ok", Headers: map[string]string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.svc.Replay(context.Background(), orig.ID, Input{Target: TargetEndpoint, EndpointID: target.ID})
	if err != nil {
		t.Fatal(err)
	}
	if out.Created == nil || out.EndpointResponse.Status != 201 {
		t.Fatalf("outcome %+v", out)
	}
	created, err := e.db.GetRequest(context.Background(), out.Created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Source != storage.SourceReplay || created.ReplayOf == nil || *created.ReplayOf != orig.ID {
		t.Errorf("replay link missing: %+v", created)
	}
	if created.Path != "/events" || created.RawQuery != "x=1" || string(created.Body) != `{"n":1}` {
		t.Errorf("copied fields: %q %q %q", created.Path, created.RawQuery, created.Body)
	}
	if created.Headers["X-Signature"][0] != "abc" {
		t.Errorf("headers: %v", created.Headers)
	}
	if *out.Attempt.ResultRequestID != created.ID || out.Attempt.Outcome != storage.OutcomeSuccess {
		t.Errorf("attempt %+v", out.Attempt)
	}

	disabled := false
	if _, err := e.db.UpdateEndpoint(context.Background(), target.ID, storage.EndpointUpdate{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Replay(context.Background(), orig.ID, Input{Target: TargetEndpoint, EndpointID: target.ID}); !errors.Is(err, ErrEndpointDisabled) {
		t.Fatalf("disabled target: %v", err)
	}
}
