// Package mockresp defines the response an endpoint returns to senders and
// validates user-supplied response configuration.
package mockresp

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/abood-lmao/relaybox/internal/validate"
)

// Limits for mock response configuration.
const (
	MaxBodyBytes       = 64 << 10
	MaxHeaders         = 32
	MaxHeaderNameLen   = 128
	MaxHeaderValueLen  = 2048
	MaxContentTypeLen  = 255
	MaxDelayMS         = 10000
	DefaultContentType = "application/json"
	DefaultBody        = `{"received":true}`
)

// AllowedMethods lists the request methods endpoints accept, in Allow header order.
var AllowedMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

// forbiddenHeaders are transport-level or server-controlled headers that a
// mock response may not set. Hook responses share an origin with the
// dashboard, so headers that affect cookies, storage, or security policy are
// reserved for the server.
var forbiddenHeaders = map[string]string{
	"connection":                          "hop-by-hop header",
	"keep-alive":                          "hop-by-hop header",
	"proxy-connection":                    "hop-by-hop header",
	"transfer-encoding":                   "transport header",
	"te":                                  "hop-by-hop header",
	"trailer":                             "hop-by-hop header",
	"upgrade":                             "hop-by-hop header",
	"proxy-authenticate":                  "hop-by-hop header",
	"proxy-authorization":                 "hop-by-hop header",
	"content-length":                      "set by the server",
	"content-encoding":                    "set by the server",
	"content-type":                        "use the content type field instead",
	"date":                                "set by the server",
	"host":                                "request header",
	"expect":                              "request header",
	"set-cookie":                          "cookies would be set on the dashboard origin",
	"set-cookie2":                         "cookies would be set on the dashboard origin",
	"clear-site-data":                     "would affect the dashboard origin",
	"content-security-policy":             "set by the server",
	"content-security-policy-report-only": "set by the server",
	"strict-transport-security":           "would affect the dashboard origin",
	"alt-svc":                             "would affect the dashboard origin",
	"service-worker-allowed":              "would affect the dashboard origin",
	"x-content-type-options":              "set by the server",
}

// Config is an endpoint's mock response.
type Config struct {
	Status      int               `json:"status"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	DelayMS     int               `json:"delay_ms"`
}

// Default returns the response used by new endpoints.
func Default() Config {
	return Config{
		Status:      http.StatusOK,
		ContentType: DefaultContentType,
		Headers:     map[string]string{},
		Body:        DefaultBody,
	}
}

// Normalize canonicalizes header names. Call it after Validate, which
// detects names that differ only in case.
func (c Config) Normalize() Config {
	out := c
	out.ContentType = strings.TrimSpace(c.ContentType)
	out.Headers = make(map[string]string, len(c.Headers))
	for k, v := range c.Headers {
		out.Headers[textproto.CanonicalMIMEHeaderKey(k)] = v
	}
	return out
}

// Validate checks the configuration and returns validate.Errors on failure.
func (c Config) Validate() error {
	c.ContentType = strings.TrimSpace(c.ContentType)
	errs := validate.Errors{}

	if c.Status < 200 || c.Status > 599 {
		errs.Add("status", "Status must be between 200 and 599.")
	}
	if len(c.Body) > MaxBodyBytes {
		errs.Add("body", fmt.Sprintf("Body must be at most %d bytes.", MaxBodyBytes))
	}
	if (c.Status == http.StatusNoContent || c.Status == http.StatusNotModified) && c.Body != "" {
		errs.Add("body", fmt.Sprintf("Status %d responses cannot have a body.", c.Status))
	}
	if c.DelayMS < 0 || c.DelayMS > MaxDelayMS {
		errs.Add("delay_ms", fmt.Sprintf("Delay must be between 0 and %d milliseconds.", MaxDelayMS))
	}

	if c.ContentType != "" {
		if len(c.ContentType) > MaxContentTypeLen || !validate.IsHeaderValue(c.ContentType) {
			errs.Add("content_type", "Content type is invalid.")
		} else if _, _, err := mime.ParseMediaType(c.ContentType); err != nil {
			errs.Add("content_type", "Content type must be a media type such as application/json or text/plain.")
		} else if IsJSONContentType(c.ContentType) && c.Body != "" && !json.Valid([]byte(c.Body)) {
			errs.Add("body", "Body is not valid JSON. Fix it, or change the content type to text/plain to send it as-is.")
		}
	}

	if len(c.Headers) > MaxHeaders {
		errs.Add("headers", fmt.Sprintf("At most %d headers are allowed.", MaxHeaders))
	}
	seen := make(map[string]bool, len(c.Headers))
	for name, value := range c.Headers {
		lower := strings.ToLower(name)
		switch {
		case !validate.IsToken(name) || len(name) > MaxHeaderNameLen:
			errs.Add("headers", fmt.Sprintf("%q is not a valid header name.", name))
		case !validate.IsHeaderValue(value) || len(value) > MaxHeaderValueLen:
			errs.Add("headers", fmt.Sprintf("The value of %s contains line breaks or control characters, or is too long.", name))
		case forbiddenHeaders[lower] != "":
			errs.Add("headers", fmt.Sprintf("%s cannot be set (%s).", name, forbiddenHeaders[lower]))
		case strings.HasPrefix(lower, "x-relaybox-"):
			errs.Add("headers", fmt.Sprintf("%s uses the reserved X-Relaybox- prefix.", name))
		case seen[lower]:
			errs.Add("headers", fmt.Sprintf("%s is listed more than once.", name))
		}
		seen[lower] = true
	}
	return errs.Err()
}

// IsJSONContentType reports whether a media type denotes JSON.
func IsJSONContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// Response is a fully built HTTP response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Build produces the response for a request with the given method. HEAD
// requests and 204/304 statuses get no body, as HTTP requires.
func (c Config) Build(method string) Response {
	h := make(http.Header)
	h.Set("Cache-Control", "no-store")
	for k, v := range c.Headers {
		h.Set(k, v)
	}
	ServerHeaders(h)
	if method == http.MethodOptions && h.Get("Allow") == "" {
		h.Set("Allow", strings.Join(AllowedMethods, ", "))
	}

	body := []byte(c.Body)
	noBody := c.Status == http.StatusNoContent || c.Status == http.StatusNotModified
	if !noBody {
		if len(body) > 0 {
			ct := c.ContentType
			if ct == "" {
				ct = "text/plain; charset=utf-8"
			}
			h.Set("Content-Type", ct)
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
	}
	if noBody || method == http.MethodHead {
		body = nil
	}
	return Response{Status: c.Status, Header: h, Body: body}
}

// ServerHeaders adds the headers Relaybox sets on every hook response.
// The sandbox policy stops a browser that opens a hook URL from running a
// configured HTML body with access to the dashboard origin.
func ServerHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
}
