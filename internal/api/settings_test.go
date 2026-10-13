package api

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/config"
	"github.com/abood-lmao/relaybox/internal/storage"
)

func TestSettings(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Host = "0.0.0.0"
		c.AllowRemoteDashboard = true
		c.DashboardToken = testToken
	})
	w := h.do("GET", "/api/v1/settings", nil)
	expectStatus(t, w, 200)
	if strings.Contains(w.Body.String(), testToken) {
		t.Fatal("settings expose the dashboard token")
	}
	s := decode[settingsView](t, w)
	if s.Runtime.RetentionDays != 7 || s.Defaults.RetentionDays != 7 || len(s.Overridden) != 0 {
		t.Fatalf("initial = %+v", s)
	}
	if p, _ := s.Server["database_path"].(string); !filepath.IsAbs(p) {
		t.Errorf("database_path = %v", s.Server["database_path"])
	}
	if s.Server["auth_required"] != true || s.Server["url_replay_enabled"] != false || s.Server["schema_version"] == nil {
		t.Errorf("server = %v", s.Server)
	}

	w = h.do("PATCH", "/api/v1/settings", map[string]any{"retention_days": 3, "redact_fields": []string{"card_number"}})
	expectStatus(t, w, 200)
	s = decode[settingsView](t, w)
	if s.Runtime.RetentionDays != 3 || s.Defaults.RetentionDays != 7 || !slices.Contains(s.Overridden, "retention_days") ||
		!slices.Equal(s.Runtime.RedactFields, []string{"card_number"}) {
		t.Fatalf("patched = %+v", s)
	}

	// Redaction changes apply to the next capture.
	ep := h.createEndpoint("ep", nil)
	h.hook(ep, "POST", "", `{"card_number":"4242","password":"kept"}`, "Content-Type", "application/json")
	body := h.do("GET", "/api/v1/requests/"+itoa(h.latestRequestID(ep)), nil).Body.String()
	if strings.Contains(body, "4242") || !strings.Contains(body, "kept") {
		t.Errorf("runtime redaction not applied: %s", body)
	}

	e := expectError(t, h.do("PATCH", "/api/v1/settings", map[string]any{"retention_days": -1, "max_stored_requests": 5}), 422, "validation_failed")
	if e.Fields["retention_days"] == "" {
		t.Errorf("fields = %v", e.Fields)
	}
	w = h.do("GET", "/api/v1/settings", nil)
	if s := decode[settingsView](t, w); s.Runtime.RetentionDays != 3 || s.Runtime.MaxStoredRequests != 10000 {
		t.Fatalf("failed update changed settings: %+v", s.Runtime)
	}
	expectError(t, h.do("PATCH", "/api/v1/settings", map[string]any{"default_max_body_bytes": 1 << 40}), 422, "validation_failed")
	expectError(t, h.do("PATCH", "/api/v1/settings", map[string]any{"redact_headers": []string{"bad header"}}), 422, "validation_failed")
	expectError(t, h.do("PATCH", "/api/v1/settings", map[string]any{"theme": "dark"}), 400, "invalid_json")

	w = h.do("PATCH", "/api/v1/settings", map[string]any{"reset": []string{"retention_days", "redact_fields"}})
	expectStatus(t, w, 200)
	if s := decode[settingsView](t, w); s.Runtime.RetentionDays != 7 || len(s.Overridden) != 0 {
		t.Fatalf("reset = %+v", s)
	}
}

func TestSettingsPersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persist.db")
	ctx := context.Background()

	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	h := harnessWithDB(t, db)
	expectStatus(t, h.do("PATCH", "/api/v1/settings", map[string]any{"max_stored_requests": 50}), 200)
	db.Close()

	db2, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db2.Close() })
	h2 := harnessWithDB(t, db2)
	w := h2.do("GET", "/api/v1/settings", nil)
	if s := decode[settingsView](t, w); s.Runtime.MaxStoredRequests != 50 {
		t.Fatalf("after restart: %+v", s.Runtime)
	}
}

func TestCleanup(t *testing.T) {
	h := newHarness(t)
	ep := h.createEndpoint("old", nil)
	stored, err := h.db.GetEndpoint(context.Background(), ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if _, _, err := h.cap.Ingest(context.Background(), stored, capture.Inbound{Method: "POST", Path: "/", Source: storage.SourceCapture, ReceivedAt: old}); err != nil {
		t.Fatal(err)
	}
	h.hook(ep, "POST", "", "{}")

	w := h.do("POST", "/api/v1/maintenance/cleanup", nil)
	expectStatus(t, w, 200)
	if res := decode[map[string]int](t, w); res["expired"] != 0 {
		t.Fatalf("7-day retention removed a 3-day-old request: %v", res)
	}
	expectStatus(t, h.do("PATCH", "/api/v1/settings", map[string]any{"retention_days": 1}), 200)
	w = h.do("POST", "/api/v1/maintenance/cleanup", nil)
	expectStatus(t, w, 200)
	if res := decode[map[string]int](t, w); res["expired"] != 1 {
		t.Fatalf("cleanup = %v", res)
	}
	if p := h.list(""); p.Total != 1 {
		t.Fatalf("%d requests left", p.Total)
	}
}
