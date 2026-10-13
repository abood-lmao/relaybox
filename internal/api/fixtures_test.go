package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abood-lmao/relaybox/internal/fixture"
)

func TestExportImportRoundTrip(t *testing.T) {
	h := newHarness(t)
	src := h.createEndpoint("source", nil)
	dst := h.createEndpoint("destination", nil)
	h.hook(src, "POST", "/orders?api_key=k1&page=2", `{"order":7,"password":"pw"}`,
		"Content-Type", "application/json", "Authorization", "Bearer live", "X-Event", "order.paid")
	id := h.latestRequestID(src)

	w := h.do("GET", "/api/v1/requests/"+itoa(id)+"/export", nil)
	expectStatus(t, w, 200)
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, ".json") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	body := w.Body.String()
	for _, secret := range []string{"pw\"", "Bearer live", "k1", src.PublicID, "127.0.0.1"} {
		if strings.Contains(body, secret) {
			t.Errorf("export contains %q:\n%s", secret, body)
		}
	}
	var f fixture.File
	if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if f.Format != fixture.Format || f.Version != fixture.Version || len(f.Requests) != 1 {
		t.Fatalf("fixture = %+v", f)
	}
	r0 := f.Requests[0]
	if r0.Method != "POST" || r0.Path != "/orders" || r0.Endpoint != "source" || r0.Body == nil {
		t.Fatalf("request = %+v", r0)
	}
	for _, p := range r0.Headers {
		if strings.EqualFold(p[0], "Host") {
			t.Error("export includes the Host header")
		}
	}

	w = h.do("POST", "/api/v1/fixtures/import?endpoint_id="+itoa(dst.ID), w.Body.Bytes())
	expectStatus(t, w, 200)
	imp := decode[struct {
		Imported   int     `json:"imported"`
		EndpointID int64   `json:"endpoint_id"`
		RequestIDs []int64 `json:"request_ids"`
	}](t, w)
	if imp.Imported != 1 || imp.EndpointID != dst.ID || len(imp.RequestIDs) != 1 {
		t.Fatalf("import = %+v", imp)
	}
	w = h.do("GET", "/api/v1/requests/"+itoa(imp.RequestIDs[0]), nil)
	got := decode[RequestDetailView](t, w)
	if got.Source != "import" || got.Endpoint.ID != dst.ID || got.Method != "POST" || got.Path != "/orders" ||
		got.QueryString != r0.Query || !strings.Contains(got.Body.Data, `"order":7`) {
		t.Fatalf("imported request = %+v", got)
	}
	if !strings.Contains(got.URL, dst.PublicID) {
		t.Errorf("imported url = %q", got.URL)
	}
}

func TestExportOptions(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("ep", nil)
	for _, b := range []string{`{"n":1}`, `{"n":2}`, `{"n":3}`} {
		h.hook(ep, "POST", "", b, "Content-Type", "application/json")
	}
	latest := h.latestRequestID(ep)

	w := h.do("GET", "/api/v1/requests/"+itoa(latest)+"/export?include_body=false", nil)
	expectStatus(t, w, 200)
	var f fixture.File
	_ = json.Unmarshal(w.Body.Bytes(), &f)
	if f.Requests[0].Body != nil || !f.Requests[0].BodyOmitted {
		t.Errorf("body should be omitted: %+v", f.Requests[0])
	}
	expectError(t, h.do("GET", "/api/v1/requests/"+itoa(latest)+"/export?include_body=maybe", nil), 400, "invalid_parameter")
	expectError(t, h.do("GET", "/api/v1/requests/987654/export", nil), 404, "not_found")

	w = h.do("POST", "/api/v1/fixtures/export", map[string]any{"endpoint_id": ep.ID, "limit": 2})
	expectStatus(t, w, 200)
	f = fixture.File{}
	_ = json.Unmarshal(w.Body.Bytes(), &f)
	if len(f.Requests) != 2 {
		t.Fatalf("endpoint export has %d requests", len(f.Requests))
	}
	w = h.do("POST", "/api/v1/fixtures/export", map[string]any{"request_ids": []int64{latest}, "include_bodies": false})
	expectStatus(t, w, 200)
	f = fixture.File{}
	_ = json.Unmarshal(w.Body.Bytes(), &f)
	if len(f.Requests) != 1 || f.Requests[0].Body != nil {
		t.Fatalf("ids export = %+v", f.Requests)
	}

	expectError(t, h.do("POST", "/api/v1/fixtures/export", map[string]any{}), 400, "invalid_input")
	expectError(t, h.do("POST", "/api/v1/fixtures/export", map[string]any{"endpoint_id": ep.ID, "limit": 5000}), 400, "invalid_input")
	expectError(t, h.do("POST", "/api/v1/fixtures/export", map[string]any{"endpoint_id": 999}), 404, "not_found")
	expectError(t, h.do("POST", "/api/v1/fixtures/export", map[string]any{"request_ids": []int64{999}}), 404, "not_found")
}

