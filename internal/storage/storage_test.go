package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abood-lmao/relaybox/internal/ids"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/redact"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustEndpoint(t *testing.T, db *DB, name string) Endpoint {
	t.Helper()
	e, err := db.CreateEndpoint(context.Background(), NewEndpoint{Name: name, Enabled: true, Mock: mockresp.Default()})
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	return e
}

func mustRequest(t *testing.T, db *DB, endpointID int64, mutate func(*CapturedRequest)) *CapturedRequest {
	t.Helper()
	req := &CapturedRequest{
		EndpointID:     endpointID,
		Method:         "POST",
		Path:           "/",
		Headers:        map[string][]string{"Content-Type": {"application/json"}},
		Body:           []byte(`{"event":"test"}`),
		BodySize:       16,
		ContentType:    "application/json",
		ResponseStatus: 200,
		ReceivedAt:     time.Now(),
		Duration:       1500 * time.Microsecond,
	}
	if mutate != nil {
		mutate(req)
	}
	if err := db.InsertRequest(context.Background(), req); err != nil {
		t.Fatalf("InsertRequest: %v", err)
	}
	return req
}

func TestEndpointLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	e := mustEndpoint(t, db, "Stripe")
	if !ids.IsPublicID(e.PublicID) || e.ID == 0 || !e.Enabled {
		t.Fatalf("unexpected endpoint %+v", e)
	}
	if e.Mock.Status != 200 || e.Mock.Body != mockresp.DefaultBody {
		t.Fatalf("mock not stored: %+v", e.Mock)
	}

	got, err := db.GetEndpointByPublicID(ctx, e.PublicID)
	if err != nil || got.ID != e.ID {
		t.Fatalf("GetEndpointByPublicID = %+v, %v", got, err)
	}

	name := "Stripe (test mode)"
	disabled := false
	limit := int64(4096)
	mock := mockresp.Config{Status: 202, ContentType: "text/plain", Body: "ok", Headers: map[string]string{"X-A": "1"}}
	updated, err := db.UpdateEndpoint(ctx, e.ID, EndpointUpdate{
		Name: &name, Enabled: &disabled, Mock: &mock, SetMaxBodyBytes: true, MaxBodyBytes: &limit,
	})
	if err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}
	if updated.Name != name || updated.Enabled || updated.Mock.Status != 202 || updated.Mock.Headers["X-A"] != "1" ||
		updated.MaxBodyBytes == nil || *updated.MaxBodyBytes != 4096 {
		t.Fatalf("update not applied: %+v", updated)
	}

	cleared, err := db.UpdateEndpoint(ctx, e.ID, EndpointUpdate{SetMaxBodyBytes: true})
	if err != nil || cleared.MaxBodyBytes != nil {
		t.Fatalf("clearing limit failed: %+v, %v", cleared, err)
	}

	rotated, err := db.RotateEndpointPublicID(ctx, e.ID)
	if err != nil || rotated.PublicID == e.PublicID || !ids.IsPublicID(rotated.PublicID) {
		t.Fatalf("rotate failed: %+v, %v", rotated, err)
	}
	if _, err := db.GetEndpointByPublicID(ctx, e.PublicID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old identifier should stop resolving, got %v", err)
	}

	if _, err := db.UpdateEndpoint(ctx, 9999, EndpointUpdate{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of missing endpoint: %v", err)
	}
}

func TestEndpointNamesAreUniqueIgnoringCase(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	mustEndpoint(t, db, "GitHub")
	other := mustEndpoint(t, db, "Stripe")

	if _, err := db.CreateEndpoint(ctx, NewEndpoint{Name: "github", Mock: mockresp.Default()}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("create duplicate: %v", err)
	}
	name := "GITHUB"
	if _, err := db.UpdateEndpoint(ctx, other.ID, EndpointUpdate{Name: &name}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("rename to duplicate: %v", err)
	}
}

