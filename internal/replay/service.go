package replay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/redact"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/validate"
)

// Errors returned by Service.Replay.
var (
	ErrURLReplayDisabled    = errors.New("replay to URLs is disabled for remote dashboard access")
	ErrConfirmationRequired = errors.New("replay to an external destination must be confirmed")
	ErrTruncatedBody        = errors.New("the stored body is truncated, so replaying it would send incomplete data")
	ErrEndpointDisabled     = errors.New("the target endpoint is disabled")
	ErrBusy                 = errors.New("too many replays are in progress")
)

// Limits for user-edited replay requests.
const (
	MaxBodyBytes      = 10 << 20
	MaxHeaders        = 100
	maxHeaderNameLen  = 128
	maxHeaderValueLen = 8192
	maxConcurrent     = 4
	historyLimit      = 50
)

// Target kinds.
const (
	TargetURL      = storage.ReplayTargetURL
	TargetEndpoint = storage.ReplayTargetEndpoint
)

// neverForward are framing and hop-by-hop headers that cannot be replayed.
var neverForward = map[string]string{
	"host":                "set from the destination URL",
	"content-length":      "set from the body",
	"transfer-encoding":   "set by the HTTP client",
	"connection":          "hop-by-hop header",
	"keep-alive":          "hop-by-hop header",
	"proxy-connection":    "hop-by-hop header",
	"proxy-authorization": "hop-by-hop header",
	"proxy-authenticate":  "hop-by-hop header",
	"te":                  "hop-by-hop header",
	"trailer":             "hop-by-hop header",
	"upgrade":             "hop-by-hop header",
	"expect":              "hop-by-hop header",
	"http2-settings":      "hop-by-hop header",
}

// skipByDefault are headers added by proxies or clients in transit. They can
// be forwarded, but are not selected unless the user asks for them.
var skipByDefault = map[string]bool{
	"accept-encoding": true, "forwarded": true, "via": true, "cdn-loop": true,
	"x-forwarded-for": true, "x-forwarded-host": true, "x-forwarded-proto": true,
	"x-forwarded-port": true, "x-forwarded-server": true, "x-real-ip": true,
	"cf-connecting-ip": true, "cf-ray": true, "cf-visitor": true, "cf-ipcountry": true,
	"true-client-ip": true, "x-request-start": true,
}

// HeaderOption describes how a captured header is treated in a replay.
type HeaderOption struct {
	Name     string
	Value    string
	Selected bool   // forwarded unless the user changes the selection
	Redacted bool   // the stored value is a redaction placeholder
	Blocked  string // non-empty when the header can never be forwarded
}

// HeaderOptions lists the captured headers of req with their default
// selection, sorted by name.
func HeaderOptions(req storage.CapturedRequest) []HeaderOption {
	redacted := make(map[string]bool)
	for _, r := range req.Redactions {
		if r.Location == "header" {
			redacted[strings.ToLower(r.Name)] = true
		}
	}
	names := make([]string, 0, len(req.Headers))
	for name := range req.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []HeaderOption
	for _, name := range names {
		lower := strings.ToLower(name)
		for _, v := range req.Headers[name] {
			opt := HeaderOption{
				Name:     name,
				Value:    v,
				Redacted: redacted[lower] || strings.Contains(v, redact.Placeholder),
				Blocked:  neverForward[lower],
			}
			opt.Selected = opt.Blocked == "" && !opt.Redacted && !skipByDefault[lower]
			out = append(out, opt)
		}
	}
	return out
}

// DefaultHeaders returns the headers forwarded when the user does not pick
// any.
func DefaultHeaders(req storage.CapturedRequest) [][2]string {
	var out [][2]string
	for _, o := range HeaderOptions(req) {
		if o.Selected {
			out = append(out, [2]string{o.Name, o.Value})
		}
	}
	return out
}

// Input is a replay requested by the user.
type Input struct {
	Target     string
	URL        string
	EndpointID int64
	Method     string      // empty means the original method
	Headers    [][2]string // nil means DefaultHeaders
	Body       []byte      // nil means the original body
	Confirmed  bool
}

