// Package fixture converts captured requests to and from portable JSON
// fixture files. The format is documented in docs/api.md and described by
// api/fixture.schema.json.
//
// Exports re-apply the current redaction policy, leave out endpoint URLs,
// internal IDs, and sender addresses, and can omit bodies. Imports are
// validated strictly and only ever produce stored requests; nothing in a
// fixture is executed or sent anywhere.
package fixture

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/redact"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/validate"
)

// Format identifiers and limits.
const (
	Format            = "relaybox.fixture"
	Version           = 1
	MaxBytes          = 32 << 20
	MaxRequests       = 1000
	maxPathLen        = 2048
	maxQueryLen       = 8192
	maxHeaders        = 200
	maxHeaderNameLen  = 256
	maxHeaderValueLen = 8192
	maxProblems       = 20
)

// Body encodings.
const (
	EncodingUTF8   = "utf8"
	EncodingBase64 = "base64"
)

// Notice is included in every export.
const Notice = "Relaybox redacted the configured sensitive headers and fields before export. " +
	"Bodies can still contain personal data or secrets that the redaction rules did not match. " +
	"Review this file before sharing it."

// File is a fixture document.
type File struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	Generator  string    `json:"generator,omitempty"`
	Notice     string    `json:"notice,omitempty"`
	Requests   []Request `json:"requests"`
}

// Pair is a header name and value, encoded as a two-element JSON array.
type Pair [2]string

var errPairShape = errors.New("header entries must be [name, value] pairs of strings")

// UnmarshalJSON rejects arrays that do not have exactly two strings, which
// the default decoding of [2]string would silently accept.
func (p *Pair) UnmarshalJSON(b []byte) error {
	var parts []string
	if err := json.Unmarshal(b, &parts); err != nil || len(parts) != 2 {
		return errPairShape
	}
	p[0], p[1] = parts[0], parts[1]
	return nil
}

// Request is one recorded request.
type Request struct {
	Method      string             `json:"method"`
	Path        string             `json:"path"`
	Query       string             `json:"query,omitempty"`
	Headers     []Pair             `json:"headers"`
	Body        *Body              `json:"body,omitempty"`
	BodyOmitted bool               `json:"body_omitted,omitempty"`
	ReceivedAt  *time.Time         `json:"received_at,omitempty"`
	Endpoint    string             `json:"endpoint,omitempty"`
	Redactions  []redact.Redaction `json:"redactions,omitempty"`
}

// Body holds a request body. Bodies that are not valid UTF-8 text are
// base64 encoded.
type Body struct {
	Encoding  string `json:"encoding"`
	Data      string `json:"data"`
	Truncated bool   `json:"truncated,omitempty"`
}

// BuildOptions control an export.
type BuildOptions struct {
	IncludeBodies bool
	Generator     string
	Now           time.Time
}

// Build creates a sanitized fixture from stored requests.
func Build(reqs []storage.CapturedRequest, policy redact.Policy, opts BuildOptions) File {
	f := File{
		Format:     Format,
		Version:    Version,
		ExportedAt: opts.Now.UTC().Truncate(time.Millisecond),
		Generator:  opts.Generator,
		Notice:     Notice,
		Requests:   make([]Request, 0, len(reqs)),
	}
	for _, req := range reqs {
		header := http.Header{}
		for k, v := range req.Headers {
			if strings.EqualFold(k, "Host") {
				continue // the Relaybox host name is not part of the request being recorded
			}
			header[k] = v
		}
		headers, reds := policy.Headers(header)
		_, rawQuery, qreds := policy.Query(req.RawQuery)
		reds = mergeRedactions(req.Redactions, reds, qreds)

		received := req.ReceivedAt.UTC()
		out := Request{
			Method:     req.Method,
			Path:       req.Path,
			Query:      rawQuery,
			Headers:    sortedPairs(headers),
			ReceivedAt: &received,
			Endpoint:   req.EndpointName,
		}
		if opts.IncludeBodies {
			body, breds := policy.Body(req.ContentType, req.Body, req.BodyTruncated)
			reds = mergeRedactions(reds, breds)
			if len(body) > 0 || req.BodyTruncated {
				out.Body = EncodeBody(body, req.BodyTruncated)
			}
		} else if len(req.Body) > 0 {
			out.BodyOmitted = true
		}
		out.Redactions = reds
		f.Requests = append(f.Requests, out)
	}
	return f
}

// EncodeBody picks the encoding for a body.
func EncodeBody(b []byte, truncated bool) *Body {
	if utf8.Valid(b) {
		return &Body{Encoding: EncodingUTF8, Data: string(b), Truncated: truncated}
	}
	return &Body{Encoding: EncodingBase64, Data: base64.StdEncoding.EncodeToString(b), Truncated: truncated}
}

// Decode returns the body bytes.
func (b *Body) Decode() ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	switch b.Encoding {
	case EncodingUTF8:
		return []byte(b.Data), nil
	case EncodingBase64:
		return base64.StdEncoding.DecodeString(b.Data)
	}
	return nil, fmt.Errorf("unknown encoding %q", b.Encoding)
}