func TestDeleteEndpointCascades(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	a := mustEndpoint(t, db, "A")
	b := mustEndpoint(t, db, "B")
	r1 := mustRequest(t, db, a.ID, nil)
	mustRequest(t, db, a.ID, nil)
	kept := mustRequest(t, db, b.ID, nil)
	if err := db.InsertReplayAttempt(ctx, &ReplayAttempt{RequestID: r1.ID, Target: ReplayTargetURL, Destination: "https://example.com/",
		DestinationHost: "example.com", Method: "POST", Outcome: OutcomeSuccess, Summary: "HTTP 200", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	n, err := db.DeleteEndpoint(ctx, a.ID)
	if err != nil || n != 2 {
		t.Fatalf("DeleteEndpoint = %d, %v", n, err)
	}
	if _, err := db.GetRequest(ctx, r1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("request of deleted endpoint still present: %v", err)
	}
	if _, err := db.GetRequest(ctx, kept.ID); err != nil {
		t.Fatalf("other endpoint's request was deleted: %v", err)
	}
	attempts, err := db.ListReplayAttempts(ctx, r1.ID, 10)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("replay attempts should cascade: %v %v", attempts, err)
	}
	if _, err := db.DeleteEndpoint(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestRequestRoundTripPreservesBinaryAndMetadata(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	e := mustEndpoint(t, db, "Binary")
	cl := int64(4)
	in := mustRequest(t, db, e.ID, func(r *CapturedRequest) {
		r.Method = "PUT"
		r.Path = "/files/upload"
		r.RawQuery = "a=1&token=%5BREDACTED%5D"
		r.Query = []redact.Pair{{"a", "1"}, {"token", redact.Placeholder}}
		r.Body = []byte{0x00, 0xff, 0xfe, 0x10}
		r.BodySize = 4
		r.ContentType = "application/octet-stream"
		r.ContentLength = &cl
		r.RemoteAddr = "203.0.113.9"
		r.ResponseHeaders = map[string][]string{"X-Mock": {"1"}}
		r.ResponseBody = []byte("ok")
		r.Redactions = []redact.Redaction{{Location: "query", Name: "token"}}
	})

	got, err := db.GetRequest(ctx, in.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if string(got.Body) != string(in.Body) || got.ContentLength == nil || *got.ContentLength != 4 {
		t.Fatalf("body/content length mismatch: %+v", got)
	}
	if got.EndpointName != "Binary" || got.EndpointPublicID != e.PublicID || got.Source != SourceCapture {
		t.Fatalf("joined fields wrong: %+v", got)
	}
	if len(got.Query) != 2 || got.Query[1][1] != redact.Placeholder || got.RawQuery != in.RawQuery {
		t.Fatalf("query mismatch: %+v", got.Query)
	}
	if got.Duration != 1500*time.Microsecond || got.ResponseHeaders["X-Mock"][0] != "1" || len(got.Redactions) != 1 {
		t.Fatalf("metadata mismatch: %+v", got)
	}
}

func TestListRequestsFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	a := mustEndpoint(t, db, "A")
	b := mustEndpoint(t, db, "B")
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 30; i++ {
		ep := a.ID
		if i%3 == 0 {
			ep = b.ID
		}
		mustRequest(t, db, ep, func(r *CapturedRequest) {
			r.ReceivedAt = base.Add(time.Duration(i) * time.Minute)
			r.Method = []string{"POST", "GET", "DELETE"}[i%3]
			r.ResponseStatus = []int{200, 404, 500, 201, 302}[i%5]
			r.Body = []byte(fmt.Sprintf(`{"n":%d,"tag":"item-%d"}`, i, i))
			r.BodySize = int64(len(r.Body))
			r.Duration = time.Duration(i) * time.Millisecond
		})
	}

	page, total, err := db.ListRequests(ctx, RequestFilter{Limit: 10})
	if err != nil || total != 30 || len(page) != 10 {
		t.Fatalf("first page = %d rows, total %d, err %v", len(page), total, err)
	}
	if !page[0].ReceivedAt.Equal(base.Add(29 * time.Minute)) {
		t.Fatalf("default order should be newest first, got %v", page[0].ReceivedAt)
	}
	page3, _, _ := db.ListRequests(ctx, RequestFilter{Limit: 10, Offset: 20})
	if len(page3) != 10 || page3[9].ReceivedAt != base {
		t.Fatalf("last page wrong: %d rows", len(page3))
	}

	_, total, _ = db.ListRequests(ctx, RequestFilter{EndpointID: b.ID})
	if total != 10 {
		t.Errorf("endpoint filter total = %d, want 10", total)
	}
	_, total, _ = db.ListRequests(ctx, RequestFilter{Methods: []string{"GET", "DELETE"}})
	if total != 20 {
		t.Errorf("method filter total = %d, want 20", total)
	}
	_, total, _ = db.ListRequests(ctx, RequestFilter{Statuses: []StatusMatch{{Class: 2}}})
	if total != 12 {
		t.Errorf("2xx filter total = %d, want 12", total)
	}
	_, total, _ = db.ListRequests(ctx, RequestFilter{Statuses: []StatusMatch{{Code: 404}, {Class: 5}}})
	if total != 12 {
		t.Errorf("404 or 5xx total = %d, want 12", total)
	}
	from, to := base.Add(10*time.Minute), base.Add(19*time.Minute)
	_, total, _ = db.ListRequests(ctx, RequestFilter{From: &from, To: &to})
	if total != 10 {
		t.Errorf("date range total = %d, want 10", total)
	}
	rows, total, _ := db.ListRequests(ctx, RequestFilter{Search: "item-17"})
	if total != 1 || rows[0].Method != "DELETE" {
		t.Errorf("search found %d rows", total)
	}
	rows, _, _ = db.ListRequests(ctx, RequestFilter{Sort: "duration", Ascending: true, Limit: 1})
	if len(rows) != 1 || rows[0].Duration != 0 {
		t.Errorf("duration sort wrong: %+v", rows)
	}
}

func TestSearchTreatsInputAsData(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	e := mustEndpoint(t, db, "A")
	mustRequest(t, db, e.ID, func(r *CapturedRequest) { r.Body = []byte(`{"discount":"100%_off"}`) })
	mustRequest(t, db, e.ID, func(r *CapturedRequest) { r.Body = []byte(`{"discount":"1000 off"}`) })

	for term, want := range map[string]int64{
		`' OR 1=1 --`:                         0,
		`"; DROP TABLE captured_requests; --`: 0,
		`\`:                                   0,
		// Only the first body contains these characters literally; as LIKE
		// wildcards they would match both rows.
		`%`: 1,
		`_`: 1,
	} {
		rows, total, err := db.ListRequests(ctx, RequestFilter{Search: term})
		if err != nil {
			t.Fatalf("search %q failed: %v", term, err)
		}
		if total != want || int64(len(rows)) != want {
			t.Errorf("search %q matched %d rows, want %d", term, total, want)
		}
	}
	_, total, _ := db.ListRequests(ctx, RequestFilter{Search: "100%_off"})
	if total != 1 {
		t.Errorf("literal %% and _ search matched %d rows, want 1", total)
	}
	if n, _ := db.CountRequests(ctx); n != 2 {
		t.Fatalf("table damaged, %d rows", n)
	}
}

func TestBulkDeleteRespectsScope(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	a := mustEndpoint(t, db, "A")
	b := mustEndpoint(t, db, "B")
	r1 := mustRequest(t, db, a.ID, nil)
	r2 := mustRequest(t, db, a.ID, nil)
	mustRequest(t, db, b.ID, nil)

	if _, err := db.DeleteRequests(ctx, DeleteScope{}); err == nil {
		t.Fatal("empty scope must be rejected")
	}
	n, err := db.DeleteRequests(ctx, DeleteScope{IDs: []int64{r1.ID}})
	if err != nil || n != 1 {
		t.Fatalf("delete by id = %d, %v", n, err)
	}
	n, err = db.DeleteRequests(ctx, DeleteScope{EndpointID: b.ID})
	if err != nil || n != 1 {
		t.Fatalf("delete by endpoint = %d, %v", n, err)
	}
	if _, err := db.GetRequest(ctx, r2.ID); err != nil {
		t.Fatalf("request outside scope deleted: %v", err)
	}
	n, err = db.DeleteRequests(ctx, DeleteScope{All: true})
	if err != nil || n != 1 {
		t.Fatalf("delete all = %d, %v", n, err)
	}
	if err := db.DeleteRequest(ctx, r2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteRequest on missing row: %v", err)
	}
}

func TestRetentionDeletesOnlyOldRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	e := mustEndpoint(t, db, "A")
	now := time.Now()
	for i := 0; i < 1200; i++ {
		mustRequest(t, db, e.ID, func(r *CapturedRequest) { r.ReceivedAt = now.Add(-10 * 24 * time.Hour) })
	}
	fresh := mustRequest(t, db, e.ID, func(r *CapturedRequest) { r.ReceivedAt = now.Add(-time.Hour) })

	n, err := db.DeleteRequestsBefore(ctx, now.Add(-7*24*time.Hour))
	if err != nil || n != 1200 {
		t.Fatalf("DeleteRequestsBefore = %d, %v", n, err)
	}
	if _, err := db.GetRequest(ctx, fresh.ID); err != nil {
		t.Fatalf("fresh request deleted: %v", err)
	}
}

func TestTrimRequestsKeepsNewest(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	e := mustEndpoint(t, db, "A")
	base := time.Now()
	var newest *CapturedRequest
	for i := 0; i < 20; i++ {
		newest = mustRequest(t, db, e.ID, func(r *CapturedRequest) { r.ReceivedAt = base.Add(time.Duration(i) * time.Second) })
	}
	if _, err := db.TrimRequests(ctx, 0); err == nil {
		t.Fatal("TrimRequests(0) must refuse instead of deleting everything")
	}
	n, err := db.TrimRequests(ctx, 5)
	if err != nil || n != 15 {
		t.Fatalf("TrimRequests = %d, %v", n, err)
	}
	if _, err := db.GetRequest(ctx, newest.ID); err != nil {
		t.Fatalf("newest request removed: %v", err)
	}
}

func TestDataSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "persist.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	e := mustEndpoint(t, db, "Persistent")
	req := mustRequest(t, db, e.ID, nil)
	if err := db.SaveSettings(ctx, map[string]string{"retention_days": "3"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	if _, err := db2.GetRequest(ctx, req.ID); err != nil {
		t.Fatalf("request lost after reopen: %v", err)
	}
	settings, _ := db2.LoadSettings(ctx)
	if settings["retention_days"] != "3" {
		t.Fatalf("settings lost: %v", settings)
	}
	v, err := db2.SchemaVersion(ctx)
	if err != nil || v != 1 {
		t.Fatalf("schema version = %d, %v", v, err)
	}
}

func TestNewerSchemaIsRejected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.w.Exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future', 0)"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("expected newer-schema error, got %v", err)
	}
}

func TestStatsUseRealData(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	a := mustEndpoint(t, db, "Busy")
	b := mustEndpoint(t, db, "Quiet")
	now := time.Date(2026, 3, 1, 15, 30, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		mustRequest(t, db, a.ID, func(r *CapturedRequest) {
			r.ReceivedAt = now.Add(-time.Duration(i) * time.Hour)
			r.ResponseStatus = 200
		})
	}
	mustRequest(t, db, b.ID, func(r *CapturedRequest) { r.ReceivedAt = now.Add(-48 * time.Hour); r.ResponseStatus = 503 })

	s, err := db.Stats(ctx, now)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.TotalRequests != 5 || s.Last24h != 4 || s.Endpoints != 2 {
		t.Fatalf("totals wrong: %+v", s)
	}
	if s.StatusClasses["2xx"] != 4 || s.StatusClasses["5xx"] != 1 {
		t.Fatalf("status classes wrong: %v", s.StatusClasses)
	}
	if len(s.Hourly) != 24 || s.Hourly[23].Count != 1 || s.Hourly[20].Count != 1 {
		t.Fatalf("hourly buckets wrong: %+v", s.Hourly)
	}
	var sum int64
	for _, h := range s.Hourly {
		sum += h.Count
	}
	if sum != 4 {
		t.Fatalf("hourly sum = %d, want 4", sum)
	}
	if len(s.TopEndpoints) != 2 || s.TopEndpoints[0].Name != "Busy" || s.TopEndpoints[0].Last24h != 4 {
		t.Fatalf("top endpoints wrong: %+v", s.TopEndpoints)
	}
}
