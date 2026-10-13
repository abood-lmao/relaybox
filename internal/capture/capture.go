// Package capture receives webhook requests on /hooks/{id}, stores them, and
// answers with the endpoint's mock response.
package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/abood-lmao/relaybox/internal/ids"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/ratelimit"
	"github.com/abood-lmao/relaybox/internal/redact"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
)

// PathPrefix is where endpoints are mounted.
const PathPrefix = "/hooks/"

// Event types published by this package.
const (
	EventRequestCreated   = "request.created"
	EventRequestsImported = "requests.imported"
)

// persistTimeout bounds how long storing a capture may take. Storage uses its
// own context so a sender that disconnects early does not abort the write.
const persistTimeout = 10 * time.Second

// Publisher receives notifications about stored requests.
type Publisher interface {
	Publish(eventType string, payload any)
}

// RequestEvent is the payload of EventRequestCreated. It deliberately
// carries no headers or body.
type RequestEvent struct {
	ID         int64     `json:"id"`
	EndpointID int64     `json:"endpoint_id"`
	Method     string    `json:"method"`
	Status     int       `json:"status"`
	Source     string    `json:"source"`
	ReceivedAt time.Time `json:"received_at"`
}

// Options configures a Service.
type Options struct {
	DB       *storage.DB
	Settings *settings.Service
	Events   Publisher
	Limiter  *ratelimit.Limiter
	Logger   *slog.Logger
	// Secrets are values that must never be stored, such as the dashboard
	// token. They are replaced wherever they appear in a captured request.
	Secrets []string
	// StripCookies lists cookie names removed from captured Cookie headers
	// before anything else happens (the dashboard session cookie).
	StripCookies []string
	// OnStored is called after each successful insert, for example to let
	// the retention runner enforce the stored request limit.
	OnStored func()
}

// Service records inbound requests.
type Service struct {
	db           *storage.DB
	settings     *settings.Service
	events       Publisher
	limiter      *ratelimit.Limiter
	log          *slog.Logger
	secrets      []string
	stripCookies []string
	onStored     func()
	now          func() time.Time
}

// New returns a capture service.
func New(o Options) *Service {
	s := &Service{
		db:           o.DB,
		settings:     o.Settings,
		events:       o.Events,
		limiter:      o.Limiter,
		log:          o.Logger,
		stripCookies: o.StripCookies,
		onStored:     o.OnStored,
		now:          time.Now,
	}
	for _, secret := range o.Secrets {
		if secret != "" {
			s.secrets = append(s.secrets, secret)
		}
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	return s
}

// Inbound is a request to record.
type Inbound struct {
	Method        string
	Path          string // relative to the endpoint URL, starting with "/"
	RawQuery      string
	Header        http.Header
	Body          []byte
	BodyTruncated bool
	ContentLength *int64
	RemoteAddr    string
	Source        string
	ReplayOf      *int64
	ReceivedAt    time.Time
}

// ServeHTTP handles requests under PathPrefix. It parses the path itself
// instead of relying on ServeMux patterns, so unusual sub-paths such as
// "//events" are captured rather than redirected.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	publicID, subPath := splitHookPath(r.URL.EscapedPath())
	if !ids.IsPublicID(publicID) {
		writeError(w, http.StatusNotFound, "not_found", "No endpoint exists at this URL.")
		return
	}
	if !slices.Contains(mockresp.AllowedMethods, r.Method) {
		w.Header().Set("Allow", strings.Join(mockresp.AllowedMethods, ", "))
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed",
			"Endpoints accept GET, HEAD, POST, PUT, PATCH, DELETE, and OPTIONS.")
		return
	}

	ep, err := s.db.GetEndpointByPublicID(r.Context(), publicID)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No endpoint exists at this URL.")
		return
	case err != nil:
		s.log.Error("endpoint lookup failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "The request could not be recorded. Try again later.")
		return
	}
	if !ep.Enabled {
		writeError(w, http.StatusServiceUnavailable, "endpoint_disabled", "This endpoint is disabled.")
		return
	}
	if ok, wait := s.limiter.Allow(publicID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests to this endpoint. Retry later.")
		return
	}

	limit := s.bodyLimit(ep)
	body, tooLarge, readErr := readBody(w, r, limit)

	in := Inbound{
		Method:        r.Method,
		Path:          subPath,
		RawQuery:      r.URL.RawQuery,
		Header:        capturedHeaders(r),
		Body:          body,
		BodyTruncated: tooLarge || readErr != nil,
		ContentLength: contentLength(r),
		RemoteAddr:    remoteIP(r.RemoteAddr),
		Source:        storage.SourceCapture,
		ReceivedAt:    start,
	}

	var (
		resp  mockresp.Response
		delay time.Duration
	)
	switch {
	case tooLarge:
		resp = errorResponse(r.Method, http.StatusRequestEntityTooLarge, "payload_too_large",
			fmt.Sprintf("The request body exceeds this endpoint's limit of %d bytes.", limit))
	case readErr != nil:
		resp = errorResponse(r.Method, http.StatusBadRequest, "body_read_failed", "The request body could not be read completely.")
	default:
		resp = ep.Mock.Build(r.Method)
		delay = time.Duration(ep.Mock.DelayMS) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), persistTimeout)
	defer cancel()
	req := s.prepare(ep, in, resp)
	req.Duration = s.now().Sub(start) + delay
	if err := s.db.InsertRequest(ctx, req); err != nil {
		s.log.Error("could not store captured request", "endpoint_id", ep.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "capture_failed", "The request could not be recorded. Try again later.")
		return
	}
	s.stored(req)
	s.log.Info("request captured", "endpoint_id", ep.ID, "request_id", req.ID, "method", req.Method,
		"status", resp.Status, "bytes", req.BodySize, "truncated", req.BodyTruncated)

	if delay > 0 {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-r.Context().Done():
			return
		}
	}
	writeResponse(w, resp)
}

