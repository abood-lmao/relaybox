package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/abood-lmao/relaybox/internal/config"
)

type replayResult struct {
	Attempt          ReplayAttemptView `json:"attempt"`
	Response         *ResponseView     `json:"response"`
	CreatedRequestID *int64            `json:"created_request_id"`
}

type replayDraftView struct {
	Request          RequestSummaryView `json:"request"`
	Method           string             `json:"method"`
	Headers          []HeaderOptionView `json:"headers"`
	Body             BodyView           `json:"body"`
	URLReplayEnabled bool               `json:"url_replay_enabled"`
	AllowedHosts     []string           `json:"allowed_hosts"`
}

func TestReplayDraft(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("src", nil)
	h.hook(ep, "PUT", "", `{"a":1}`, "Content-Type", "application/json", "Authorization", "Bearer abc", "X-Forwarded-For", "1.2.3.4", "X-Signature", "sig")
	id := itoa(h.latestRequestID(ep))

	w := h.do("GET", "/api/v1/requests/"+id+"/replay", nil)
	expectStatus(t, w, 200)
	d := decode[replayDraftView](t, w)
	if d.Method != "PUT" || !d.URLReplayEnabled || d.AllowedHosts == nil || d.Body.Data != `{"a":1}` {
		t.Fatalf("draft = %+v", d)
	}
	opts := map[string]HeaderOptionView{}
	for _, o := range d.Headers {
		opts[o.Name] = o
	}
	if o := opts["Authorization"]; o.Selected || !o.Redacted {
		t.Errorf("redacted header should be listed but not selected: %+v", o)
	}
	if o := opts["Host"]; o.Selected || o.Blocked == "" {
		t.Errorf("Host must be blocked: %+v", o)
	}
	if o := opts["X-Forwarded-For"]; o.Selected {
		t.Errorf("proxy header selected by default: %+v", o)
	}
	if o := opts["X-Signature"]; !o.Selected {
		t.Errorf("signature header should be selected: %+v", o)
	}
	expectError(t, h.do("GET", "/api/v1/requests/9999/replay", nil), 404, "not_found")
}

func TestReplayToEndpoint(t *testing.T) {
	h := newHarness(t)
	src := h.createEndpoint("src", nil)
	dst := h.createEndpoint("dst", map[string]any{"mock_response": map[string]any{"status": 201, "content_type": "text/plain", "body": "made"}})
	h.hook(src, "POST", "/orders?x=1", `{"total":5}`, "Content-Type", "application/json", "X-Signature", "abc")
	origID := h.latestRequestID(src)

	w := h.do("POST", "/api/v1/requests/"+itoa(origID)+"/replay", map[string]any{"target": "endpoint", "endpoint_id": dst.ID})
	expectStatus(t, w, 200)
	res := decode[replayResult](t, w)
	if res.CreatedRequestID == nil || res.Attempt.Outcome != "success" || res.Attempt.Target != "endpoint" ||
		res.Attempt.Destination != "dst" || res.Response == nil || res.Response.Status != 201 || res.Response.Body.Data != "made" {
		t.Fatalf("result = %+v", res)
	}

	w = h.do("GET", "/api/v1/requests/"+itoa(*res.CreatedRequestID), nil)
	expectStatus(t, w, 200)
	created := decode[RequestDetailView](t, w)
	if created.Source != "replay" || created.ReplayOf == nil || *created.ReplayOf != origID ||
		created.Path != "/orders" || created.QueryString != "x=1" || created.Body.Data != `{"total":5}` ||
		!slices.Contains(created.Headers, [2]string{"X-Signature", "abc"}) {
		t.Fatalf("created = %+v", created)
	}

	w = h.do("GET", "/api/v1/requests/"+itoa(origID), nil)
	if kids := decode[RequestDetailView](t, w).ReplayChildren; len(kids) != 1 || kids[0].ID != *res.CreatedRequestID || kids[0].Endpoint.Name != "dst" {
		t.Fatalf("children = %+v", kids)
	}

	// Edited body and headers are sent instead of the stored ones.
	w = h.do("POST", "/api/v1/requests/"+itoa(origID)+"/replay", map[string]any{
		"target": "endpoint", "endpoint_id": dst.ID, "method": "patch",
		"headers": [][2]string{{"Content-Type", "text/plain"}},
		"body":    map[string]string{"encoding": "base64", "data": "aGVsbG8="},
	})
	expectStatus(t, w, 200)
	res2 := decode[replayResult](t, w)
	w = h.do("GET", "/api/v1/requests/"+itoa(*res2.CreatedRequestID), nil)
	edited := decode[RequestDetailView](t, w)
	if edited.Method != "PATCH" || edited.Body.Data != "hello" || slices.ContainsFunc(edited.Headers, func(p [2]string) bool { return p[0] == "X-Signature" }) {
		t.Fatalf("edited replay = %+v", edited)
	}

	w = h.do("GET", "/api/v1/requests/"+itoa(origID)+"/replays", nil)
	expectStatus(t, w, 200)
	if items := decode[struct{ Items []ReplayAttemptView }](t, w).Items; len(items) != 2 || items[0].ResultRequestID == nil {
		t.Fatalf("history = %+v", items)
	}
	expectError(t, h.do("GET", "/api/v1/requests/31337/replays", nil), 404, "not_found")
}

