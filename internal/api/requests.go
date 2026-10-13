package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/abood-lmao/relaybox/internal/compare"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/storage"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
	maxOffset       = 10_000_000
	maxSearchLen    = 200
	maxBulkIDs      = 1000
)

var sources = []string{storage.SourceCapture, storage.SourceImport, storage.SourceReplay}

func (a *API) listRequests(w http.ResponseWriter, r *http.Request) error {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return badRequest("invalid_filter", "The query string is malformed.")
	}
	f, err := parseRequestFilter(q)
	if err != nil {
		return err
	}
	items, total, err := a.db.ListRequests(r.Context(), f)
	if err != nil {
		return err
	}
	views := make([]RequestSummaryView, 0, len(items))
	for _, s := range items {
		views = append(views, summaryView(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views, "total": total, "limit": f.Limit, "offset": f.Offset})
	return nil
}

func parseRequestFilter(q url.Values) (storage.RequestFilter, error) {
	f := storage.RequestFilter{Limit: defaultPageSize, Sort: "received_at"}
	if v := q.Get("endpoint_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return f, badRequest("invalid_filter", "endpoint_id must be a positive integer.")
		}
		f.EndpointID = id
	}
	for _, m := range splitList(q["method"]) {
		m = strings.ToUpper(m)
		if !slices.Contains(mockresp.AllowedMethods, m) {
			return f, badRequest("invalid_filter", "Unknown method %q.", m)
		}
		f.Methods = append(f.Methods, m)
	}
	for _, s := range splitList(q["status"]) {
		s = strings.ToLower(s)
		if len(s) == 3 && strings.HasSuffix(s, "xx") && s[0] >= '1' && s[0] <= '5' {
			f.Statuses = append(f.Statuses, storage.StatusMatch{Class: int(s[0] - '0')})
			continue
		}
		code, err := strconv.Atoi(s)
		if err != nil || code < 100 || code > 599 {
			return f, badRequest("invalid_filter", "Status %q must be a code such as 404 or a class such as 4xx.", s)
		}
		f.Statuses = append(f.Statuses, storage.StatusMatch{Code: code})
	}
	for _, s := range splitList(q["source"]) {
		if !slices.Contains(sources, s) {
			return f, badRequest("invalid_filter", "Source must be capture, import, or replay.")
		}
		f.Sources = append(f.Sources, s)
	}
	var err error
	if f.From, err = parseTime(q.Get("from"), false); err != nil {
		return f, badRequest("invalid_filter", "from: %v", err)
	}
	if f.To, err = parseTime(q.Get("to"), true); err != nil {
		return f, badRequest("invalid_filter", "to: %v", err)
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return f, badRequest("invalid_filter", "to must not be before from.")
	}
	f.Search = strings.TrimSpace(q.Get("q"))
	if len([]rune(f.Search)) > maxSearchLen {
		return f, badRequest("invalid_filter", "Search text must be at most %d characters.", maxSearchLen)
	}
	if v := q.Get("sort"); v != "" {
		if !storage.ValidSort(v) {
			return f, badRequest("invalid_filter", "sort must be one of received_at, duration, size, status, method.")
		}
		f.Sort = v
	}
	switch q.Get("order") {
	case "", "desc":
	case "asc":
		f.Ascending = true
	default:
		return f, badRequest("invalid_filter", "order must be asc or desc.")
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageSize {
			return f, badRequest("invalid_filter", "limit must be between 1 and %d.", maxPageSize)
		}
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxOffset {
			return f, badRequest("invalid_filter", "offset must be between 0 and %d.", maxOffset)
		}
		f.Offset = n
	}
	return f, nil
}

// splitList accepts both repeated parameters and comma-separated values.
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// parseTime accepts RFC 3339 timestamps or YYYY-MM-DD dates. A date used as
// an upper bound covers the whole day.
func parseTime(v string, endOfDay bool) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return &t, nil
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return nil, errors.New("use an RFC 3339 timestamp or a YYYY-MM-DD date")
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Millisecond)
	}
	return &t, nil
}

func (a *API) getRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	req, err := a.db.GetRequest(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound("Request")
	}
	if err != nil {
		return err
	}
	children, err := a.db.ReplayChildren(r.Context(), id, 20)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, a.detailView(req, children))
	return nil
}

func (a *API) deleteRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := a.db.DeleteRequest(r.Context(), id); errors.Is(err, storage.ErrNotFound) {
		return notFound("Request")
	} else if err != nil {
		return err
	}
	a.publish("requests.deleted", map[string]any{"count": 1, "ids": []int64{id}})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// bulkDeleteRequests requires an explicit scope so a missing parameter can