// Outcome is the result of a replay.
type Outcome struct {
	Attempt storage.ReplayAttempt
	// Response is set for URL replays that received an HTTP response.
	Response *Result
	// Created and EndpointResponse are set for replays into an endpoint.
	Created          *storage.CapturedRequest
	EndpointResponse *mockresp.Response
}

// ServiceOptions configures a Service.
type ServiceOptions struct {
	DB         *storage.DB
	Capture    *capture.Service
	Guard      *Guard
	Sender     *Sender
	URLEnabled bool
	Logger     *slog.Logger
}

// Service runs replays and records their history.
type Service struct {
	db         *storage.DB
	capture    *capture.Service
	guard      *Guard
	sender     *Sender
	urlEnabled bool
	sem        chan struct{}
	log        *slog.Logger
	now        func() time.Time
}

// NewService returns a replay service.
func NewService(o ServiceOptions) *Service {
	s := &Service{
		db: o.DB, capture: o.Capture, guard: o.Guard, sender: o.Sender,
		urlEnabled: o.URLEnabled, sem: make(chan struct{}, maxConcurrent), log: o.Logger, now: time.Now,
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	return s
}

// URLEnabled reports whether replays to URLs are allowed.
func (s *Service) URLEnabled() bool { return s.urlEnabled }

// History returns recent replay attempts for a request.
func (s *Service) History(ctx context.Context, requestID int64) ([]storage.ReplayAttempt, error) {
	return s.db.ListReplayAttempts(ctx, requestID, historyLimit)
}

// Replay validates the input and performs the replay.
func (s *Service) Replay(ctx context.Context, requestID int64, in Input) (Outcome, error) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return Outcome{}, ErrBusy
	}

	orig, err := s.db.GetRequest(ctx, requestID)
	if err != nil {
		return Outcome{}, err
	}

	errs := validate.Errors{}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = orig.Method
	}
	if !slices.Contains(mockresp.AllowedMethods, method) {
		errs.Add("method", "Method must be one of "+strings.Join(mockresp.AllowedMethods, ", ")+".")
	}
	pairs := in.Headers
	if pairs == nil {
		pairs = DefaultHeaders(orig)
	}
	header, herrs := buildHeader(pairs)
	for k, v := range herrs {
		errs.Add(k, v)
	}
	body := in.Body
	if body == nil {
		if orig.BodyTruncated {
			return Outcome{}, ErrTruncatedBody
		}
		body = orig.Body
	} else if len(body) > MaxBodyBytes {
		errs.Add("body", fmt.Sprintf("Body must be at most %d bytes.", MaxBodyBytes))
	}

	switch in.Target {
	case TargetURL:
		if !s.urlEnabled {
			return Outcome{}, ErrURLReplayDisabled
		}
		u, err := s.guard.ValidateURL(in.URL)
		if err != nil {
			errs.Add("url", err.Error())
		}
		if err := errs.Err(); err != nil {
			return Outcome{}, err
		}
		if !in.Confirmed {
			return Outcome{}, ErrConfirmationRequired
		}
		return s.replayToURL(ctx, orig, Request{URL: u, Method: method, Header: header, Body: body})
	case TargetEndpoint:
		var target storage.Endpoint
		if in.EndpointID <= 0 {
			errs.Add("endpoint_id", "Choose an endpoint.")
		} else if target, err = s.db.GetEndpoint(ctx, in.EndpointID); errors.Is(err, storage.ErrNotFound) {
			errs.Add("endpoint_id", "Endpoint not found.")
		} else if err != nil {
			return Outcome{}, err
		}
		if err := errs.Err(); err != nil {
			return Outcome{}, err
		}
		if !target.Enabled {
			return Outcome{}, ErrEndpointDisabled
		}
		return s.replayToEndpoint(ctx, orig, target, method, header, body)
	default:
		errs.Add("target", `Target must be "url" or "endpoint".`)
		return Outcome{}, errs
	}
}