func TestReplayValidation(t *testing.T) {
	h := newHarness(t)
	src := h.createEndpoint("src", nil)
	off := h.createEndpoint("off", map[string]any{"enabled": false})
	h.hook(src, "POST", "", "{}")
	path := "/api/v1/requests/" + itoa(h.latestRequestID(src)) + "/replay"

	cases := []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"target": "ftp"}, "target"},
		{map[string]any{"target": "endpoint"}, "endpoint_id"},
		{map[string]any{"target": "endpoint", "endpoint_id": 999}, "endpoint_id"},
		{map[string]any{"target": "endpoint", "endpoint_id": src.ID, "method": "TRACE"}, "method"},
		{map[string]any{"target": "endpoint", "endpoint_id": src.ID, "headers": [][2]string{{"Host", "evil"}}}, "headers"},
		{map[string]any{"target": "endpoint", "endpoint_id": src.ID, "headers": [][2]string{{"X-A", "a\r\nb"}}}, "headers"},
		{map[string]any{"target": "endpoint", "endpoint_id": src.ID, "headers": [][2]string{{"X-Relaybox-Replay", "1"}}}, "headers"},
		{map[string]any{"target": "endpoint", "endpoint_id": src.ID, "body": map[string]string{"encoding": "base64", "data": "!!"}}, "body"},
		{map[string]any{"target": "endpoint", "endpoint_id": src.ID, "body": map[string]string{"encoding": "rot13", "data": "x"}}, "body"},
		{map[string]any{"target": "url", "url": "http://127.0.0.1:9/", "confirmed": true}, "url"},
		{map[string]any{"target": "url", "url": "http://169.254.169.254/latest/meta-data/", "confirmed": true}, "url"},
		{map[string]any{"target": "url", "url": "http://localhost/", "confirmed": true}, "url"},
		{map[string]any{"target": "url", "url": "file:///etc/passwd", "confirmed": true}, "url"},
		{map[string]any{"target": "url", "url": "https://user:pw@example.com/", "confirmed": true}, "url"},
	}
	for _, c := range cases {
		e := expectError(t, h.do("POST", path, c.body), 422, "validation_failed")
		if e.Fields[c.field] == "" {
			t.Errorf("%v: fields = %v", c.body, e.Fields)
		}
	}
	expectError(t, h.do("POST", path, map[string]any{"target": "endpoint", "endpoint_id": off.ID}), 409, "endpoint_disabled")
	expectError(t, h.do("POST", path, map[string]any{"target": "url", "url": "https://example.com/hook"}), 409, "confirmation_required")
	expectError(t, h.do("POST", "/api/v1/requests/5555/replay", map[string]any{"target": "endpoint", "endpoint_id": src.ID}), 404, "not_found")

	small := h.createEndpoint("small", map[string]any{"max_body_bytes": 1024})
	h.hook(small, "POST", "", strings.Repeat("z", 3000))
	truncated := "/api/v1/requests/" + itoa(h.latestRequestID(small)) + "/replay"
	expectError(t, h.do("POST", truncated, map[string]any{"target": "endpoint", "endpoint_id": src.ID}), 409, "body_truncated")
	w := h.do("POST", truncated, map[string]any{"target": "endpoint", "endpoint_id": src.ID, "body": map[string]string{"data": "replacement"}})
	expectStatus(t, w, 200)

	w = h.do("GET", "/api/v1/requests/"+itoa(h.latestRequestID(src))+"/replays", nil)
	if items := decode[struct{ Items []ReplayAttemptView }](t, w).Items; len(items) != 0 {
		t.Errorf("rejected replays were recorded: %+v", items)
	}
}

