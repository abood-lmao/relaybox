// Package retention deletes captured requests that fall outside the
// configured retention policy: requests older than the retention period,
// and the oldest requests beyond the stored request limit.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
)

// EventRequestsDeleted is published when requests are removed.
const EventRequestsDeleted = "requests.deleted"

const (
	defaultInterval = 10 * time.Minute
	nudgeDebounce   = 5 * time.Second
)

// Publisher receives notifications about deletions.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Result reports what a cleanup removed (or would remove, for a plan).
type Result struct {
	Expired int64 `json:"expired"`
	Trimmed int64 `json:"trimmed"`
}

// Runner applies the retention policy periodically.
type Runner struct {
	db       *storage.DB
	settings *settings.Service
	events   Publisher
	log      *slog.Logger
	interval time.Duration
	nudge    chan struct{}
	now      func() time.Time
}

// New returns a runner. events and log may be nil.
func New(db *storage.DB, set *settings.Service, events Publisher, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Runner{
		db: db, settings: set, events: events, log: log,
		interval: defaultInterval, nudge: make(chan struct{}, 1), now: time.Now,
	}
}

// RunOnce applies the policy immediately.
func (r *Runner) RunOnce(ctx context.Context) (Result, error) {
	cur := r.settings.Current()
	var res Result
	if cur.RetentionDays > 0 {
		cutoff := r.now().Add(-time.Duration(cur.RetentionDays) * 24 * time.Hour)
		n, err := r.db.DeleteRequestsBefore(ctx, cutoff)
		res.Expired = n
		if err != nil {
			return res, err
		}
	}
	if cur.MaxStoredRequests > 0 {
		n, err := r.db.TrimRequests(ctx, cur.MaxStoredRequests)
		res.Trimmed = n
		if err != nil {
			return res, err
		}
	}
	if total := res.Expired + res.Trimmed; total > 0 {
		if r.events != nil {
			r.events.Publish(EventRequestsDeleted, map[string]any{"count": total, "reason": "retention"})
		}
		if err := r.db.Vacuum(ctx); err != nil {
			r.log.Warn("incremental vacuum failed", "err", err)
		}
		r.log.Info("retention cleanup", "expired", res.Expired, "trimmed", res.Trimmed)
	}
	return res, nil
}

// Plan reports what RunOnce would delete, without deleting anything.
func (r *Runner) Plan(ctx context.Context) (Result, error) {
	cur := r.settings.Current()
	var res Result
	if cur.RetentionDays > 0 {
		cutoff := r.now().Add(-time.Duration(cur.RetentionDays) * 24 * time.Hour)
		n, err := r.db.CountRequestsBefore(ctx, cutoff)
		if err != nil {
			return res, err
		}
		res.Expired = n
	}
	if cur.MaxStoredRequests > 0 {
		total, err := r.db.CountRequests(ctx)
		if err != nil {
			return res, err
		}
		if remaining := total - res.Expired; remaining > int64(cur.MaxStoredRequests) {
			res.Trimmed = remaining - int64(cur.MaxStoredRequests)
		}
	}
	return res, nil
}

// Nudge asks the runner to check soon, for example after new requests were
// stored. It never blocks.
func (r *Runner) Nudge() {
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

// Run applies the policy at startup, then periodically and when nudged,
// until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	run := func() {
		if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("retention cleanup failed", "err", err)
		}
	}
	run()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
			last = r.now()
		case <-r.nudge:
			if wait := nudgeDebounce - r.now().Sub(last); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
			run()
			last = r.now()
		}
	}
}