// never widen a deletion.
func (a *API) bulkDeleteRequests(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	var scope storage.DeleteScope
	switch q.Get("scope") {
	case "all":
		scope.All = true
	case "endpoint":
		id, err := strconv.ParseInt(q.Get("endpoint_id"), 10, 64)
		if err != nil || id <= 0 {
			return badRequest("invalid_scope", "scope=endpoint needs a positive endpoint_id.")
		}
		if _, err := a.db.GetEndpoint(r.Context(), id); errors.Is(err, storage.ErrNotFound) {
			return notFound("Endpoint")
		} else if err != nil {
			return err
		}
		scope.EndpointID = id
	case "ids":
		parts := splitList(q["ids"])
		if len(parts) == 0 || len(parts) > maxBulkIDs {
			return badRequest("invalid_scope", "scope=ids needs between 1 and %d IDs.", maxBulkIDs)
		}
		for _, p := range parts {
			id, err := strconv.ParseInt(p, 10, 64)
			if err != nil || id <= 0 {
				return badRequest("invalid_scope", "%q is not a valid request ID.", p)
			}
			scope.IDs = append(scope.IDs, id)
		}
	default:
		return badRequest("invalid_scope", "Set scope to all, endpoint, or ids.")
	}
	n, err := a.db.DeleteRequests(r.Context(), scope)
	if err != nil {
		return err
	}
	a.log.Info("requests deleted", "scope", q.Get("scope"), "count", n)
	a.publish("requests.deleted", map[string]any{"count": n})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
	return nil
}

// downloadBody returns a stored body as an attachment. Bodies are never
// served inline, so a captured HTML or SVG payload cannot run in the
// dashboard's origin.
func (a *API) downloadBody(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	part := r.URL.Query().Get("part")
	if part == "" {
		part = "request"
	}
	if part != "request" && part != "response" {
		return badRequest("invalid_part", "part must be request or response.")
	}
	req, err := a.db.GetRequest(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound("Request")
	}
	if err != nil {
		return err
	}
	body, ct := req.Body, req.ContentType
	if part == "response" {
		body, ct = req.ResponseBody, http.Header(req.ResponseHeaders).Get("Content-Type")
	}
	ext := map[string]string{KindJSON: "json", KindText: "txt", KindForm: "txt"}[makeBodyView(body, ct, 0, false).Kind]
	if ext == "" {
		ext = "bin"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="relaybox-%s-%d.%s"`, part, id, ext))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return nil
}

type compareInput struct {
	LeftID  int64  `json:"left_id"`
	RightID int64  `json:"right_id"`
	Part    string `json:"part"`
}

func (a *API) compareRequests(w http.ResponseWriter, r *http.Request) error {
	var in compareInput
	if err := decodeJSON(w, r, &in, maxJSONBody); err != nil {
		return err
	}
	if in.LeftID <= 0 || in.RightID <= 0 {
		return badRequest("invalid_input", "left_id and right_id must be request IDs.")
	}
	if in.Part == "" {
		in.Part = "request"
	}
	if in.Part != "request" && in.Part != "response" {
		return badRequest("invalid_input", "part must be request or response.")
	}
	left, err := a.db.GetRequest(r.Context(), in.LeftID)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound(fmt.Sprintf("Request #%d", in.LeftID))
	} else if err != nil {
		return err
	}
	right, err := a.db.GetRequest(r.Context(), in.RightID)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound(fmt.Sprintf("Request #%d", in.RightID))
	} else if err != nil {
		return err
	}

	side := func(req storage.CapturedRequest) (compare.Side, map[string][]string) {
		if in.Part == "response" {
			return compare.Side{Body: req.ResponseBody, ContentType: http.Header(req.ResponseHeaders).Get("Content-Type")}, req.ResponseHeaders
		}
		return compare.Side{Body: req.Body, ContentType: req.ContentType, Truncated: req.BodyTruncated}, req.Headers
	}
	ls, lh := side(left)
	rs, rh := side(right)
	writeJSON(w, http.StatusOK, map[string]any{
		"part":    in.Part,
		"left":    a.detailSummary(left),
		"right":   a.detailSummary(right),
		"body":    compare.Bodies(ls, rs),
		"headers": compare.Headers(lh, rh),
	})
	return nil
}

func (a *API) detailSummary(req storage.CapturedRequest) RequestSummaryView {
	return a.detailView(req, nil).RequestSummaryView
}