func TestImportRejectsInvalidFixtures(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("ep", nil)
	path := "/api/v1/fixtures/import?endpoint_id=" + itoa(ep.ID)

	expectError(t, h.do("POST", "/api/v1/fixtures/import", `{}`), 400, "invalid_parameter")
	expectError(t, h.do("POST", "/api/v1/fixtures/import?endpoint_id=999", `{}`), 404, "not_found")

	r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	expectError(t, w, http.StatusUnsupportedMediaType, "unsupported_media_type")

	for name, body := range map[string]string{
		"not json":       `not json`,
		"wrong format":   `{"format":"other","version":1,"requests":[]}`,
		"wrong version":  `{"format":"relaybox.fixture","version":9,"requests":[{"method":"GET","path":"/","headers":[]}]}`,
		"unknown field":  `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[],"run":"rm -rf /"}]}`,
		"bad method":     `{"format":"relaybox.fixture","version":1,"requests":[{"method":"TRACE","path":"/","headers":[]}]}`,
		"bad path":       `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"../etc","headers":[]}]}`,
		"header shape":   `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[["a","b","c"]]}]}`,
		"header newline": `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[["X-A","1\r\nX-B: 2"]]}]}`,
		"bad base64":     `{"format":"relaybox.fixture","version":1,"requests":[{"method":"POST","path":"/","headers":[],"body":{"encoding":"base64","data":"%%%"}}]}`,
		"no requests":    `{"format":"relaybox.fixture","version":1,"requests":[]}`,
	} {
		w := h.do("POST", path, body)
		e := expectError(t, w, 422, "invalid_fixture")
		if len(e.Details) == 0 {
			t.Errorf("%s: no details", name)
		}
	}
	if p := h.list(""); p.Total != 0 {
		t.Fatalf("rejected imports stored %d requests", p.Total)
	}

	big := `{"format":"relaybox.fixture","version":1,"requests":[{"method":"POST","path":"/","headers":[],"body":{"encoding":"utf8","data":"` +
		strings.Repeat("a", fixture.MaxBytes) + `"}}]}`
	expectError(t, h.do("POST", path, big), 413, "payload_too_large")
}

func TestImportAppliesCurrentRules(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("ep", map[string]any{"max_body_bytes": 1024})
	data := `{"format":"relaybox.fixture","version":1,"requests":[
		{"method":"POST","path":"/a","query":"access_token=t","headers":[["Content-Type","application/json"],["Authorization","Bearer x"]],
		 "body":{"encoding":"utf8","data":"{\"secret\":\"s\",\"keep\":1}"}},
		{"method":"POST","path":"/big","headers":[],"body":{"encoding":"utf8","data":"` + strings.Repeat("b", 2048) + `"}}
	]}`
	w := h.do("POST", "/api/v1/fixtures/import?endpoint_id="+itoa(ep.ID), data)
	expectStatus(t, w, 200)
	ids := decode[struct {
		RequestIDs []int64 `json:"request_ids"`
	}](t, w).RequestIDs
	if len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}

	w = h.do("GET", "/api/v1/requests/"+itoa(ids[0]), nil)
	first := w.Body.String()
	if strings.Contains(first, `"s"`) || strings.Contains(first, "Bearer x") || strings.Contains(first, "access_token=t") {
		t.Errorf("imported secrets were not redacted: %s", first)
	}
	w = h.do("GET", "/api/v1/requests/"+itoa(ids[1]), nil)
	second := decode[RequestDetailView](t, w)
	if !second.BodyTruncated || second.Body.StoredBytes != 1024 {
		t.Errorf("endpoint body limit not applied: %+v", second.Body)
	}
}
