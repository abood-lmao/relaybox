package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abood-lmao/relaybox/internal/redact"
)

// Request sources.
const (
	SourceCapture = "capture"
	SourceImport  = "import"
	SourceReplay  = "replay"
)

// CapturedRequest is a stored inbound request and the response Relaybox sent.
type CapturedRequest struct {
	ID               int64
	EndpointID       int64
	EndpointName     string
	EndpointPublicID string
	Source           string
	ReplayOf         *int64
	Method           string
	Path             string
	RawQuery         string
	Query            []redact.Pair
	Headers          map[string][]string
	Body             []byte
	BodySize         int64
	BodyTruncated    bool
	ContentType      string
	ContentLength    *int64
	RemoteAddr       string
	ResponseStatus   int
	ResponseHeaders  map[string][]string
	ResponseBody     []byte
	Duration         time.Duration
	ReceivedAt       time.Time
	Redactions       []redact.Redaction
}

// RequestSummary is the list view of a captured request (no bodies).
type RequestSummary struct {
	ID             int64
	EndpointID     int64
	EndpointName   string
	Source         string
	ReplayOf       *int64
	Method         string
	Path           string
	RawQuery       string
	ResponseStatus int
	Duration       time.Duration
	BodySize       int64
	BodyTruncated  bool
	ContentType    string
	ReceivedAt     time.Time
	Redacted       bool
}

// StatusMatch filters by an exact status code, or by class when Class is set
// (2 matches 200-299).
type StatusMatch struct {
	Code  int
	Class int
}

// RequestFilter selects captured requests. Zero values mean "no filter".
type RequestFilter struct {
	EndpointID int64
	Methods    []string
	Statuses   []StatusMatch
	Sources    []string
	From       *time.Time
	To         *time.Time
	Search     string
	Sort       string // received_at, duration, size, status, method
	Ascending  bool
	Limit      int
	Offset     int
}

// Sort keys accepted by ListRequests, mapped to fixed SQL expressions so no
// user input ever reaches the ORDER BY clause.
var sortColumns = map[string]string{
	"received_at": "r.received_at",
	"duration":    "r.duration_us",
	"size":        "r.body_size",
	"status":      "r.response_status",
	"method":      "r.method",
}

// ValidSort reports whether key is an accepted sort key.
func ValidSort(key string) bool {
	_, ok := sortColumns[key]
	return ok
}

const requestColumns = `r.id, r.endpoint_id, e.name, e.public_id, r.source, r.replay_of, r.method, r.path,
	r.raw_query, r.query, r.headers, r.body, r.body_size, r.body_truncated, r.content_type,
	r.content_length, r.remote_addr, r.response_status, r.response_headers, r.response_body,
	r.duration_us, r.received_at, r.redactions`

// InsertRequest stores a captured request and sets its ID.
func (db *DB) InsertRequest(ctx context.Context, req *CapturedRequest) error {
	return db.InsertRequests(ctx, []*CapturedRequest{req})
}

