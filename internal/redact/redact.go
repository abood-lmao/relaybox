// Package redact removes sensitive values from captured requests before they
// are stored or exported.
//
// Header values are replaced for configured header names. Field names are
// matched against query parameters, JSON object keys, and form fields after
// normalization (case-insensitive, ignoring "_", "-", "." and spaces), so
// "api_key", "apiKey", and "API-KEY" all match the same entry.
//
// JSON bodies are rewritten in place: only the matched values change, and
// the rest of the document keeps its original bytes and formatting.
package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/abood-lmao/relaybox/internal/validate"
)

// Placeholder replaces redacted values.
const Placeholder = "[REDACTED]"

// DefaultHeaders are redacted unless the user changes the list.
var DefaultHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"Set-Cookie",
	"X-Api-Key",
	"Api-Key",
	"X-Auth-Token",
	"X-Access-Token",
	"X-Amz-Security-Token",
	"X-Goog-Api-Key",
	"X-Gitlab-Token",
	"X-Hook-Secret",
	"X-Webhook-Secret",
	"X-Telegram-Bot-Api-Secret-Token",
}

// DefaultFields are redacted in query strings, JSON bodies, and form bodies.
var DefaultFields = []string{
	"password",
	"passwd",
	"secret",
	"client_secret",
	"api_key",
	"access_token",
	"refresh_token",
	"id_token",
	"token",
	"private_key",
	"authorization",
	"card_number",
	"cvc",
	"cvv",
}

// Redaction records one value that was replaced.
type Redaction struct {
	Location string `json:"location"` // header, query, or body
	Name     string `json:"name"`     // header name, query key, JSON path, or form key
}

// Pair is an ordered key/value pair, encoded in JSON as a two-element array.
type Pair [2]string

// Policy decides which values to redact.
type Policy struct {
	headers map[string]bool
	fields  map[string]bool
}

// NewPolicy builds a policy from header names and field names.
func NewPolicy(headers, fields []string) Policy {
	p := Policy{headers: make(map[string]bool), fields: make(map[string]bool)}
	for _, h := range headers {
		if h = strings.TrimSpace(h); h != "" {
			p.headers[strings.ToLower(h)] = true
		}
	}
	for _, f := range fields {
		if n := NormalizeField(f); n != "" {
			p.fields[n] = true
		}
	}
	return p
}

// NormalizeField lowercases a field name and strips separators.
func NormalizeField(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case '_', '-', '.', ' ':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// HeaderRedacted reports whether a header name is covered by the policy.
func (p Policy) HeaderRedacted(name string) bool {
	return p.headers[strings.ToLower(name)]
}

// FieldRedacted reports whether a query, JSON, or form field name is covered.
// Bracketed form keys such as user[password] match on their last segment.
func (p Policy) FieldRedacted(name string) bool {
	if len(p.fields) == 0 {
		return false
	}
	if p.fields[NormalizeField(name)] {
		return true
	}
	if i := strings.LastIndexByte(name, '['); i >= 0 && strings.HasSuffix(name, "]") {
		return p.fields[NormalizeField(name[i+1:len(name)-1])]
	}
	return false
}

// Headers returns a redacted copy of h.
func (p Policy) Headers(h http.Header) (map[string][]string, []Redaction) {
	out := make(map[string][]string, len(h))
	var reds []Redaction
	for name, values := range h {
		if !p.HeaderRedacted(name) {
			out[name] = append([]string(nil), values...)
			continue
		}
		rv := make([]string, len(values))
		for i, v := range values {
			rv[i] = redactHeaderValue(name, v)
		}
		out[name] = rv
		reds = append(reds, Redaction{Location: "header", Name: name})
	}
	sort.Slice(reds, func(i, j int) bool { return reds[i].Name < reds[j].Name })
	return out, reds
}

// redactHeaderValue keeps the parts of a value that help debugging without
// revealing credentials: the auth scheme and cookie names.
func redactHeaderValue(name, v string) string {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization":
		scheme, _, found := strings.Cut(strings.TrimSpace(v), " ")
		if found && len(scheme) <= 24 && validate.IsToken(scheme) {
			return scheme + " " + Placeholder
		}
	case "cookie":
		parts := strings.Split(v, ";")
		for i, part := range parts {
			k, _, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok && validate.IsToken(k) {
				parts[i] = k + "=" + Placeholder
			} else {
				parts[i] = Placeholder
			}
		}
		return strings.Join(parts, "; ")
	case "set-cookie":
		k, _, ok := strings.Cut(v, "=")
		if k = strings.TrimSpace(k); ok && validate.IsToken(k) {
			return k + "=" + Placeholder
		}
	}
	return Placeholder
}

