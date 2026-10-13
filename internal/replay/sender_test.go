package replay

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// allowServer returns a guard that allows the test server's loopback address.
func allowServer(t *testing.T, srv *httptest.Server) (*Guard, *url.URL) {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	g := NewGuard([]string{u.Host})
	return g, u
}

func TestSendToAllowlistedServer(t *testing.T) {
	var gotHeader, gotBody, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotHeader, gotBody, gotMethod = r.Header.Get("X-Event"), string(b), r.Method
		w.Header().Set("X-Reply", "ok")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, "created")
	}))
	defer srv.Close()
	g, u := allowServer(t, srv)
	res := NewSender(g, "relaybox-test", 5*time.Second).Send(context.Background(), Request{
		URL: u, Method: "PUT", Header: http.Header{"X-Event": {"push"}}, Body: []byte("payload"),
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Status != 201 || string(res.Body) != "created" || res.Header.Get("X-Reply") != "ok" {
		t.Fatalf("result %+v", res)
	}
	if gotMethod != "PUT" || gotHeader != "push" || gotBody != "payload" {
		t.Fatalf("server saw %s %q %q", gotMethod, gotHeader, gotBody)
	}
}

func TestLoopbackIsBlockedWithoutAllowlist(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	res := NewSender(NewGuard(nil), "t", time.Second).Send(context.Background(), Request{URL: u, Method: "GET"})
	if !IsBlocked(res.Err) || hits.Load() != 0 {
		t.Fatalf("err=%v hits=%d", res.Err, hits.Load())
	}
}

func TestResolvedAddressIsDialedDirectly(t *testing.T) {
	var host string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { host = r.Host }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	g := NewGuard([]string{"hooks.test:" + port})
	var lookups atomic.Int32
	g.lookup = func(_ context.Context, name string) ([]netip.Addr, error) {
		lookups.Add(1)
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	u, _ := url.Parse("http://hooks.test:" + port + "/x")
	res := NewSender(g, "t", time.Second).Send(context.Background(), Request{URL: u, Method: "GET"})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if host != "hooks.test:"+port {
		t.Errorf("Host header = %q", host)
	}
	if lookups.Load() != 1 {
		t.Errorf("resolved %d times, want exactly once", lookups.Load())
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()

	srvURL, _ := url.Parse(srv.URL)
	targetURL, _ := url.Parse(target.URL)
	g := NewGuard([]string{srvURL.Host, targetURL.Host})
	res := NewSender(g, "t", time.Second).Send(context.Background(), Request{URL: srvURL, Method: "POST"})
	if res.Err != nil || res.Status != http.StatusFound {
		t.Fatalf("status=%d err=%v", res.Status, res.Err)
	}
	if res.Header.Get("Location") != target.URL || targetHits.Load() != 0 {
		t.Fatalf("redirect was followed (hits=%d)", targetHits.Load())
	}
}

func TestResponseBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("x", 64<<10)
		for i := 0; i < 40; i++ { // 2.5 MiB
			_, _ = io.WriteString(w, chunk)
		}
	}))
	defer srv.Close()
	g, u := allowServer(t, srv)
	res := NewSender(g, "t", 5*time.Second).Send(context.Background(), Request{URL: u, Method: "GET"})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.BodyTruncated || len(res.Body) != MaxResponseBodyBytes {
		t.Fatalf("truncated=%v len=%d", res.BodyTruncated, len(res.Body))
	}
}

func TestTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	g, u := allowServer(t, srv)
	s := NewSender(g, "t", 200*time.Millisecond)
	res := s.Send(context.Background(), Request{URL: u, Method: "GET"})
	if res.Err == nil || !strings.Contains(s.Summary(res), "Timed out") {
		t.Fatalf("err=%v summary=%q", res.Err, s.Summary(res))
	}
}

func TestTLSVerificationIsEnforced(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	g, u := allowServer(t, srv)
	s := NewSender(g, "t", 5*time.Second)
	res := s.Send(context.Background(), Request{URL: u, Method: "GET"})
	if res.Err == nil || !strings.Contains(s.Summary(res), "TLS certificate") {
		t.Fatalf("self-signed certificate accepted: err=%v summary=%q", res.Err, s.Summary(res))
	}
}

func TestEnvironmentProxyIsIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	g, u := allowServer(t, srv)
	res := NewSender(g, "t", 5*time.Second).Send(context.Background(), Request{URL: u, Method: "GET"})
	if res.Err != nil || res.Status != 204 {
		t.Fatalf("request went through the proxy: status=%d err=%v", res.Status, res.Err)
	}
}

func TestConnectionRefusedSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	g, u := allowServer(t, srv)
	srv.Close()
	s := NewSender(g, "t", 2*time.Second)
	res := s.Send(context.Background(), Request{URL: u, Method: "GET"})
	if res.Err == nil || !strings.Contains(s.Summary(res), "refused") {
		t.Fatalf("summary = %q (err %v)", s.Summary(res), res.Err)
	}
}
