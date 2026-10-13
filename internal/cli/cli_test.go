package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/fixture"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
)

type result struct {
	code           int
	stdout, stderr string
}

func run(t *testing.T, environ []string, args ...string) result {
	t.Helper()
	return runCtx(t, context.Background(), environ, args...)
}

func runCtx(t *testing.T, ctx context.Context, environ []string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(ctx, args, environ, &out, &errb)
	return result{code, out.String(), errb.String()}
}

func dbEnv(path string) []string { return []string{"RELAYBOX_DATABASE_PATH=" + path} }

// seed creates a database with one endpoint and the given requests, which
// are stored with the given ages.
func seed(t *testing.T, path string, ages ...time.Duration) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	set, _, err := settings.New(ctx, db, settings.Values{
		RetentionDays: 7, MaxStoredRequests: 10000, DefaultMaxBodyBytes: 1 << 20,
		RedactHeaders: []string{"Authorization"}, RedactFields: []string{"password"},
	}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := db.CreateEndpoint(ctx, storage.NewEndpoint{Name: "Payments", Enabled: true, Mock: mockresp.Default()})
	if err != nil {
		t.Fatal(err)
	}
	c := capture.New(capture.Options{DB: db, Settings: set})
	for i, age := range ages {
		_, _, err := c.Ingest(ctx, ep, capture.Inbound{
			Method: "POST", Path: "/event", Source: storage.SourceCapture,
			Header:     map[string][]string{"Content-Type": {"application/json"}, "Authorization": {"Bearer live-key"}},
			Body:       []byte(`{"n":` + strconv.Itoa(i) + `,"password":"pw"}`),
			ReceivedAt: time.Now().Add(-age),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHelpAndVersion(t *testing.T) {
	if r := run(t, nil); r.code != 0 || !strings.Contains(r.stdout, "Commands:") {
		t.Errorf("no args: %+v", r)
	}
	if r := run(t, nil, "help"); r.code != 0 || !strings.Contains(r.stdout, "serve") {
		t.Errorf("help: %+v", r)
	}
	if r := run(t, nil, "help", "serve"); r.code != 0 || !strings.Contains(r.stdout, "--port") {
		t.Errorf("help serve: %+v", r)
	}
	if r := run(t, nil, "serve", "--help"); r.code != 0 || !strings.Contains(r.stdout, "--database") {
		t.Errorf("serve --help: %+v", r)
	}
	if r := run(t, nil, "help", "nope"); r.code != 2 || !strings.Contains(r.stderr, "unknown command") {
		t.Errorf("help nope: %+v", r)
	}
	if r := run(t, nil, "version"); r.code != 0 || !strings.HasPrefix(r.stdout, "relaybox ") {
		t.Errorf("version: %+v", r)
	}
	if r := run(t, nil, "launch"); r.code != 2 || !strings.Contains(r.stderr, `unknown command "launch"`) || r.stdout != "" {
		t.Errorf("unknown: %+v", r)
	}
}

func TestUsageErrors(t *testing.T) {
	if r := run(t, nil, "serve", "--bogus"); r.code != 2 || !strings.Contains(r.stderr, "bogus") {
		t.Errorf("bad flag: %+v", r)
	}
	if r := run(t, nil, "serve", "extra"); r.code != 2 {
		t.Errorf("extra arg: %+v", r)
	}
	if r := run(t, []string{"RELAYBOX_PORT=abc", "RELAYBOX_LOG_FORMAT=xml"}, "serve"); r.code != 2 ||
		!strings.Contains(r.stderr, "RELAYBOX_PORT") || !strings.Contains(r.stderr, "RELAYBOX_LOG_FORMAT") {
		t.Errorf("invalid env: %+v", r)
	}
	if r := run(t, nil, "serve", "--port", "70000"); r.code != 2 || !strings.Contains(r.stderr, "RELAYBOX_PORT") {
		t.Errorf("invalid flag value: %+v", r)
	}
	if r := run(t, []string{"RELAYBOX_ALLOW_REMOTE_DASHBOARD=true"}, "serve"); r.code != 2 || !strings.Contains(r.stderr, "RELAYBOX_DASHBOARD_TOKEN") {
		t.Errorf("remote without token: %+v", r)
	}
	if r := run(t, nil, "export", "--limit", "0"); r.code != 2 {
		t.Errorf("export limit: %+v", r)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestServeStartsAndStops(t *testing.T) {
	port := freePort(t)
	dir := t.TempDir()
	environ := []string{
		"RELAYBOX_PORT=" + strconv.Itoa(port),
		"RELAYBOX_DATABASE_PATH=" + filepath.Join(dir, "data", "relaybox.db"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- Main(ctx, []string{"serve"}, environ, &stdout, &stderr) }()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := checkHealth(context.Background(), base+"/healthz"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start; stderr:\n%s", stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r := run(t, environ, "healthcheck"); r.code != 0 {
		t.Errorf("healthcheck: %+v", r)
	}
	if r := run(t, environ, "doctor"); r.code != 0 || !strings.Contains(r.stdout, "already running") {
		t.Errorf("doctor while running: %+v", r)
	}

	// A second server on the same port fails with a clear message.
	r := run(t, environ, "serve")
	if r.code != 1 || !strings.Contains(r.stderr, "already in use") {
		t.Errorf("second serve: %+v", r)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d; stderr:\n%s", code, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop")
	}
	logs := stderr.String()
	if !strings.Contains(logs, "relaybox started") || !strings.Contains(logs, "stopped") {
		t.Errorf("logs:\n%s", logs)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "relaybox.db")); err != nil {
		t.Errorf("database not created: %v", err)
	}
}

func TestCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	if r := run(t, dbEnv(path), "cleanup"); r.code != 1 || !strings.Contains(r.stderr, "no database") {
		t.Fatalf("missing db: %+v", r)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("cleanup created a database")
	}

	seed(t, path, 10*24*time.Hour, 9*24*time.Hour, time.Hour)
	r := run(t, dbEnv(path), "cleanup", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "Would delete 2 requests (2 expired, 0 over the limit)") ||
		!strings.Contains(r.stdout, "keep requests for 7 days") {
		t.Fatalf("dry run: %+v", r)
	}
	r = run(t, dbEnv(path), "cleanup", "--database", path)
	if r.code != 0 || !strings.Contains(r.stdout, "Deleted 2 requests") {
		t.Fatalf("cleanup: %+v", r)
	}
	if r := run(t, dbEnv(path), "cleanup"); r.code != 0 || !strings.Contains(r.stdout, "Nothing to delete") {
		t.Fatalf("second cleanup: %+v", r)
	}
	r = run(t, append(dbEnv(path), "RELAYBOX_RETENTION_DAYS=0", "RELAYBOX_MAX_STORED_REQUESTS=0"), "cleanup")
	if r.code != 0 || !strings.Contains(r.stdout, "Retention is off") {
		t.Fatalf("retention off: %+v", r)
	}
	r = run(t, append(dbEnv(path), "RELAYBOX_RETENTION_DAYS=0", "RELAYBOX_MAX_STORED_REQUESTS=0"), "cleanup", "--bogus")
	if r.code != 2 {
		t.Fatalf("bad flag: %+v", r)
	}
}

func TestExport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	seed(t, path, 3*time.Hour, 2*time.Hour, time.Hour)

	r := run(t, dbEnv(path), "export", "--endpoint", "payments", "--limit", "2")
	if r.code != 0 {
		t.Fatalf("export: %+v", r)
	}
	var f fixture.File
	if err := json.Unmarshal([]byte(r.stdout), &f); err != nil {
		t.Fatalf("stdout is not a fixture: %v\n%s", err, r.stdout)
	}
	if f.Format != fixture.Format || len(f.Requests) != 2 {
		t.Fatalf("fixture = %+v", f)
	}
	if !strings.Contains(f.Requests[0].Body.Data, `"n":1`) || !strings.Contains(f.Requests[1].Body.Data, `"n":2`) {
		t.Errorf("expected the two newest requests, oldest first: %+v", f.Requests)
	}
	if strings.Contains(r.stdout, "live-key") || strings.Contains(r.stdout, `"pw"`) {
		t.Errorf("export contains secrets:\n%s", r.stdout)
	}
	if _, err := fixture.Parse([]byte(r.stdout), 1<<20); err != nil {
		t.Errorf("exported fixture does not parse: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.json")
	r = run(t, dbEnv(path), "export", "--endpoint", "1", "--out", out, "--no-bodies")
	if r.code != 0 || !strings.Contains(r.stderr, "Exported 3 requests") {
		t.Fatalf("export to file: %+v", r)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	f = fixture.File{}
	_ = json.Unmarshal(data, &f)
	if len(f.Requests) != 3 || f.Requests[0].Body != nil || !f.Requests[0].BodyOmitted {
		t.Fatalf("no-bodies export = %+v", f.Requests)
	}

	if r := run(t, dbEnv(path), "export", "--endpoint", "Shipping"); r.code != 1 || !strings.Contains(r.stderr, "Shipping") {
		t.Errorf("unknown endpoint: %+v", r)
	}
	if r := run(t, dbEnv(filepath.Join(t.TempDir(), "none.db")), "export"); r.code != 1 {
		t.Errorf("missing db: %+v", r)
	}
}

func TestDoctor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	port := strconv.Itoa(freePort(t))
	r := run(t, dbEnv(path), "doctor", "--port", port)
	if r.code != 0 || !strings.Contains(r.stdout, "does not exist yet") || !strings.Contains(r.stdout, "is free") {
		t.Fatalf("fresh doctor: %+v", r)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("doctor created the database")
	}

	seed(t, path, time.Hour)
	r = run(t, dbEnv(path), "doctor", "--port", port)
	if r.code != 0 || !strings.Contains(r.stdout, "integrity ok") || !strings.Contains(r.stdout, "1 stored request,") {
		t.Fatalf("doctor: %+v", r)
	}
	if !strings.Contains(r.stdout, "only accept clients on this machine") {
		t.Errorf("security summary missing: %s", r.stdout)
	}

	// A port held by something that is not Relaybox is a failure.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	r = run(t, dbEnv(path), "doctor", "--port", busy)
	if r.code != 1 || !strings.Contains(r.stdout, "fail") {
		t.Fatalf("busy port: %+v", r)
	}

	if err := os.WriteFile(path, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	r = run(t, dbEnv(path), "doctor", "--port", port)
	if r.code != 1 || !strings.Contains(r.stdout, "fail  database") {
		t.Fatalf("corrupt database: %+v", r)
	}
	if r := run(t, []string{"RELAYBOX_PORT=0"}, "doctor"); r.code != 2 {
		t.Errorf("invalid config: %+v", r)
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ok.Close()
	if r := run(t, nil, "healthcheck", "--url", ok.URL+"/healthz"); r.code != 0 {
		t.Errorf("healthy: %+v", r)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if r := run(t, nil, "healthcheck", "--url", bad.URL); r.code != 1 || !strings.Contains(r.stderr, "503") {
		t.Errorf("unhealthy: %+v", r)
	}
	port := strconv.Itoa(freePort(t))
	if r := run(t, []string{"RELAYBOX_PORT=" + port}, "healthcheck"); r.code != 1 {
		t.Errorf("nothing listening: %+v", r)
	}
}

func TestConnectHost(t *testing.T) {
	cases := map[string]string{"0.0.0.0": "127.0.0.1", "::": "::1", "127.0.0.1": "127.0.0.1", "example.com": "example.com", "10.1.2.3": "10.1.2.3"}
	for in, want := range cases {
		if got := connectHost(in); got != want {
			t.Errorf("connectHost(%q) = %q, want %q", in, got, want)
		}
	}
}
