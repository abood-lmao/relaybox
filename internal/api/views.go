package api

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/compare"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/redact"
	"github.com/abood-lmao/relaybox/internal/storage"
)

// EndpointView is the API representation of an endpoint.
type EndpointView struct {
	ID                    int64           `json:"id"`
	Name                  string          `json:"name"`
	Enabled               bool            `json:"enabled"`
	URL                   string          `json:"url"`
	PublicID              string          `json:"public_id"`
	MockResponse          mockresp.Config `json:"mock_response"`
	MaxBodyBytes          *int64          `json:"max_body_bytes"`
	EffectiveMaxBodyBytes int64           `json:"effective_max_body_bytes"`
	RequestCount          int64           `json:"request_count"`
	LastRequestAt         *time.Time      `json:"last_request_at"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

func (a *API) endpointView(e storage.Endpoint) EndpointView {
	effective := a.settings.Current().DefaultMaxBodyBytes
	if e.MaxBodyBytes != nil {
		effective = min(*e.MaxBodyBytes, a.settings.Ceiling())
	}
	mock := e.Mock
	if mock.Headers == nil {
		mock.Headers = map[string]string{}
	}
	return EndpointView{
		ID: e.ID, Name: e.Name, Enabled: e.Enabled,
		URL:          a.hookURL(e.PublicID),
		PublicID:     e.PublicID,
		MockResponse: mock, MaxBodyBytes: e.MaxBodyBytes, EffectiveMaxBodyBytes: effective,
		RequestCount: e.RequestCount, LastRequestAt: e.LastRequestAt,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
}

func (a *API) hookURL(publicID string) string {
	return a.cfg.PublicBaseURL + capture.PathPrefix + publicID
}

// EndpointRef identifies an endpoint inside other objects.
type EndpointRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// RequestSummaryView is a row in the request list.
type RequestSummaryView struct {
	ID            int64       `json:"id"`
	Endpoint      EndpointRef `json:"endpoint"`
	Source        string      `json:"source"`
	ReplayOf      *int64      `json:"replay_of"`
	Method        string      `json:"method"`
	Path          string      `json:"path"`
	QueryString   string      `json:"query_string"`
	Status        int         `json:"status"`
	DurationMS    float64     `json:"duration_ms"`
	BodySize      int64       `json:"body_size"`
	BodyTruncated bool        `json:"body_truncated"`
	ContentType   string      `json:"content_type"`
	ReceivedAt    time.Time   `json:"received_at"`
	Redacted      bool        `json:"redacted"`
}

func summaryView(s storage.RequestSummary) RequestSummaryView {
	return RequestSummaryView{
		ID: s.ID, Endpoint: EndpointRef{s.EndpointID, s.EndpointName}, Source: s.Source, ReplayOf: s.ReplayOf,
		Method: s.Method, Path: s.Path, QueryString: s.RawQuery, Status: s.ResponseStatus,
		DurationMS: durationMS(s.Duration), BodySize: s.BodySize, BodyTruncated: s.BodyTruncated,
		ContentType: s.ContentType, ReceivedAt: s.ReceivedAt, Redacted: s.Redacted,
	}
}

func durationMS(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// RequestDetailView is a captured request with everything stored about it.
type RequestDetailView struct {
	RequestSummaryView
	URL            string             `json:"url"`
	Query          [][2]string        `json:"query"`
	Headers        [][2]string        `json:"headers"`
	Body           BodyView           `json:"body"`
	ContentLength  *int64             `json:"content_length"`
	RemoteAddr     string             `json:"remote_addr"`
	Response       ResponseView       `json:"response"`
	Redactions     []redact.Redaction `json:"redactions"`
	ReplayChildren []ReplayChildView  `json:"replay_children"`
}

// ResponseView is the response Relaybox sent, or a replay destination
// returned.
type ResponseView struct {
	Status     int         `json:"status"`
	StatusText string      `json:"status_text"`
	Headers    [][2]string `json:"headers"`
	Body       BodyView    `json:"body"`
}

// ReplayChildView links to a request created by replaying this one.
type ReplayChildView struct {
	ID         int64       `json:"id"`
	Endpoint   EndpointRef `json:"endpoint"`
	Status     int         `json:"status"`
	ReceivedAt time.Time   `json:"received_at"`
}

func (a *API) detailView(req storage.CapturedRequest, children []storage.ReplayChild) RequestDetailView {
	summary := RequestSummaryView{
		ID: req.ID, Endpoint: EndpointRef{req.EndpointID, req.EndpointName}, Source: req.Source, ReplayOf: req.ReplayOf,
		Method: req.Method, Path: req.Path, QueryString: req.RawQuery, Status: req.ResponseStatus,
		DurationMS: durationMS(req.Duration), BodySize: req.BodySize, BodyTruncated: req.BodyTruncated,
		ContentType: req.ContentType, ReceivedAt: req.ReceivedAt, Redacted: len(req.Redactions) > 0,
	}
	u := a.hookURL(req.EndpointPublicID)
	if req.Path != "/" {
		u += req.Path
	}
	if req.RawQuery != "" {
		u += "?" + req.RawQuery
	}
	query := make([][2]string, 0, len(req.Query))
	for _, p := range req.Query {
		query = append(query, [2]string{p[0], p[1]})
	}
	redactions := req.Redactions
	if redactions == nil {
		redactions = []redact.Redaction{}
	}
	kids := make([]ReplayChildView, 0, len(children))
	for _, c := range children {
		kids = append(kids, ReplayChildView{ID: c.ID, Endpoint: EndpointRef{c.EndpointID, c.EndpointName}, Status: c.ResponseStatus, ReceivedAt: c.ReceivedAt})
	}
	respHeaders := http.Header(req.ResponseHeaders)
	return RequestDetailView{
		RequestSummaryView: summary,
		URL:                u,
		Query:              query,
		Headers:            sortedPairs(req.Headers),
		Body:               makeBodyView(req.Body, req.ContentType, req.BodySize, req.BodyTruncated),
		ContentLength:      req.ContentLength,
		RemoteAddr:         req.RemoteAddr,
		Response: ResponseView{
			Status:     req.ResponseStatus,
			StatusText: http.StatusText(req.ResponseStatus),
			Headers:    sortedPairs(req.ResponseHeaders),
			Body:       makeBodyView(req.ResponseBody, respHeaders.Get("Content-Type"), int64(len(req.ResponseBody)), false),
		},
		Redactions:     redactions,
		ReplayChildren: kids,
	}
}

func sortedPairs(h map[string][]string) [][2]string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	out := [][2]string{}
	for _, k := range names {
		for _, v := range h[k] {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// Body kinds.
const (
	KindEmpty     = "empty"
	KindJSON      = "json"
	KindText      = "text"
	KindForm      = "form"
	KindMultipart = "multipart"
	KindBinary    = "binary"
)

// BodyView carries a body with enough information to display it safely.
// Text bodies are sent as UTF-8; anything else is base64 encoded.
type BodyView struct {
	Encoding    string      `json:"encoding"`
	Data        string      `json:"data"`
	Size        int64       `json:"size"`
	StoredBytes int         `json:"stored_bytes"`
	Truncated   bool        `json:"truncated"`
	Kind        string      `json:"kind"`
	ContentType string      `json:"content_type"`
	ParseError  string      `json:"parse_error,omitempty"`
	Form        [][2]string `json:"form,omitempty"`
	Parts       []PartView  `json:"parts,omitempty"`
}

// PartView is one part of a multipart body.
type PartView struct {
	Name        string      `json:"name"`
	Filename    string      `json:"filename,omitempty"`
	ContentType string      `json:"content_type,omitempty"`
	Size        int         `json:"size"`
	Text        *string     `json:"text,omitempty"`
	Headers     [][2]string `json:"headers"`
}

const (
	maxPartsShown   = 100
	maxPartTextSize = 64 << 10
)

func makeBodyView(body []byte, contentType string, size int64, truncated bool) BodyView {
	v := BodyView{Size: size, StoredBytes: len(body), Truncated: truncated, ContentType: contentType, Encoding: "utf8"}
	if size < int64(len(body)) {
		v.Size = int64(len(body))
	}
	mt, params, _ := mime.ParseMediaType(contentType)
	isText := utf8.Valid(body) && bytes.IndexByte(body, 0) < 0
	if isText {
		v.Data = string(body)
	} else {
		v.Encoding = "base64"
		v.Data = base64.StdEncoding.EncodeToString(body)
	}

	switch {
	case len(body) == 0:
		v.Kind = KindEmpty
	case mt == "multipart/form-data" && params["boundary"] != "":
		v.Kind = KindMultipart
		v.Parts, v.ParseError = parseMultipart(body, params["boundary"])
	case !isText:
		v.Kind = KindBinary
	case mt == "application/x-www-form-urlencoded":
		v.Kind = KindForm
		v.Form = parseForm(string(body))
	case strings.Contains(mt, "json") || redact.LooksLikeJSON(body):
		if msg := compare.JSONError(body); msg != "" {
			v.Kind = KindText
			if strings.Contains(mt, "json") || !truncated {
				v.ParseError = msg
			}
		} else {
			v.Kind = KindJSON
		}
	default:
		v.Kind = KindText
	}
	return v
}

func parseForm(s string) [][2]string {
	out := [][2]string{}
	for _, seg := range strings.Split(s, "&") {
		if seg == "" {
			continue
		}
		k, v, _ := strings.Cut(seg, "=")
		if uk, err := url.QueryUnescape(k); err == nil {
			k = uk
		}
		if uv, err := url.QueryUnescape(v); err == nil {
			v = uv
		}
		out = append(out, [2]string{k, v})
	}
	return out
}

func parseMultipart(body []byte, boundary string) ([]PartView, string) {
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := []PartView{}
	for len(parts) < maxPartsShown {
		p, err := mr.NextRawPart()
		if err == io.EOF {
			return parts, ""
		}
		if err != nil {
			return parts, "The multipart body is incomplete or malformed; showing the parts that could be read."
		}
		data, err := io.ReadAll(io.LimitReader(p, int64(len(body))))
		view := PartView{
			Name:        p.FormName(),
			Filename:    p.FileName(),
			ContentType: p.Header.Get("Content-Type"),
			Size:        len(data),
			Headers:     sortedPairs(p.Header),
		}
		if len(data) <= maxPartTextSize && utf8.Valid(data) && bytes.IndexByte(data, 0) < 0 {
			text := string(data)
			view.Text = &text
		}
		parts = append(parts, view)
		if err != nil {
			return parts, "The multipart body is incomplete or malformed; showing the parts that could be read."
		}
	}
	return parts, "Only the first 100 parts are shown."
}

// ReplayAttemptView is one entry of a request's replay history.
type ReplayAttemptView struct {
	ID               int64     `json:"id"`
	RequestID        int64     `json:"request_id"`
	Target           string    `json:"target"`
	Destination      string    `json:"destination"`
	DestinationHost  string    `json:"destination_host"`
	TargetEndpointID *int64    `json:"target_endpoint_id"`
	ResultRequestID  *int64    `json:"result_request_id"`
	Method           string    `json:"method"`
	ResponseStatus   *int      `json:"response_status"`
	ResponseSize     *int64    `json:"response_size"`
	DurationMS       float64   `json:"duration_ms"`
	Outcome          string    `json:"outcome"`
	Summary          string    `json:"summary"`
	CreatedAt        time.Time `json:"created_at"`
}

func attemptView(a storage.ReplayAttempt) ReplayAttemptView {
	return ReplayAttemptView{
		ID: a.ID, RequestID: a.RequestID, Target: a.Target, Destination: a.Destination,
		DestinationHost: a.DestinationHost, TargetEndpointID: a.TargetEndpointID,
		ResultRequestID: a.ResultRequestID, Method: a.Method, ResponseStatus: a.ResponseStatus,
		ResponseSize: a.ResponseSize, DurationMS: durationMS(a.Duration), Outcome: a.Outcome,
		Summary: a.Summary, CreatedAt: a.CreatedAt,
	}
}
