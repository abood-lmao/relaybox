package storage

import (
	"context"
	"database/sql"
	"time"
)

// Replay targets and outcomes.
const (
	ReplayTargetURL      = "url"
	ReplayTargetEndpoint = "endpoint"
	OutcomeSuccess       = "success"
	OutcomeFailure       = "failure"
)

// ReplayAttempt records one replay of a captured request. Request headers and
// bodies sent during the replay are not stored, and Destination never includes
// a query string.
type ReplayAttempt struct {
	ID               int64
	RequestID        int64
	Target           string
	Destination      string
	DestinationHost  string
	TargetEndpointID *int64
	ResultRequestID  *int64
	Method           string
	ResponseStatus   *int
	ResponseSize     *int64
	Duration         time.Duration
	Outcome          string
	Summary          string
	CreatedAt        time.Time
}

// InsertReplayAttempt stores a replay attempt and sets its ID.
func (db *DB) InsertReplayAttempt(ctx context.Context, a *ReplayAttempt) error {
	var status any
	if a.ResponseStatus != nil {
		status = *a.ResponseStatus
	}
	res, err := db.w.ExecContext(ctx, `INSERT INTO replay_attempts
		(request_id, target, destination, destination_host, target_endpoint_id, result_request_id, method,
		 response_status, response_size, duration_us, outcome, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.RequestID, a.Target, a.Destination, a.DestinationHost, nullableInt(a.TargetEndpointID),
		nullableInt(a.ResultRequestID), a.Method, status, nullableInt(a.ResponseSize),
		a.Duration.Microseconds(), a.Outcome, a.Summary, millis(a.CreatedAt))
	if err != nil {
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}

// ListReplayAttempts returns replay attempts for a request, newest first.
func (db *DB) ListReplayAttempts(ctx context.Context, requestID int64, limit int) ([]ReplayAttempt, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT id, request_id, target, destination, destination_host,
		target_endpoint_id, result_request_id, method, response_status, response_size, duration_us,
		outcome, summary, created_at
		FROM replay_attempts WHERE request_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, requestID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReplayAttempt{}
	for rows.Next() {
		var (
			a                                   ReplayAttempt
			targetEndpoint, resultRequest, size sql.NullInt64
			status                              sql.NullInt64
			durationUS, created                 int64
		)
		if err := rows.Scan(&a.ID, &a.RequestID, &a.Target, &a.Destination, &a.DestinationHost,
			&targetEndpoint, &resultRequest, &a.Method, &status, &size, &durationUS,
			&a.Outcome, &a.Summary, &created); err != nil {
			return nil, err
		}
		if targetEndpoint.Valid {
			v := targetEndpoint.Int64
			a.TargetEndpointID = &v
		}
		if resultRequest.Valid {
			v := resultRequest.Int64
			a.ResultRequestID = &v
		}
		if status.Valid {
			v := int(status.Int64)
			a.ResponseStatus = &v
		}
		if size.Valid {
			v := size.Int64
			a.ResponseSize = &v
		}
		a.Duration = time.Duration(durationUS) * time.Microsecond
		a.CreatedAt = fromMillis(created)
		out = append(out, a)
	}
	return out, rows.Err()
}