func TestReplayToAllowlistedURL(t *testing.T) {
	var (
		mu       sync.Mutex
		received *http.Request
		gotBody  string
	)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		received, gotBody = r.Clone(r.Context()), string(b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer target.Close()
	hostPort := strings.TrimPrefix(target.URL, "http://")

	h := newHarness(t, func(c *config.Config) { c.ReplayAllowedHosts = []string{hostPort} })
	src := h.createEndpoint("src", nil)
	h.hook(src, "POST", "/p?secret=1", `{"n":1}`, "Content-Type", "application/json", "X-Signature", "s1", "Authorization", "Bearer real")
	id := itoa(h.latestRequestID(src))

	w := h.do("POST", "/api/v1/requests/"+id+"/replay", map[string]any{
		"target": "url", "url": target.URL + "/receive?token=abc", "confirmed": true,
	})
	expectStatus(t, w, 200)
	res := decode[replayResult](t, w)
	if res.Attempt.Outcome != "success" || res.Response == nil || res.Response.Status != 202 || res.Response.Body.Data != `{"ok":true}` {
		t.Fatalf("result = %+v", res)
	}
	if res.Attempt.Destination != target.URL+"/receive" || strings.Contains(w.Body.String(), "token=abc") {
		t.Errorf("destination must be stored without the query: %q", res.Attempt.Destination)
	}

	mu.Lock()
	defer mu.Unlock()
	if received == nil {
		t.Fatal("target received nothing")
	}
	if received.Method != "POST" || received.URL.RawQuery != "token=abc" || gotBody != `{"n":1}` {
		t.Errorf("target got %s %s %q", received.Method, received.URL, gotBody)
	}
	if received.Header.Get("X-Signature") != "s1" || received.Header.Get("Authorization") != "" {
		t.Errorf("forwarded headers = %v", received.Header)
	}
	if !strings.HasPrefix(received.Header.Get("User-Agent"), "relaybox") {
		t.Errorf("user agent = %q", received.Header.Get("User-Agent"))
	}

	// A different port on the same host is not allowlisted.
	u, _ := url.Parse(target.URL)
	e := expectError(t, h.do("POST", "/api/v1/requests/"+id+"/replay", map[string]any{
		"target": "url", "url": "http://" + u.Hostname() + ":1/", "confirmed": true,
	}), 422, "validation_failed")
	if e.Fields["url"] == "" {
		t.Errorf("fields = %v", e.Fields)
	}
}

func TestReplayFailureIsRecorded(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer target.Close()
	h := newHarness(t, func(c *config.Config) { c.ReplayAllowedHosts = []string{strings.TrimPrefix(target.URL, "http://")} })
	src := h.createEndpoint("src", nil)
	h.hook(src, "POST", "", "{}")
	id := itoa(h.latestRequestID(src))

	w := h.do("POST", "/api/v1/requests/"+id+"/replay", map[string]any{"target": "url", "url": target.URL, "confirmed": true})
	expectStatus(t, w, 200)
	res := decode[replayResult](t, w)
	if res.Attempt.Outcome != "failure" || res.Attempt.ResponseStatus == nil || *res.Attempt.ResponseStatus != 500 {
		t.Fatalf("result = %+v", res)
	}
	addr := target.Listener.Addr().String()
	target.Close()
	w = h.do("POST", "/api/v1/requests/"+id+"/replay", map[string]any{"target": "url", "url": "http://" + addr + "/", "confirmed": true})
	expectStatus(t, w, 200)
	res = decode[replayResult](t, w)
	if res.Attempt.Outcome != "failure" || res.Response != nil || res.Attempt.Summary == "" {
		t.Fatalf("connection failure = %+v", res)
	}
}

func TestURLReplayDisabledForRemoteDashboards(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Host = "0.0.0.0"
		c.AllowRemoteDashboard = true
		c.DashboardToken = testToken
	})
	src := h.createEndpoint("src", nil)
	other := h.createEndpoint("other", nil)
	h.hook(src, "POST", "", "{}")
	id := itoa(h.latestRequestID(src))

	w := h.do("GET", "/api/v1/requests/"+id+"/replay", nil)
	if d := decode[replayDraftView](t, w); d.URLReplayEnabled {
		t.Error("draft says URL replay is enabled")
	}
	expectError(t, h.do("POST", "/api/v1/requests/"+id+"/replay", map[string]any{
		"target": "url", "url": "https://example.com/", "confirmed": true,
	}), 403, "replay_disabled")
	expectStatus(t, h.do("POST", "/api/v1/requests/"+id+"/replay", map[string]any{"target": "endpoint", "endpoint_id": other.ID}), 200)

	h2 := newHarness(t, func(c *config.Config) {
		c.Host = "0.0.0.0"
		c.AllowRemoteDashboard = true
		c.DashboardToken = testToken
		c.AllowRemoteReplay = true
	})
	src2 := h2.createEndpoint("src", nil)
	h2.hook(src2, "POST", "", "{}")
	e := expectError(t, h2.do("POST", "/api/v1/requests/"+itoa(h2.latestRequestID(src2))+"/replay", map[string]any{
		"target": "url", "url": "http://10.0.0.5/", "confirmed": true,
	}), 422, "validation_failed")
	if e.Fields["url"] == "" {
		t.Errorf("private address must stay blocked with remote replay on: %v", e.Fields)
	}
}