// Ingest records a request that did not arrive over HTTP, such as a replay
// into an endpoint. The endpoint's body limit, redaction, and mock response
// apply as if it had been received; the configured delay does not.
func (s *Service) Ingest(ctx context.Context, ep storage.Endpoint, in Inbound) (*storage.CapturedRequest, mockresp.Response, error) {
	reqs, resps, err := s.ingest(ctx, ep, []Inbound{in})
	if err != nil {
		return nil, mockresp.Response{}, err
	}
	s.stored(reqs[0])
	return reqs[0], resps[0], nil
}

// IngestBatch records several requests in one transaction, for fixture
// imports. It publishes a single summary event instead of one per request.
func (s *Service) IngestBatch(ctx context.Context, ep storage.Endpoint, ins []Inbound) ([]*storage.CapturedRequest, error) {
	reqs, _, err := s.ingest(ctx, ep, ins)
	if err != nil {
		return nil, err
	}
	if s.events != nil {
		s.events.Publish(EventRequestsImported, map[string]any{"endpoint_id": ep.ID, "count": len(reqs)})
	}
	if s.onStored != nil {
		s.onStored()
	}
	return reqs, nil
}

func (s *Service) ingest(ctx context.Context, ep storage.Endpoint, ins []Inbound) ([]*storage.CapturedRequest, []mockresp.Response, error) {
	limit := s.bodyLimit(ep)
	reqs := make([]*storage.CapturedRequest, len(ins))
	resps := make([]mockresp.Response, len(ins))
	for i, in := range ins {
		start := s.now()
		if in.ReceivedAt.IsZero() {
			in.ReceivedAt = start
		}
		if in.Path == "" {
			in.Path = "/"
		}
		var resp mockresp.Response
		if int64(len(in.Body)) > limit {
			in.Body = in.Body[:limit]
			in.BodyTruncated = true
			resp = errorResponse(in.Method, http.StatusRequestEntityTooLarge, "payload_too_large",
				fmt.Sprintf("The request body exceeds this endpoint's limit of %d bytes.", limit))
		} else {
			resp = ep.Mock.Build(in.Method)
		}
		req := s.prepare(ep, in, resp)
		req.Duration = s.now().Sub(start)
		reqs[i] = req
		resps[i] = resp
	}
	if err := s.db.InsertRequests(ctx, reqs); err != nil {
		return nil, nil, err
	}
	return reqs, resps, nil
}

func (s *Service) stored(req *storage.CapturedRequest) {
	if s.events != nil {
		s.events.Publish(EventRequestCreated, RequestEvent{
			ID: req.ID, EndpointID: req.EndpointID, Method: req.Method,
			Status: req.ResponseStatus, Source: req.Source, ReceivedAt: req.ReceivedAt,
		})
	}
	if s.onStored != nil {
		s.onStored()
	}
}

func (s *Service) bodyLimit(ep storage.Endpoint) int64 {
	if ep.MaxBodyBytes != nil {
		return min(*ep.MaxBodyBytes, s.settings.Ceiling())
	}
	return s.settings.Current().DefaultMaxBodyBytes
}

// prepare applies redaction and builds the row to store.
func (s *Service) prepare(ep storage.Endpoint, in Inbound, resp mockresp.Response) *storage.CapturedRequest {
	policy := s.settings.Current().Policy

	header := s.stripSessionCookies(in.Header)
	headers, reds := policy.Headers(header)
	query, rawQuery, qreds := policy.Query(in.RawQuery)
	reds = append(reds, qreds...)
	contentType := header.Get("Content-Type")
	body, breds := policy.Body(contentType, in.Body, in.BodyTruncated)
	reds = append(reds, breds...)
	rawQuery, query, body, sreds := s.scrubSecrets(headers, rawQuery, query, body)
	reds = append(reds, sreds...)

	return &storage.CapturedRequest{
		EndpointID:       ep.ID,
		EndpointName:     ep.Name,
		EndpointPublicID: ep.PublicID,
		Source:           in.Source,
		ReplayOf:         in.ReplayOf,
		Method:           in.Method,
		Path:             in.Path,
		RawQuery:         rawQuery,
		Query:            query,
		Headers:          headers,
		Body:             body,
		BodySize:         int64(len(in.Body)),
		BodyTruncated:    in.BodyTruncated,
		ContentType:      contentType,
		ContentLength:    in.ContentLength,
		RemoteAddr:       in.RemoteAddr,
		ResponseStatus:   resp.Status,
		ResponseHeaders:  resp.Header,
		ResponseBody:     resp.Body,
		ReceivedAt:       in.ReceivedAt,
		Redactions:       reds,
	}
}