// Query parses a raw query string into ordered pairs and returns the pairs
// and the raw string with sensitive values replaced. The original encoding
// of untouched parameters is preserved.
func (p Policy) Query(raw string) ([]Pair, string, []Redaction) {
	if raw == "" {
		return []Pair{}, "", nil
	}
	segments := strings.Split(raw, "&")
	pairs := make([]Pair, 0, len(segments))
	var reds []Redaction
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		rawKey, rawVal, _ := strings.Cut(seg, "=")
		key := unescape(rawKey)
		val := unescape(rawVal)
		if p.FieldRedacted(key) {
			val = Placeholder
			segments[i] = rawKey + "=" + url.QueryEscape(Placeholder)
			reds = append(reds, Redaction{Location: "query", Name: key})
		}
		pairs = append(pairs, Pair{key, val})
	}
	return pairs, strings.Join(segments, "&"), reds
}

func unescape(s string) string {
	if u, err := url.QueryUnescape(s); err == nil {
		return u
	}
	return s
}

// Body redacts configured fields in JSON and URL-encoded form bodies. Other
// content types, and complete JSON that fails to parse, are returned
// unchanged.
//
// When truncated is true the body was cut off at the size limit. Matched
// values are still replaced; if the cut falls inside a matched value, or the
// JSON is malformed before the cut, the body is shortened to the last point
// that could be checked, so an unredacted secret is never kept.
func (p Policy) Body(contentType string, body []byte, truncated bool) ([]byte, []Redaction) {
	if len(p.fields) == 0 || len(body) == 0 {
		return body, nil
	}
	mt, params, _ := mime.ParseMediaType(contentType)
	switch {
	case mt == "application/x-www-form-urlencoded":
		out, names := p.redactForm(body)
		return out, toRedactions(names)
	case mt == "multipart/form-data":
		out, names := p.redactMultipart(body, params["boundary"])
		return out, toRedactions(names)
	case isJSONType(mt) || LooksLikeJSON(body):
		out, paths, err := p.redactJSON(body, truncated)
		if err != nil {
			return body, nil
		}
		return out, toRedactions(paths)
	}
	return body, nil
}

func toRedactions(names []string) []Redaction {
	if len(names) == 0 {
		return nil
	}
	out := make([]Redaction, len(names))
	for i, n := range names {
		out[i] = Redaction{Location: "body", Name: n}
	}
	return out
}

func isJSONType(mt string) bool {
	return mt == "application/json" || strings.HasSuffix(mt, "+json") ||
		mt == "application/x-ndjson" || mt == "text/json"
}

// LooksLikeJSON reports whether body starts like a JSON object or array.
func LooksLikeJSON(body []byte) bool {
	t := bytes.TrimLeft(body, " \t\r\n")
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}

func (p Policy) redactForm(body []byte) ([]byte, []string) {
	parts := strings.Split(string(body), "&")
	var names []string
	for i, part := range parts {
		rawKey, _, _ := strings.Cut(part, "=")
		key := unescape(rawKey)
		if key != "" && p.FieldRedacted(key) {
			parts[i] = rawKey + "=" + url.QueryEscape(Placeholder)
			names = append(names, key)
		}
	}
	if len(names) == 0 {
		return body, nil
	}
	return []byte(strings.Join(parts, "&")), names
}

const maxMultipartParts = 1000

// redactMultipart replaces the content of parts whose form field name
// matches. It works on the raw bytes so unmatched parts, including binary
// file content, are kept exactly. A part cut off by truncation is redacted
// up to the end of the body.
func (p Policy) redactMultipart(body []byte, boundary string) ([]byte, []string) {
	if boundary == "" || len(boundary) > 200 {
		return body, nil
	}
	delim := []byte("--" + boundary)
	start := bytes.Index(body, delim)
	if start < 0 {
		return body, nil
	}

	var (
		out   bytes.Buffer
		names []string
		last  int
	)
	pos := start
	for parts := 0; parts < maxMultipartParts; parts++ {
		after := pos + len(delim)
		if bytes.HasPrefix(body[after:], []byte("--")) {
			break // closing delimiter
		}
		lineEnd := bytes.Index(body[after:], []byte("\r\n"))
		if lineEnd < 0 {
			break
		}
		partStart := after + lineEnd + 2
		next := bytes.Index(body[partStart:], append([]byte("\r\n"), delim...))
		partEnd := len(body)
		if next >= 0 {
			partEnd = partStart + next
		}
		headerEnd := bytes.Index(body[partStart:partEnd], []byte("\r\n\r\n"))
		if headerEnd >= 0 {
			contentStart := partStart + headerEnd + 4
			if name := multipartFieldName(body[partStart : partStart+headerEnd+2]); name != "" && p.FieldRedacted(name) {
				out.Write(body[last:contentStart])
				out.WriteString(Placeholder)
				last = partEnd
				names = append(names, name)
			}
		}
		if next < 0 {
			break
		}
		pos = partEnd + 2
	}
	if len(names) == 0 {
		return body, nil
	}
	out.Write(body[last:])
	return out.Bytes(), names
}

