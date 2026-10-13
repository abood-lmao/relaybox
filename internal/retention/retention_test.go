package retention

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
)

type counter struct{ n int }

func (c *counter) Publish(string, any) { c.n++ }

func setup(t *testing.T, days, max int) (*storage.DB, *Runner, *counter, int64) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "ret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	set, _, err := settings.New(context.Background(), db, settings.Values{
		RetentionDays: days, MaxStoredRequests: max, DefaultMaxBodyBytes: 1024,
	}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := db.CreateEndpoint(context.Background(), storage.NewEndpoint{Name: "e", Enabled: true, Mock: mockresp.Default()})
	if err != nil {
		t.Fatal(err)
	}
	c := &counter{}
	return db, New(db, set, c, nil), c, ep.ID
}

func insert(t *testing.T, db *storage.DB, endpointID int64, at time.Time) {
	t.Helper()
	err := db.InsertRequest(context.Background(), &storage.CapturedRequest{
		EndpointID: endpointID, Method: "POST", Path: "/", ResponseStatus: 200, ReceivedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunOnceRemovesOnlyExpiredRequests(t *testing.T) {
	db, r, events, ep := setup(t, 7, 0)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	insert(t, db, ep, now.Add(-8*24*time.Hour))
	insert(t, db, ep, now.Add(-7*24*time.Hour-time.Minute))
	insert(t, db, ep, now.Add(-6*24*time.Hour))
	insert(t, db, ep, now)

	plan, err := r.Plan(context.Background())
	if err != nil || plan.Expired != 2 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if n, _ := db.CountRequests(context.Background()); n != 4 {
		t.Fatal("Plan must not delete anything")
	}
	res, err := r.RunOnce(context.Background())
	if err != nil || res.Expired != 2 || res.Trimmed != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if n, _ := db.CountRequests(context.Background()); n != 2 {
		t.Fatalf("remaining = %d", n)
	}
	if events.n != 1 {
		t.Fatalf("events = %d", events.n)
	}
}

func TestRunOnceEnforcesStoredLimit(t *testing.T) {
	db, r, _, ep := setup(t, 0, 3)
	now := time.Now()
	for i := 0; i < 5; i++ {
		insert(t, db, ep, now.Add(time.Duration(i)*time.Second))
	}
	plan, _ := r.Plan(context.Background())
	if plan.Trimmed != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	res, err := r.RunOnce(context.Background())
	if err != nil || res.Trimmed != 2 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	ids, _ := db.LatestRequestIDs(context.Background(), 0, 10)
	if len(ids) != 3 {
		t.Fatalf("remaining %d", len(ids))
	}
}

func TestZeroMeansKeepForever(t *testing.T) {
	db, r, events, ep := setup(t, 0, 0)
	insert(t, db, ep, time.Now().Add(-1000*24*time.Hour))
	res, err := r.RunOnce(context.Background())
	if err != nil || res.Expired != 0 || events.n != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestRunStopsWithContext(t *testing.T) {
	_, r, _, _ := setup(t, 1, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	r.Nudge()
	r.Nudge() // must not block
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