// stripSessionCookies removes Relaybox's own cookies. A browser that opens a
// hook URL sends the dashboard session cookie along, and it must never end
// up in storage, regardless of the redaction settings.
func (s *Service) stripSessionCookies(h http.Header) http.Header {
	if len(s.stripCookies) == 0 || len(h.Values("Cookie")) == 0 {
		return h
	}
	out := h.Clone()
	out.Del("Cookie")
	for _, v := range h.Values("Cookie") {
		var kept []string
		for _, part := range strings.Split(v, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
			if !slices.Contains(s.stripCookies, name) {
				kept = append(kept, strings.TrimSpace(part))
			}
		}
		if len(kept) > 0 {
			out.Add("Cookie", strings.Join(kept, "; "))
		}
	}
	return out
}

// scrubSecrets replaces configured secret values anywhere they appear.
func (s *Service) scrubSecrets(headers map[string][]string, rawQuery string, query []redact.Pair, body []byte) (string, []redact.Pair, []byte, []redact.Redaction) {
	var reds []redact.Redaction
	for _, secret := range s.secrets {
		for name, values := range headers {
			for i, v := range values {
				if strings.Contains(v, secret) {
					values[i] = redact.Placeholder
					reds = append(reds, redact.Redaction{Location: "header", Name: name})
				}
			}
		}
		if strings.Contains(rawQuery, secret) || queryContains(query, secret) {
			rawQuery = strings.ReplaceAll(rawQuery, secret, redact.Placeholder)
			for i := range query {
				query[i][0] = strings.ReplaceAll(query[i][0], secret, redact.Placeholder)
				query[i][1] = strings.ReplaceAll(query[i][1], secret, redact.Placeholder)
			}
			reds = append(reds, redact.Redaction{Location: "query", Name: "dashboard token"})
		}
		if bytes.Contains(body, []byte(secret)) {
			body = bytes.ReplaceAll(body, []byte(secret), []byte(redact.Placeholder))
			reds = append(reds, redact.Redaction{Location: "body", Name: "dashboard token"})
		}
	}
	return rawQuery, query, body, reds
}

func queryContains(query []redact.Pair, secret string) bool {
	for _, p := range query {
		if strings.Contains(p[0], secret) || strings.Contains(p[1], secret) {
			return true
		}
	}
	return false
}

// splitHookPath splits "/hooks/{id}/rest" into the public ID and "/rest".
func splitHookPath(escapedPath string) (publicID, subPath string) {
	rest, ok := strings.CutPrefix(escapedPath, PathPrefix)
	if !ok {
		return "", ""
	}
	publicID, sub, found := strings.Cut(rest, "/")
	if !found || sub == "" {
		return publicID, "/"
	}
	return publicID, "/" + sub
}

// readBody reads at most limit bytes. tooLarge reports that the body was
// longer; the bytes read so far are returned for inspection.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) (body []byte, tooLarge bool, err error) {
	if r.Body == nil || r.Body == http.NoBody {
		return []byte{}, false, nil
	}
	var buf bytes.Buffer
	if r.ContentLength > 0 {
		buf.Grow(int(min(r.ContentLength, limit)))
	}
	_, err = buf.ReadFrom(http.MaxBytesReader(w, r.Body, limit))
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return buf.Bytes(), true, nil
	}
	return buf.Bytes(), false, err
}

// capturedHeaders returns the request headers, adding back the ones Go
// moves out of the header map.
func capturedHeaders(r *http.Request) http.Header {
	h := r.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	if r.Host != "" {
		h.Set("Host", r.Host)
	}
	if len(r.TransferEncoding) > 0 {
		h.Set("Transfer-Encoding", strings.Join(r.TransferEncoding, ", "))
	}
	return h
}

// contentLength returns the declared body length, or nil when the sender
// declared none (chunked bodies, or requests without a body).
func contentLength(r *http.Request) *int64 {
	if r.ContentLength < 0 || (r.ContentLength == 0 && r.Header.Get("Content-Length") == "") {
		return nil
	}
	v := r.ContentLength
	return &v
}

func remoteIP(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func errorResponse(method string, status int, code, message string) mockresp.Response {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	h := make(http.Header)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	mockresp.ServerHeaders(h)
	if method == http.MethodHead {
		body = nil
	}
	return mockresp.Response{Status: status, Header: h, Body: body}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeResponse(w, errorResponse("", status, code, message))
}

func writeResponse(w http.ResponseWriter, resp mockresp.Response) {
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.Status)
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
	}
}