func multipartFieldName(headerBlock []byte) string {
	for _, line := range strings.Split(string(headerBlock), "\r\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Content-Disposition") {
			continue
		}
		if _, params, err := mime.ParseMediaType(strings.TrimSpace(value)); err == nil {
			return params["name"]
		}
	}
	return ""
}

type jsonFrame struct {
	object    bool
	expectKey bool
	key       string
	index     int
	path      string
}

// redactJSON walks the token stream and records the byte span of every value
// whose key matches. Spans are replaced afterwards, so unmatched content keeps
// its exact original bytes. In partial mode (truncated input) parse errors cut
// the output instead of failing.
func (p Policy) redactJSON(body []byte, partial bool) ([]byte, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	var (
		stack []jsonFrame
		spans [][2]int64
		paths []string
	)
	cut := func(at int64) ([]byte, []string, error) {
		return applySpans(body[:at], spans), paths, nil
	}
	childPath := func() string {
		if len(stack) == 0 {
			return "$"
		}
		top := stack[len(stack)-1]
		if top.object {
			return JoinPath(top.path, top.key)
		}
		return fmt.Sprintf("%s[%d]", top.path, top.index)
	}
	valueDone := func() {
		if len(stack) == 0 {
			return
		}
		top := &stack[len(stack)-1]
		if top.object {
			top.expectKey = true
		} else {
			top.index++
		}
	}

	for {
		lastGood := dec.InputOffset()
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if !partial {
				return nil, nil, err
			}
			if isTruncation(err) {
				// The incomplete final token belongs to an unmatched key.
				return cut(int64(len(body)))
			}
			return cut(lastGood)
		}

		if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].expectKey {
			if d, ok := tok.(json.Delim); ok && d == '}' {
				stack = stack[:n-1]
				valueDone()
				continue
			}
			key, ok := tok.(string)
			if !ok {
				if partial {
					return cut(lastGood)
				}
				return nil, nil, fmt.Errorf("unexpected token %v", tok)
			}
			top := &stack[n-1]
			top.key = key
			top.expectKey = false
			if p.FieldRedacted(key) {
				start := valueStart(body, dec.InputOffset())
				path := JoinPath(top.path, key)
				if err := skipValue(dec); err != nil {
					if !partial {
						return nil, nil, err
					}
					paths = append(paths, path)
					return cut(start)
				}
				spans = append(spans, [2]int64{start, dec.InputOffset()})
				paths = append(paths, path)
				top.expectKey = true
			}
			continue
		}

		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{':
				stack = append(stack, jsonFrame{object: true, expectKey: true, path: childPath()})
			case '[':
				stack = append(stack, jsonFrame{path: childPath()})
			case ']', '}':
				if len(stack) == 0 {
					if partial {
						return cut(lastGood)
					}
					return nil, nil, fmt.Errorf("unbalanced %q", d)
				}
				stack = stack[:len(stack)-1]
				valueDone()
			}
			continue
		}
		valueDone()
	}
	if len(stack) != 0 && !partial {
		return nil, nil, io.ErrUnexpectedEOF
	}
	return applySpans(body, spans), paths, nil
}

func isTruncation(err error) bool {
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

func applySpans(body []byte, spans [][2]int64) []byte {
	if len(spans) == 0 {
		return body
	}
	replacement := []byte(`"` + Placeholder + `"`)
	var out bytes.Buffer
	out.Grow(len(body))
	var last int64
	for _, s := range spans {
		out.Write(body[last:s[0]])
		out.Write(replacement)
		last = s[1]
	}
	out.Write(body[last:])
	return out.Bytes()
}

// valueStart skips whitespace and the colon that follow an object key.
func valueStart(body []byte, keyEnd int64) int64 {
	i := keyEnd
	for i < int64(len(body)) {
		switch body[i] {
		case ' ', '\t', '\r', '\n', ':':
			i++
			continue
		}
		break
	}
	return i
}

func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok || (d != '{' && d != '[') {
		return nil
	}
	for depth := 1; depth > 0; {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

var identifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// JoinPath appends an object key to a JSON path using dot notation when the
// key is a plain identifier and bracket notation otherwise.
func JoinPath(path, key string) string {
	if identifier.MatchString(key) {
		return path + "." + key
	}
	quoted, _ := json.Marshal(key)
	return path + "[" + string(quoted) + "]"
}
