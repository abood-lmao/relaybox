package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<!doctype html><title>Relaybox</title>")},
		"assets/app-abc123.js":  {Data: []byte("console.log(1)")},
		"assets/app-abc123.css": {Data: []byte("body{}")},
		"favicon.svg":           {Data: []byte("<svg/>")},
	}
}

func get(t *testing.T, h http.Handler, method, target string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestServesFilesWithCaching(t *testing.T) {
	h, err := NewHandler(testFS())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Built() {
		t.Fatal("Built() = false")
	}

	w := get(t, h, "GET", "/assets/app-abc123.js")
	if w.Code != 200 || w.Body.String() != "console.log(1)" {
		t.Fatalf("asset: %d %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("js content type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset cache = %q", cc)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	if w := get(t, h, "GET", "/assets/app-abc123.js", "If-None-Match", etag); w.Code != http.StatusNotModified {
		t.Errorf("conditional request = %d", w.Code)
	}

	w = get(t, h, "GET", "/")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>Relaybox</title>") || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index: %d %v", w.Code, w.Header())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("html content type = %q", ct)
	}
	if w := get(t, h, "HEAD", "/favicon.svg"); w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("HEAD favicon: %d %v", w.Code, w.Header())
	}
}

func TestSPAFallback(t *testing.T) {
	h, _ := NewHandler(testFS())
	for _, p := range []string{"/requests", "/requests/42", "/endpoints/7/settings", "/index.html"} {
		w := get(t, h, "GET", p)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>Relaybox</title>") {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	for _, p := range []string{"/assets/missing.js", "/missing.png", "/assets/x"} {
		if w := get(t, h, "GET", p); w.Code != 404 {
			t.Errorf("%s: %d, want 404", p, w.Code)
		}
	}
	if w := get(t, h, "GET", "/../../etc/passwd"); !strings.Contains(w.Body.String(), "<title>Relaybox</title>") {
		t.Errorf("traversal should only ever reach embedded files: %d %q", w.Code, w.Body.String())
	}
	if w := get(t, h, "GET", "/assets/../index.html"); w.Code != 200 || !strings.Contains(w.Body.String(), "Relaybox") {
		t.Errorf("cleaned path: %d", w.Code)
	}
	if w := get(t, h, "POST", "/"); w.Code != 405 {
		t.Errorf("POST: %d", w.Code)
	}
}

func TestNotBuiltPage(t *testing.T) {
	h, err := NewHandler(nil)
	if err != nil {
		t.Fatal(err)
	}
	if h.Built() {
		t.Fatal("Built() = true")
	}
	w := get(t, h, "GET", "/requests")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "make build") {
		t.Fatalf("not built page: %d %q", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<style") || strings.Contains(w.Body.String(), "<script") {
		t.Error("the page must work under the dashboard CSP (no inline styles or scripts)")
	}
}
