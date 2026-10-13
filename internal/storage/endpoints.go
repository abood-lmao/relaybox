package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/abood-lmao/relaybox/internal/ids"
	"github.com/abood-lmao/relaybox/internal/mockresp"
)

// Endpoint is a capture endpoint reachable at /hooks/{PublicID}.
type Endpoint struct {
	ID            int64
	PublicID      string
	Name          string
	Enabled       bool
	Mock          mockresp.Config
	MaxBodyBytes  *int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	RequestCount  int64
	LastRequestAt *time.Time
}

// NewEndpoint holds the fields needed to create an endpoint.
type NewEndpoint struct {
	Name         string
	Enabled      bool
	Mock         mockresp.Config
	MaxBodyBytes *int64
}

// EndpointUpdate holds optional changes. Nil fields are left unchanged.
type EndpointUpdate struct {
	Name            *string
	Enabled         *bool
	Mock            *mockresp.Config
	SetMaxBodyBytes bool   // when true, MaxBodyBytes replaces the stored value
	MaxBodyBytes    *int64 // nil clears the per-endpoint limit
}

const endpointColumns = `e.id, e.public_id, e.name, e.enabled, e.mock_status, e.mock_content_type,
	e.mock_headers, e.mock_body, e.mock_delay_ms, e.max_body_bytes, e.created_at, e.updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanEndpoint(s rowScanner, extra ...any) (Endpoint, error) {
	var (
		e        Endpoint
		enabled  int
		headers  string
		maxBody  sql.NullInt64
		created  int64
		updated  int64
		scanInto = []any{&e.ID, &e.PublicID, &e.Name, &enabled, &e.Mock.Status, &e.Mock.ContentType, &headers, &e.Mock.Body, &e.Mock.DelayMS, &maxBody, &created, &updated}
	)
	if err := s.Scan(append(scanInto, extra...)...); err != nil {
		return Endpoint{}, err
	}
	e.Enabled = enabled == 1
	e.Mock.Headers = map[string]string{}
	if err := json.Unmarshal([]byte(headers), &e.Mock.Headers); err != nil {
		return Endpoint{}, fmt.Errorf("decode mock headers for endpoint %d: %w", e.ID, err)
	}
	if maxBody.Valid {
		v := maxBody.Int64
		e.MaxBodyBytes = &v
	}
	e.CreatedAt = fromMillis(created)
	e.UpdatedAt = fromMillis(updated)
	return e, nil
}

// CreateEndpoint inserts an endpoint with a fresh random public identifier.
func (db *DB) CreateEndpoint(ctx context.Context, in NewEndpoint) (Endpoint, error) {
	headers, err := json.Marshal(nonNilHeaders(in.Mock.Headers))
	if err != nil {
		return Endpoint{}, err
	}
	now := millis(db.now())
	for attempt := 0; attempt < 3; attempt++ {
		publicID := ids.NewPublicID()
		res, err := db.w.ExecContext(ctx, `INSERT INTO endpoints
			(public_id, name, enabled, mock_status, mock_content_type, mock_headers, mock_body, mock_delay_ms, max_body_bytes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			publicID, in.Name, boolInt(in.Enabled), in.Mock.Status, in.Mock.ContentType, string(headers),
			in.Mock.Body, in.Mock.DelayMS, nullableInt(in.MaxBodyBytes), now, now)
		if isDuplicateName(err) {
			return Endpoint{}, ErrDuplicateName
		}
		if isUniqueViolation(err) {
			continue
		}
		if err != nil {
			return Endpoint{}, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return Endpoint{}, err
		}
		return db.GetEndpoint(ctx, id)
	}
	return Endpoint{}, errors.New("could not generate a unique endpoint identifier")
}