// InsertRequests stores several requests in one transaction.
func (db *DB) InsertRequests(ctx context.Context, reqs []*CapturedRequest) error {
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO captured_requests
		(endpoint_id, source, replay_of, method, path, raw_query, query, headers, body, body_size,
		 body_truncated, content_type, content_length, remote_addr, response_status, response_headers,
		 response_body, duration_us, received_at, redactions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, req := range reqs {
		query, err := marshalJSON(nonNilPairs(req.Query))
		if err != nil {
			return err
		}
		headers, err := marshalJSON(nonNilMap(req.Headers))
		if err != nil {
			return err
		}
		respHeaders, err := marshalJSON(nonNilMap(req.ResponseHeaders))
		if err != nil {
			return err
		}
		redactions, err := marshalJSON(nonNilRedactions(req.Redactions))
		if err != nil {
			return err
		}
		source := req.Source
		if source == "" {
			source = SourceCapture
		}
		res, err := stmt.ExecContext(ctx, req.EndpointID, source, nullableInt(req.ReplayOf), req.Method,
			req.Path, req.RawQuery, query, headers, nonNilBytes(req.Body), req.BodySize, boolInt(req.BodyTruncated),
			req.ContentType, nullableInt(req.ContentLength), req.RemoteAddr, req.ResponseStatus, respHeaders,
			nonNilBytes(req.ResponseBody), req.Duration.Microseconds(), millis(req.ReceivedAt), redactions)
		if err != nil {
			return err
		}
		if req.ID, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanRequest(s rowScanner) (CapturedRequest, error) {
	var (
		r                                         CapturedRequest
		replayOf, contentLength                   sql.NullInt64
		query, headers, respHeaders, redactionsJS string
		truncated                                 int
		durationUS, received                      int64
	)
	err := s.Scan(&r.ID, &r.EndpointID, &r.EndpointName, &r.EndpointPublicID, &r.Source, &replayOf, &r.Method,
		&r.Path, &r.RawQuery, &query, &headers, &r.Body, &r.BodySize, &truncated, &r.ContentType,
		&contentLength, &r.RemoteAddr, &r.ResponseStatus, &respHeaders, &r.ResponseBody, &durationUS,
		&received, &redactionsJS)
	if err != nil {
		return CapturedRequest{}, err
	}
	if replayOf.Valid {
		v := replayOf.Int64
		r.ReplayOf = &v
	}
	if contentLength.Valid {
		v := contentLength.Int64
		r.ContentLength = &v
	}
	r.BodyTruncated = truncated == 1
	r.Duration = time.Duration(durationUS) * time.Microsecond
	r.ReceivedAt = fromMillis(received)
	for _, f := range []struct {
		src string
		dst any
	}{{query, &r.Query}, {headers, &r.Headers}, {respHeaders, &r.ResponseHeaders}, {redactionsJS, &r.Redactions}} {
		if err := json.Unmarshal([]byte(f.src), f.dst); err != nil {
			return CapturedRequest{}, fmt.Errorf("decode request %d: %w", r.ID, err)
		}
	}
	return r, nil
}

// GetRequest returns a captured request by ID.
func (db *DB) GetRequest(ctx context.Context, id int64) (CapturedRequest, error) {
	row := db.r.QueryRowContext(ctx, `SELECT `+requestColumns+`
		FROM captured_requests r JOIN endpoints e ON e.id = r.endpoint_id WHERE r.id = ?`, id)
	req, err := scanRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CapturedRequest{}, ErrNotFound
	}
	return req, err
}

// GetRequests returns the requests with the given IDs, oldest first. Missing
// IDs are skipped.
func (db *DB) GetRequests(ctx context.Context, ids []int64) ([]CapturedRequest, error) {
	if len(ids) == 0 {
		return []CapturedRequest{}, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.r.QueryContext(ctx, `SELECT `+requestColumns+`
		FROM captured_requests r JOIN endpoints e ON e.id = r.endpoint_id
		WHERE r.id IN (`+placeholders(len(ids))+`) ORDER BY r.received_at, r.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CapturedRequest{}
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

// LatestRequestIDs returns up to limit request IDs, newest first, optionally
// limited to one endpoint.
func (db *DB) LatestRequestIDs(ctx context.Context, endpointID int64, limit int) ([]int64, error) {
	query := "SELECT id FROM captured_requests"
	args := []any{}
	if endpointID > 0 {
		query += " WHERE endpoint_id = ?"
		args = append(args, endpointID)
	}
	query += " ORDER BY received_at DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ReplayChild is a request created by replaying another request into an endpoint.
type ReplayChild struct {
	ID             int64
	EndpointID     int64
	EndpointName   string
	ResponseStatus int
	ReceivedAt     time.Time
}

// ReplayChildren lists requests whose replay_of points at id, newest first.
func (db *DB) ReplayChildren(ctx context.Context, id int64, limit int) ([]ReplayChild, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT r.id, r.endpoint_id, e.name, r.response_status, r.received_at
		FROM captured_requests r JOIN endpoints e ON e.id = r.endpoint_id
		WHERE r.replay_of = ? ORDER BY r.id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReplayChild{}
	for rows.Next() {
		var (
			c        ReplayChild
			received int64
		)
		if err := rows.Scan(&c.ID, &c.EndpointID, &c.EndpointName, &c.ResponseStatus, &received); err != nil {
			return nil, err
		}
		c.ReceivedAt = fromMillis(received)
		out = append(out, c)
	}
	return out, rows.Err()
}

func buildWhere(f RequestFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if f.EndpointID > 0 {
		clauses = append(clauses, "r.endpoint_id = ?")
		args = append(args, f.EndpointID)
	}
	if len(f.Methods) > 0 {
		clauses = append(clauses, "r.method IN ("+placeholders(len(f.Methods))+")")
		for _, m := range f.Methods {
			args = append(args, m)
		}
	}
	if len(f.Sources) > 0 {
		clauses = append(clauses, "r.source IN ("+placeholders(len(f.Sources))+")")
		for _, s := range f.Sources {
			args = append(args, s)
		}
	}
	if len(f.Statuses) > 0 {
		var ors []string
		for _, s := range f.Statuses {
			if s.Class > 0 {
				ors = append(ors, "(r.response_status >= ? AND r.response_status < ?)")
				args = append(args, s.Class*100, s.Class*100+100)
			} else {
				ors = append(ors, "r.response_status = ?")
				args = append(args, s.Code)
			}
		}
		clauses = append(clauses, "("+strings.Join(ors, " OR ")+")")
	}
	if f.From != nil {
		clauses = append(clauses, "r.received_at >= ?")
		args = append(args, millis(*f.From))
	}
	if f.To != nil {
		clauses = append(clauses, "r.received_at <= ?")
		args = append(args, millis(*f.To))
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		like := "%" + escapeLike(term) + "%"
		search := []string{
			`r.path LIKE ? ESCAPE '\'`,
			`r.raw_query LIKE ? ESCAPE '\'`,
			`r.headers LIKE ? ESCAPE '\'`,
			`r.content_type LIKE ? ESCAPE '\'`,
			`CAST(r.body AS TEXT) LIKE ? ESCAPE '\'`,
		}
		for range search {
			args = append(args, like)
		}
		if id, err := strconv.ParseInt(strings.TrimPrefix(term, "#"), 10, 64); err == nil && id > 0 {
			search = append(search, "r.id = ?")
			args = append(args, id)
		}
		clauses = append(clauses, "("+strings.Join(search, " OR ")+")")
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ListRequests returns one page of summaries and the total number of matches.
func (db *DB) ListRequests(ctx context.Context, f RequestFilter) ([]RequestSummary, int64, error) {
	where, args := buildWhere(f)

	var total int64
	if err := db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM captured_requests r"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	col, ok := sortColumns[f.Sort]
	if !ok {
		col = sortColumns["received_at"]
	}
	dir := "DESC"
	if f.Ascending {
		dir = "ASC"
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT r.id, r.endpoint_id, e.name, r.source, r.replay_of, r.method, r.path, r.raw_query,
		r.response_status, r.duration_us, r.body_size, r.body_truncated, r.content_type, r.received_at,
		r.redactions != '[]'
		FROM captured_requests r JOIN endpoints e ON e.id = r.endpoint_id` + where +
		" ORDER BY " + col + " " + dir + ", r.id " + dir + " LIMIT ? OFFSET ?"
	rows, err := db.r.QueryContext(ctx, query, append(args, limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []RequestSummary{}
	for rows.Next() {
		var (
			s                    RequestSummary
			replayOf             sql.NullInt64
			truncated, redacted  int
			durationUS, received int64
		)
		if err := rows.Scan(&s.ID, &s.EndpointID, &s.EndpointName, &s.Source, &replayOf, &s.Method, &s.Path,
			&s.RawQuery, &s.ResponseStatus, &durationUS, &s.BodySize, &truncated, &s.ContentType, &received,
			&redacted); err != nil {
			return nil, 0, err
		}
		if replayOf.Valid {
			v := replayOf.Int64
			s.ReplayOf = &v
		}
		s.BodyTruncated = truncated == 1
		s.Redacted = redacted == 1
		s.Duration = time.Duration(durationUS) * time.Microsecond
		s.ReceivedAt = fromMillis(received)
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// DeleteRequest removes one request and its replay history.
func (db *DB) DeleteRequest(ctx context.Context, id int64) error {
	res, err := db.w.ExecContext(ctx, "DELETE FROM captured_requests WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteScope selects requests for bulk deletion. Exactly one field should be
// set; callers validate this before calling DeleteRequests.
type DeleteScope struct {
	All        bool
	EndpointID int64
	IDs        []int64
}

// DeleteRequests removes the requests in scope and returns how many were deleted.
func (db *DB) DeleteRequests(ctx context.Context, scope DeleteScope) (int64, error) {
	var (
		query string
		args  []any
	)
	switch {
	case len(scope.IDs) > 0:
		query = "DELETE FROM captured_requests WHERE id IN (" + placeholders(len(scope.IDs)) + ")"
		for _, id := range scope.IDs {
			args = append(args, id)
		}
	case scope.EndpointID > 0:
		query = "DELETE FROM captured_requests WHERE endpoint_id = ?"
		args = append(args, scope.EndpointID)
	case scope.All:
		query = "DELETE FROM captured_requests"
	default:
		return 0, errors.New("delete scope is empty")
	}
	res, err := db.w.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteRequestsBefore removes requests received before cutoff, in batches so
// the write lock is released between them.
func (db *DB) DeleteRequestsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return db.deleteBatches(ctx, `DELETE FROM captured_requests WHERE id IN
		(SELECT id FROM captured_requests WHERE received_at < ? ORDER BY received_at, id LIMIT 500)`, millis(cutoff))
}

// TrimRequests keeps the newest keep requests and deletes the rest.
func (db *DB) TrimRequests(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		return 0, errors.New("keep must be positive")
	}
	return db.deleteBatches(ctx, `DELETE FROM captured_requests WHERE id IN
		(SELECT id FROM captured_requests ORDER BY received_at DESC, id DESC LIMIT 500 OFFSET ?)`, keep)
}

func (db *DB) deleteBatches(ctx context.Context, query string, arg any) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res, err := db.w.ExecContext(ctx, query, arg)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}

// CountRequestsBefore returns how many requests were received before cutoff.
func (db *DB) CountRequestsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var n int64
	err := db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM captured_requests WHERE received_at < ?", millis(cutoff)).Scan(&n)
	return n, err
}

// CountRequests returns the number of stored requests.
func (db *DB) CountRequests(ctx context.Context) (int64, error) {
	var n int64
	err := db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM captured_requests").Scan(&n)
	return n, err
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func nonNilMap(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}
	return m
}

func nonNilPairs(p []redact.Pair) []redact.Pair {
	if p == nil {
		return []redact.Pair{}
	}
	return p
}

func nonNilRedactions(r []redact.Redaction) []redact.Redaction {
	if r == nil {
		return []redact.Redaction{}
	}
	return r
}

func nonNilBytes(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}