func sortedPairs(h map[string][]string) []Pair {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	slices.Sort(names)
	out := []Pair{}
	for _, k := range names {
		for _, v := range h[k] {
			out = append(out, Pair{k, v})
		}
	}
	return out
}

func mergeRedactions(lists ...[]redact.Redaction) []redact.Redaction {
	var out []redact.Redaction
	seen := make(map[redact.Redaction]bool)
	for _, list := range lists {
		for _, r := range list {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// ValidationError lists everything wrong with a fixture file.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid fixture: " + strings.Join(e.Problems, "; ")
}

// Parse decodes and validates a fixture. maxBodyBytes bounds each decoded
// request body.
func Parse(data []byte, maxBodyBytes int64) (File, error) {
	if len(data) > MaxBytes {
		return File{}, &ValidationError{[]string{fmt.Sprintf("The file is larger than %d MiB.", MaxBytes>>20)}}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return File{}, &ValidationError{[]string{describeDecodeError(err)}}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return File{}, &ValidationError{[]string{"The file contains data after the fixture object."}}
	}

	var problems []string
	add := func(format string, args ...any) {
		if len(problems) < maxProblems {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}
	switch {
	case f.Format != Format:
		return File{}, &ValidationError{[]string{fmt.Sprintf("This is not a Relaybox fixture (format must be %q).", Format)}}
	case f.Version > Version:
		return File{}, &ValidationError{[]string{fmt.Sprintf("Fixture version %d was created by a newer Relaybox. This version reads version %d.", f.Version, Version)}}
	case f.Version != Version:
		return File{}, &ValidationError{[]string{fmt.Sprintf("Unsupported fixture version %d.", f.Version)}}
	}
	if len(f.Requests) == 0 {
		add("The fixture contains no requests.")
	}
	if len(f.Requests) > MaxRequests {
		add("The fixture contains %d requests; the limit is %d.", len(f.Requests), MaxRequests)
	}
	for i, r := range f.Requests {
		if len(problems) >= maxProblems {
			break
		}
		at := fmt.Sprintf("requests[%d]", i)
		if !slices.Contains(mockresp.AllowedMethods, r.Method) {
			add("%s.method: %q is not one of %s.", at, r.Method, strings.Join(mockresp.AllowedMethods, ", "))
		}
		if !strings.HasPrefix(r.Path, "/") || len(r.Path) > maxPathLen || !validURLPart(r.Path, "?#") {
			add("%s.path: must start with / and contain no spaces, control characters, ? or #.", at)
		}
		if len(r.Query) > maxQueryLen || !validURLPart(r.Query, "#") {
			add("%s.query: must contain no spaces, control characters or #, and be at most %d characters.", at, maxQueryLen)
		}
		if len(r.Headers) > maxHeaders {
			add("%s.headers: at most %d headers are allowed.", at, maxHeaders)
		}
		for j, h := range r.Headers {
			if !validate.IsToken(h[0]) || len(h[0]) > maxHeaderNameLen {
				add("%s.headers[%d]: %q is not a valid header name.", at, j, h[0])
			} else if !validate.IsHeaderValue(h[1]) || len(h[1]) > maxHeaderValueLen {
				add("%s.headers[%d]: the value of %s contains control characters or is too long.", at, j, h[0])
			}
		}
		if r.Body != nil {
			switch b, err := r.Body.Decode(); {
			case err != nil:
				add("%s.body: %v.", at, err)
			case int64(len(b)) > maxBodyBytes:
				add("%s.body: %d bytes is more than the %d byte limit.", at, len(b), maxBodyBytes)
			}
		}
		for j, red := range r.Redactions {
			if !slices.Contains([]string{"header", "query", "body"}, red.Location) || len(red.Name) > 512 {
				add("%s.redactions[%d]: location must be header, query, or body.", at, j)
			}
		}
	}
	if len(problems) > 0 {
		return File{}, &ValidationError{problems}
	}
	return f, nil
}

func validURLPart(s, forbidden string) bool {
	for _, r := range s {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(forbidden, r) {
			return false
		}
	}
	return true
}

func describeDecodeError(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return fmt.Sprintf("The file is not valid JSON (error at byte %d).", syn.Offset)
	case errors.As(err, &typ):
		return fmt.Sprintf("Field %q has the wrong type (expected %s).", typ.Field, typ.Type)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "The file is empty or ends unexpectedly."
	case errors.Is(err, errPairShape):
		return "Header entries must be [name, value] pairs of strings."
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return "Unknown field " + strings.TrimPrefix(err.Error(), "json: unknown field ") + "."
	}
	return "The file could not be read as a fixture."
}

// Header converts fixture header pairs to an http.Header.
func (r Request) Header() http.Header {
	h := http.Header{}
	for _, p := range r.Headers {
		h.Add(p[0], p[1])
	}
	return h
}
