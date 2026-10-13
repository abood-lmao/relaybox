package api

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/abood-lmao/relaybox/internal/compare"
	"github.com/abood-lmao/relaybox/internal/redact"
)

type requestPage struct {
	Items  []RequestSummaryView `json:"items"`
	Total  int64                `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

func (h *harness) list(query string) requestPage {
	h.t.Helper()
	w := h.do("GET", "/api/v1/requests?"+query, nil)
	expectStatus(h.t, w, 200)
	return decode[requestPage](h.t, w)
}

func TestListRequestsFilters(t *testing.T) {
	h := newHarness(t)
	a := h.createEndpoint("alpha", nil)
	b := h.createEndpoint("beta", map[string]any{"mock_response": map[string]any{"status": 404, "content_type": "text/plain", "body": "nope"}})

	h.hook(a, "POST", "/orders", `{"event":"order.created","id":"ord_1"}`, "Content-Type", "application/json")
	h.hook(a, "PUT", "/orders?mode=full", `{"event":"order.updated"}`, "Content-Type", "application/json")
	h.hook(a, "GET", "", "")
	h.hook(b, "POST", "/x", "plain text body with needle", "Content-Type", "text/plain")

	if p := h.list(""); p.Total != 4 || len(p.Items) != 4 || p.Limit != 50 || p.Offset != 0 {
		t.Fatalf("all: %+v", p)
	}
	if p := h.list("endpoint_id=" + itoa(b.ID)); p.Total != 1 || p.Items[0].Endpoint.Name != "beta" || p.Items[0].Status != 404 {
		t.Fatalf("by endpoint: %+v", p)
	}
	if p := h.list("method=put"); p.Total != 1 || p.Items[0].Method != "PUT" || p.Items[0].QueryString != "mode=full" {
		t.Fatalf("by method: %+v", p)
	}
	if p := h.list("method=GET,PUT"); p.Total != 2 {
		t.Fatalf("by methods: %+v", p)
	}
	if p := h.list("method=GET&method=PUT"); p.Total != 2 {
		t.Fatalf("by repeated methods: %+v", p)
	}
	if p := h.list("status=4xx"); p.Total != 1 {
		t.Fatalf("by class: %+v", p)
	}
	if p := h.list("status=200"); p.Total != 3 {
		t.Fatalf("by code: %+v", p)
	}
	if p := h.list("status=2xx,404"); p.Total != 4 {
		t.Fatalf("by mixed: %+v", p)
	}
	if p := h.list("q=needle"); p.Total != 1 || p.Items[0].Path != "/x" {
		t.Fatalf("search body: %+v", p)
	}
	if p := h.list("q=ord_1"); p.Total != 1 {
		t.Fatalf("search json: %+v", p)
	}
	if p := h.list("q=" + url.QueryEscape("ord%1")); p.Total != 0 {
		t.Fatalf("%% must be matched literally: %+v", p)
	}
	if p := h.list("q=" + url.QueryEscape("o_d_1")); p.Total != 0 {
		t.Fatalf("_ must be matched literally: %+v", p)
	}
	if p := h.list("source=capture"); p.Total != 4 {
		t.Fatalf("by source: %+v", p)
	}
	if p := h.list("source=replay"); p.Total != 0 {
		t.Fatalf("by replay source: %+v", p)
	}

	today := time.Now().UTC().Format(time.DateOnly)
	if p := h.list("from=" + today + "&to=" + today); p.Total != 4 {
		t.Fatalf("today: %+v", p)
	}
	if p := h.list("to=2000-01-01"); p.Total != 0 {
		t.Fatalf("before 2000: %+v", p)
	}
	future := url.QueryEscape(time.Now().Add(time.Hour).Format(time.RFC3339))
	if p := h.list("from=" + future); p.Total != 0 {
		t.Fatalf("future: %+v", p)
	}

	p := h.list("limit=2&order=asc")
	if p.Total != 4 || len(p.Items) != 2 || p.Items[0].Method != "POST" || p.Items[1].Method != "PUT" {
		t.Fatalf("page 1: %+v", p)
	}
	p = h.list("limit=2&offset=2&order=asc")
	if len(p.Items) != 2 || p.Items[0].Method != "GET" {
		t.Fatalf("page 2: %+v", p)
	}
	if p := h.list("sort=size&order=desc&limit=1"); p.Items[0].Path != "/orders" || p.Items[0].Method != "POST" {
		t.Fatalf("by size: %+v", p.Items)
	}

	for _, bad := range []string{
		"method=TRACE", "status=99", "status=6xx", "status=abc", "source=evil", "sort=id;drop",
		"order=up", "limit=0", "limit=201", "offset=-1", "endpoint_id=x", "from=yesterday",
		"from=2026-02-02&to=2026-02-01", "q=" + strings.Repeat("a", 201),
	} {
		expectError(t, h.do("GET", "/api/v1/requests?"+bad, nil), 400, "invalid_filter")
	}
}

func TestRequestDetail(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("detail", nil)
	h.hook(ep, "POST", "/events/1?token=abc&b=2&a=1",
		`{"user":{"name":"Ada","password":"hunter2"},"items":[1,2]}`,
		"Content-Type", "application/json", "Authorization", "Bearer secret-token", "X-Custom", "<script>alert(1)</script>")
	id := h.latestRequestID(ep)

	w := h.do("GET", "/api/v1/requests/"+itoa(id), nil)
	expectStatus(t, w, 200)
	d := decode[RequestDetailView](t, w)
	if d.Method != "POST" || d.Path != "/events/1" || d.Endpoint.Name != "detail" || d.Source != "capture" {
		t.Fatalf("detail = %+v", d.RequestSummaryView)
	}
	if !strings.HasPrefix(d.URL, "http://127.0.0.1:8787/hooks/"+ep.PublicID+"/events/1?") {
		t.Errorf("url = %q", d.URL)
	}
	if strings.Contains(w.Body.String(), "hunter2") || strings.Contains(w.Body.String(), "secret-token") ||
		strings.Contains(w.Body.String(), "token=abc") {
		t.Fatalf("secret in response: %s", w.Body.String())
	}
	if !d.Redacted || !slices.Contains(d.Redactions, redact.Redaction{Location: "body", Name: "$.user.password"}) ||
		!slices.Contains(d.Redactions, redact.Redaction{Location: "header", Name: "Authorization"}) ||
		!slices.Contains(d.Redactions, redact.Redaction{Location: "query", Name: "token"}) {
		t.Errorf("redactions = %+v", d.Redactions)
	}
	if d.Body.Kind != KindJSON || d.Body.Encoding != "utf8" || !strings.Contains(d.Body.Data, `"Ada"`) {
		t.Errorf("body = %+v", d.Body)
	}
	if !slices.Contains(d.Query, [2]string{"a", "1"}) || len(d.Query) != 3 {
		t.Errorf("query = %v", d.Query)
	}
	names := []string{}
	for _, p := range d.Headers {
		names = append(names, p[0])
	}
	if !slices.IsSorted(names) || !slices.Contains(d.Headers, [2]string{"X-Custom", "<script>alert(1)</script>"}) {
		t.Errorf("headers = %v", d.Headers)
	}
	if d.Response.Status != 200 || d.Response.Body.Kind != KindJSON || d.Response.StatusText != "OK" {
		t.Errorf("response = %+v", d.Response)
	}
	if d.Redactions == nil || d.ReplayChildren == nil {
		t.Error("lists must be [] rather than null")
	}
	expectError(t, h.do("GET", "/api/v1/requests/99999", nil), 404, "not_found")
}

func TestBodyKinds(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("kinds", nil)
	get := func(body string, headers ...string) BodyView {
		t.Helper()
		h.hook(ep, "POST", "", body, headers...)
		w := h.do("GET", "/api/v1/requests/"+itoa(h.latestRequestID(ep)), nil)
		expectStatus(t, w, 200)
		return decode[RequestDetailView](t, w).Body
	}

	if b := get(""); b.Kind != KindEmpty || b.Size != 0 {
		t.Errorf("empty: %+v", b)
	}
	if b := get(`{"broken":`, "Content-Type", "application/json"); b.Kind != KindText || b.ParseError == "" {
		t.Errorf("invalid json: %+v", b)
	}
	if b := get("a=1&b=two+words&password=x", "Content-Type", "application/x-www-form-urlencoded"); b.Kind != KindForm ||
		!slices.Contains(b.Form, [2]string{"b", "two words"}) || !slices.Contains(b.Form, [2]string{"password", redact.Placeholder}) {
		t.Errorf("form: %+v", b)
	}
	bin := string([]byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff})
	if b := get(bin, "Content-Type", "image/png"); b.Kind != KindBinary || b.Encoding != "base64" {
		t.Errorf("binary: %+v", b)
	} else if raw, _ := base64.StdEncoding.DecodeString(b.Data); string(raw) != bin {
		t.Errorf("binary round trip: %q", raw)
	}
	multi := "--XyZ\r\nContent-Disposition: form-data; name=\"note\"\r\n\r\nhello\r\n" +
		"--XyZ\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.bin\"\r\nContent-Type: application/octet-stream\r\n\r\n\x00\x01\r\n--XyZ--\r\n"
	b := get(multi, "Content-Type", "multipart/form-data; boundary=XyZ")
	if b.Kind != KindMultipart || len(b.Parts) != 2 || b.ParseError != "" {
		t.Fatalf("multipart: %+v", b)
	}
	if b.Parts[0].Name != "note" || b.Parts[0].Text == nil || *b.Parts[0].Text != "hello" {
		t.Errorf("text part: %+v", b.Parts[0])
	}
	if b.Parts[1].Filename != "a.bin" || b.Parts[1].Text != nil || b.Parts[1].Size != 2 {
		t.Errorf("file part: %+v", b.Parts[1])
	}
	if b := get(`<html><script>alert(1)</script></html>`, "Content-Type", "text/html"); b.Kind != KindText {
		t.Errorf("html: %+v", b)
	}
}

func TestTruncatedBodies(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("small", map[string]any{"max_body_bytes": 1024})
	body := `{"data":"` + strings.Repeat("x", 2000) + `"}`
	if w := h.hook(ep, "POST", "", body, "Content-Type", "application/json"); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("hook = %d", w.Code)
	}
	w := h.do("GET", "/api/v1/requests/"+itoa(h.latestRequestID(ep)), nil)
	expectStatus(t, w, 200)
	d := decode[RequestDetailView](t, w)
	if !d.BodyTruncated || !d.Body.Truncated || d.Body.StoredBytes != 1024 || d.Status != 413 {
		t.Fatalf("truncated: %+v / %+v", d.RequestSummaryView, d.Body)
	}
}

func TestDeleteRequests(t *testing.T) {
	h := newHarness(t)
	a := h.createEndpoint("a", nil)
	b := h.createEndpoint("b", nil)
	for range 3 {
		h.hook(a, "POST", "", "{}")
		h.hook(b, "POST", "", "{}")
	}

	for _, q := range []string{"", "scope=", "scope=everything", "scope=endpoint", "scope=endpoint&endpoint_id=0", "scope=ids", "scope=ids&ids=1,x"} {
		expectError(t, h.do("DELETE", "/api/v1/requests?"+q, nil), 400, "invalid_scope")
	}
	expectError(t, h.do("DELETE", "/api/v1/requests?scope=endpoint&endpoint_id=999", nil), 404, "not_found")
	if p := h.list(""); p.Total != 6 {
		t.Fatalf("rejected deletes removed data: %d left", p.Total)
	}

	single := h.latestRequestID(a)
	expectStatus(t, h.do("DELETE", "/api/v1/requests/"+itoa(single), nil), 204)
	expectError(t, h.do("DELETE", "/api/v1/requests/"+itoa(single), nil), 404, "not_found")

	bIDs := h.list("endpoint_id=" + itoa(b.ID)).Items
	w := h.do("DELETE", "/api/v1/requests?scope=ids&ids="+itoa(bIDs[0].ID)+","+itoa(bIDs[1].ID)+",99999", nil)
	expectStatus(t, w, 200)
	if n := decode[map[string]int](t, w)["deleted"]; n != 2 {
		t.Fatalf("ids deleted %d", n)
	}

	w = h.do("DELETE", "/api/v1/requests?scope=endpoint&endpoint_id="+itoa(a.ID), nil)
	expectStatus(t, w, 200)
	if n := decode[map[string]int](t, w)["deleted"]; n != 2 {
		t.Fatalf("endpoint deleted %d", n)
	}
	if p := h.list(""); p.Total != 1 || p.Items[0].Endpoint.ID != b.ID {
		t.Fatalf("after scoped deletes: %+v", p)
	}

	w = h.do("DELETE", "/api/v1/requests?scope=all", nil)
	expectStatus(t, w, 200)
	if p := h.list(""); p.Total != 0 {
		t.Fatalf("after delete all: %d", p.Total)
	}
	if w := h.do("GET", "/api/v1/endpoints", nil); len(decode[struct{ Items []EndpointView }](t, w).Items) != 2 {
		t.Error("deleting requests must keep endpoints")
	}
}

func TestDownloadBody(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("dl", nil)
	h.hook(ep, "POST", "", "<svg onload=alert(1)>", "Content-Type", "image/svg+xml")
	id := itoa(h.latestRequestID(ep))

	w := h.do("GET", "/api/v1/requests/"+id+"/body", nil)
	expectStatus(t, w, 200)
	if w.Body.String() != "<svg onload=alert(1)>" {
		t.Errorf("body = %q", w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/octet-stream" ||
		!strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("headers = %v", w.Header())
	}
	w = h.do("GET", "/api/v1/requests/"+id+"/body?part=response", nil)
	expectStatus(t, w, 200)
	if w.Body.String() != `{"received":true}` || !strings.Contains(w.Header().Get("Content-Disposition"), ".json") {
		t.Errorf("response body = %q %v", w.Body.String(), w.Header())
	}
	expectError(t, h.do("GET", "/api/v1/requests/"+id+"/body?part=other", nil), 400, "invalid_part")
	expectError(t, h.do("GET", "/api/v1/requests/424242/body", nil), 404, "not_found")
}

func TestCompareRequests(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("cmp", nil)
	h.hook(ep, "POST", "", `{"id":1,"status":"pending","tags":["a"]}`, "Content-Type", "application/json", "X-Version", "1")
	left := h.latestRequestID(ep)
	h.hook(ep, "POST", "", `{"id":1,"status":"paid","tags":["a","b"],"total":10}`, "Content-Type", "application/json", "X-Version", "2")
	right := h.latestRequestID(ep)

	w := h.do("POST", "/api/v1/requests/compare", map[string]any{"left_id": left, "right_id": right})
	expectStatus(t, w, 200)
	type comparison struct {
		Part    string               `json:"part"`
		Left    RequestSummaryView   `json:"left"`
		Right   RequestSummaryView   `json:"right"`
		Body    compare.Result       `json:"body"`
		Headers []compare.Difference `json:"headers"`
	}
	res := decode[comparison](t, w)
	if res.Part != "request" || res.Left.ID != left || res.Right.ID != right || res.Body.Mode != compare.ModeJSON || res.Body.Equal {
		t.Fatalf("compare = %+v", res)
	}
	paths := map[string]string{}
	for _, d := range res.Body.Differences {
		paths[d.Path] = d.Kind
	}
	if paths["$.status"] != compare.Changed || paths["$.tags[1]"] != compare.Added || paths["$.total"] != compare.Added || len(paths) != 3 {
		t.Errorf("differences = %+v", res.Body.Differences)
	}
	if !slices.ContainsFunc(res.Headers, func(d compare.Difference) bool {
		return d.Path == "X-Version" && d.Kind == compare.Changed && d.Left == "1" && d.Right == "2"
	}) {
		t.Errorf("header differences = %+v", res.Headers)
	}

	w = h.do("POST", "/api/v1/requests/compare", map[string]any{"left_id": left, "right_id": right, "part": "response"})
	expectStatus(t, w, 200)
	if body := decode[struct{ Body compare.Result }](t, w).Body; !body.Equal {
		t.Errorf("identical responses differ: %+v", body)
	}
	expectError(t, h.do("POST", "/api/v1/requests/compare", map[string]any{"left_id": left, "right_id": 777}), 404, "not_found")
	expectError(t, h.do("POST", "/api/v1/requests/compare", map[string]any{"left_id": left}), 400, "invalid_input")
	expectError(t, h.do("POST", "/api/v1/requests/compare", map[string]any{"left_id": left, "right_id": right, "part": "x"}), 400, "invalid_input")
}
