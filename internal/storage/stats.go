package storage

import (
	"context"
	"time"
)

// HourBucket counts requests received during one hour.
type HourBucket struct {
	Start time.Time
	Count int64
}

// EndpointActivity summarizes traffic for one endpoint.
type EndpointActivity struct {
	ID       int64
	Name     string
	Enabled  bool
	Last24h  int64
	Total    int64
	LastSeen *time.Time
}

// Stats is the data behind the overview page. Every number comes from the
// captured_requests table.
type Stats struct {
	TotalRequests int64
	Last24h       int64
	Endpoints     int64
	StatusClasses map[string]int64 // "2xx" ... "5xx"
	Hourly        []HourBucket     // 24 buckets, oldest first
	TopEndpoints  []EndpointActivity
}

// Stats computes overview statistics relative to now.
func (db *DB) Stats(ctx context.Context, now time.Time) (Stats, error) {
	var s Stats
	since := now.Add(-24 * time.Hour)

	if err := db.r.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN received_at >= ? THEN 1 ELSE 0 END), 0) FROM captured_requests`,
		millis(since)).Scan(&s.TotalRequests, &s.Last24h); err != nil {
		return s, err
	}
	if err := db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM endpoints").Scan(&s.Endpoints); err != nil {
		return s, err
	}

	s.StatusClasses = map[string]int64{"2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0}
	rows, err := db.r.QueryContext(ctx, `SELECT response_status / 100, COUNT(*) FROM captured_requests
		WHERE response_status BETWEEN 200 AND 599 GROUP BY response_status / 100`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var class int
		var n int64
		if err := rows.Scan(&class, &n); err != nil {
			rows.Close()
			return s, err
		}
		s.StatusClasses[string(rune('0'+class))+"xx"] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}

	// Hourly buckets aligned to the hour, covering the last 24 hours.
	const hourMS = int64(time.Hour / time.Millisecond)
	currentHour := millis(now) / hourMS
	firstHour := currentHour - 23
	counts := make(map[int64]int64)
	rows, err = db.r.QueryContext(ctx, `SELECT received_at / ?, COUNT(*) FROM captured_requests
		WHERE received_at >= ? GROUP BY received_at / ?`, hourMS, firstHour*hourMS, hourMS)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var h, n int64
		if err := rows.Scan(&h, &n); err != nil {
			rows.Close()
			return s, err
		}
		counts[h] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}
	for h := firstHour; h <= currentHour; h++ {
		s.Hourly = append(s.Hourly, HourBucket{Start: fromMillis(h * hourMS), Count: counts[h]})
	}

	rows, err = db.r.QueryContext(ctx, `SELECT e.id, e.name, e.enabled,
		COALESCE(SUM(CASE WHEN r.received_at >= ? THEN 1 ELSE 0 END), 0) AS recent,
		COUNT(r.id) AS total, MAX(r.received_at)
		FROM endpoints e JOIN captured_requests r ON r.endpoint_id = e.id
		GROUP BY e.id ORDER BY recent DESC, total DESC, e.id DESC LIMIT 5`, millis(since))
	if err != nil {
		return s, err
	}
	defer rows.Close()
	s.TopEndpoints = []EndpointActivity{}
	for rows.Next() {
		var (
			a       EndpointActivity
			enabled int
			last    int64
		)
		if err := rows.Scan(&a.ID, &a.Name, &enabled, &a.Last24h, &a.Total, &last); err != nil {
			return s, err
		}
		a.Enabled = enabled == 1
		t := fromMillis(last)
		a.LastSeen = &t
		s.TopEndpoints = append(s.TopEndpoints, a)
	}
	return s, rows.Err()
}