// ListEndpoints returns all endpoints, newest first, with request counts.
func (db *DB) ListEndpoints(ctx context.Context) ([]Endpoint, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT `+endpointColumns+`,
		(SELECT COUNT(*) FROM captured_requests r WHERE r.endpoint_id = e.id),
		(SELECT MAX(received_at) FROM captured_requests r WHERE r.endpoint_id = e.id)
		FROM endpoints e ORDER BY e.created_at DESC, e.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Endpoint{}
	for rows.Next() {
		e, err := scanEndpointWithStats(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEndpointWithStats(s rowScanner) (Endpoint, error) {
	var (
		count int64
		last  sql.NullInt64
	)
	e, err := scanEndpoint(s, &count, &last)
	if err != nil {
		return Endpoint{}, err
	}
	e.RequestCount = count
	if last.Valid {
		t := fromMillis(last.Int64)
		e.LastRequestAt = &t
	}
	return e, nil
}

// GetEndpoint returns an endpoint by internal ID, including request counts.
func (db *DB) GetEndpoint(ctx context.Context, id int64) (Endpoint, error) {
	row := db.r.QueryRowContext(ctx, `SELECT `+endpointColumns+`,
		(SELECT COUNT(*) FROM captured_requests r WHERE r.endpoint_id = e.id),
		(SELECT MAX(received_at) FROM captured_requests r WHERE r.endpoint_id = e.id)
		FROM endpoints e WHERE e.id = ?`, id)
	e, err := scanEndpointWithStats(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}
	return e, err
}

// GetEndpointByPublicID returns an endpoint by its public identifier.
// It is used on the capture path and skips the request count.
func (db *DB) GetEndpointByPublicID(ctx context.Context, publicID string) (Endpoint, error) {
	row := db.r.QueryRowContext(ctx, `SELECT `+endpointColumns+` FROM endpoints e WHERE e.public_id = ?`, publicID)
	e, err := scanEndpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}
	return e, err
}

// UpdateEndpoint applies the non-nil fields of u.
func (db *DB) UpdateEndpoint(ctx context.Context, id int64, u EndpointUpdate) (Endpoint, error) {
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return Endpoint{}, err
	}
	defer tx.Rollback()

	exec := func(query string, args ...any) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM endpoints WHERE id = ?", id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Endpoint{}, ErrNotFound
		}
		return Endpoint{}, err
	}
	if u.Name != nil {
		if err := exec("UPDATE endpoints SET name = ? WHERE id = ?", *u.Name, id); err != nil {
			if isDuplicateName(err) {
				return Endpoint{}, ErrDuplicateName
			}
			return Endpoint{}, err
		}
	}
	if u.Enabled != nil {
		if err := exec("UPDATE endpoints SET enabled = ? WHERE id = ?", boolInt(*u.Enabled), id); err != nil {
			return Endpoint{}, err
		}
	}
	if u.Mock != nil {
		headers, err := json.Marshal(nonNilHeaders(u.Mock.Headers))
		if err != nil {
			return Endpoint{}, err
		}
		if err := exec(`UPDATE endpoints SET mock_status = ?, mock_content_type = ?, mock_headers = ?,
			mock_body = ?, mock_delay_ms = ? WHERE id = ?`,
			u.Mock.Status, u.Mock.ContentType, string(headers), u.Mock.Body, u.Mock.DelayMS, id); err != nil {
			return Endpoint{}, err
		}
	}
	if u.SetMaxBodyBytes {
		if err := exec("UPDATE endpoints SET max_body_bytes = ? WHERE id = ?", nullableInt(u.MaxBodyBytes), id); err != nil {
			return Endpoint{}, err
		}
	}
	if err := exec("UPDATE endpoints SET updated_at = ? WHERE id = ?", millis(db.now()), id); err != nil {
		return Endpoint{}, err
	}
	if err := tx.Commit(); err != nil {
		return Endpoint{}, err
	}
	return db.GetEndpoint(ctx, id)
}

// RotateEndpointPublicID replaces the public identifier. The old URL stops
// working immediately; captured history is kept.
func (db *DB) RotateEndpointPublicID(ctx context.Context, id int64) (Endpoint, error) {
	for attempt := 0; attempt < 3; attempt++ {
		res, err := db.w.ExecContext(ctx, "UPDATE endpoints SET public_id = ?, updated_at = ? WHERE id = ?",
			ids.NewPublicID(), millis(db.now()), id)
		if isUniqueViolation(err) {
			continue
		}
		if err != nil {
			return Endpoint{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return Endpoint{}, ErrNotFound
		}
		return db.GetEndpoint(ctx, id)
	}
	return Endpoint{}, errors.New("could not generate a unique endpoint identifier")
}

// DeleteEndpoint removes an endpoint together with its captured requests and
// their replay history. It returns the number of requests deleted.
func (db *DB) DeleteEndpoint(ctx context.Context, id int64) (int64, error) {
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM captured_requests WHERE endpoint_id = ?", id).Scan(&count); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM endpoints WHERE id = ?", id)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	return count, tx.Commit()
}

// CountEndpoints returns the number of endpoints.
func (db *DB) CountEndpoints(ctx context.Context) (int64, error) {
	var n int64
	err := db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM endpoints").Scan(&n)
	return n, err
}

func nonNilHeaders(h map[string]string) map[string]string {
	if h == nil {
		return map[string]string{}
	}
	return h
}

func nullableInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