func (s *Service) replayToURL(ctx context.Context, orig storage.CapturedRequest, req Request) (Outcome, error) {
	res := s.sender.Send(ctx, req)
	attempt := storage.ReplayAttempt{
		RequestID:       orig.ID,
		Target:          TargetURL,
		Destination:     req.URL.Scheme + "://" + req.URL.Host + req.URL.EscapedPath(),
		DestinationHost: req.URL.Hostname(),
		Method:          req.Method,
		Duration:        res.Duration,
		Outcome:         storage.OutcomeFailure,
		Summary:         s.sender.Summary(res),
		CreatedAt:       s.now(),
	}
	out := Outcome{}
	if res.Err == nil {
		status, size := res.Status, res.Size
		attempt.ResponseStatus = &status
		attempt.ResponseSize = &size
		if status < 400 {
			attempt.Outcome = storage.OutcomeSuccess
		}
		out.Response = &res
	}
	if err := s.db.InsertReplayAttempt(context.WithoutCancel(ctx), &attempt); err != nil {
		return Outcome{}, err
	}
	s.log.Info("replay sent", "request_id", orig.ID, "host", attempt.DestinationHost, "outcome", attempt.Outcome)
	out.Attempt = attempt
	return out, nil
}

func (s *Service) replayToEndpoint(ctx context.Context, orig storage.CapturedRequest, target storage.Endpoint, method string, header http.Header, body []byte) (Outcome, error) {
	start := s.now()
	created, resp, err := s.capture.Ingest(ctx, target, capture.Inbound{
		Method:   method,
		Path:     orig.Path,
		RawQuery: orig.RawQuery,
		Header:   header,
		Body:     body,
		Source:   storage.SourceReplay,
		ReplayOf: &orig.ID,
	})
	if err != nil {
		return Outcome{}, err
	}
	status, size := resp.Status, int64(len(resp.Body))
	attempt := storage.ReplayAttempt{
		RequestID:        orig.ID,
		Target:           TargetEndpoint,
		Destination:      target.Name,
		TargetEndpointID: &target.ID,
		ResultRequestID:  &created.ID,
		Method:           method,
		ResponseStatus:   &status,
		ResponseSize:     &size,
		Duration:         s.now().Sub(start),
		Outcome:          storage.OutcomeSuccess,
		Summary:          fmt.Sprintf("Recorded as request #%d in %s (%d %s)", created.ID, target.Name, status, http.StatusText(status)),
		CreatedAt:        s.now(),
	}
	if status >= 400 {
		attempt.Outcome = storage.OutcomeFailure
	}
	if err := s.db.InsertReplayAttempt(context.WithoutCancel(ctx), &attempt); err != nil {
		return Outcome{}, err
	}
	return Outcome{Attempt: attempt, Created: created, EndpointResponse: &resp}, nil
}

// buildHeader validates user-chosen headers.
func buildHeader(pairs [][2]string) (http.Header, validate.Errors) {
	errs := validate.Errors{}
	h := make(http.Header)
	if len(pairs) > MaxHeaders {
		errs.Add("headers", fmt.Sprintf("At most %d headers can be forwarded.", MaxHeaders))
		return h, errs
	}
	for _, p := range pairs {
		name, value := strings.TrimSpace(p[0]), p[1]
		lower := strings.ToLower(name)
		switch {
		case !validate.IsToken(name) || len(name) > maxHeaderNameLen:
			errs.Add("headers", fmt.Sprintf("%q is not a valid header name.", name))
		case !validate.IsHeaderValue(value) || len(value) > maxHeaderValueLen:
			errs.Add("headers", fmt.Sprintf("The value of %s contains line breaks or control characters, or is too long.", name))
		case neverForward[lower] != "":
			errs.Add("headers", fmt.Sprintf("%s cannot be forwarded (%s).", name, neverForward[lower]))
		case strings.HasPrefix(lower, "x-relaybox-"):
			errs.Add("headers", fmt.Sprintf("%s uses the reserved X-Relaybox- prefix.", name))
		default:
			h.Add(name, value)
		}
	}
	return h, errs
}
