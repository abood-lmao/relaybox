package mockresp

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/abood-lmao/relaybox/internal/validate"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	big := strings.Repeat("a", MaxBodyBytes+1)
	cases := map[string]struct {
		cfg   Config
		field string
	}{
		"status low":         {Config{Status: 101}, "status"},
		"status high":        {Config{Status: 600}, "status"},
		"body too large":     {Config{Status: 200, ContentType: "text/plain", Body: big}, "body"},
		"204 with body":      {Config{Status: 204, ContentType: "text/plain", Body: "x"}, "body"},
		"invalid json":       {Config{Status: 200, ContentType: "application/json", Body: "{nope"}, "body"},
		"invalid json+json":  {Config{Status: 200, ContentType: "application/problem+json", Body: "{"}, "body"},
		"bad content type":   {Config{Status: 200, ContentType: "not a type"}, "content_type"},
		"crlf content type":  {Config{Status: 200, ContentType: "text/plain\r\nX-Evil: 1"}, "content_type"},
		"crlf header value":  {Config{Status: 200, Headers: map[string]string{"X-Test": "a\r\nSet-Cookie: x=1"}}, "headers"},
		"lf header value":    {Config{Status: 200, Headers: map[string]string{"X-Test": "a\nb"}}, "headers"},
		"nul header value":   {Config{Status: 200, Headers: map[string]string{"X-Test": "a\x00b"}}, "headers"},
		"bad header name":    {Config{Status: 200, Headers: map[string]string{"X Test": "a"}}, "headers"},
		"colon header name":  {Config{Status: 200, Headers: map[string]string{"X-Test:": "a"}}, "headers"},
		"hop-by-hop":         {Config{Status: 200, Headers: map[string]string{"Transfer-Encoding": "chunked"}}, "headers"},
		"content-length":     {Config{Status: 200, Headers: map[string]string{"content-length": "5"}}, "headers"},
		"set-cookie":         {Config{Status: 200, Headers: map[string]string{"Set-Cookie": "a=b"}}, "headers"},
		"csp":                {Config{Status: 200, Headers: map[string]string{"Content-Security-Policy": "default-src *"}}, "headers"},
		"reserved prefix":    {Config{Status: 200, Headers: map[string]string{"X-Relaybox-Id": "1"}}, "headers"},
		"duplicate by case":  {Config{Status: 200, Headers: map[string]string{"X-A": "1", "x-a": "2"}}, "headers"},
		"negative delay":     {Config{Status: 200, DelayMS: -1}, "delay_ms"},
		"delay too long":     {Config{Status: 200, DelayMS: MaxDelayMS + 1}, "delay_ms"},
		"content-type field": {Config{Status: 200, Headers: map[string]string{"Content-Type": "text/html"}}, "headers"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.cfg.Validate()
			var verr validate.Errors
			if !errors.As(err, &verr) {
				t.Fatalf("Validate() = %v, want validate.Errors", err)
			}
			if _, ok := verr[tc.field]; !ok {
				t.Fatalf("expected error on %q, got %v", tc.field, verr)
			}
		})
	}
}

func TestValidateAcceptsPlainTextAndCustomHeaders(t *testing.T) {
	cfg := Config{
		Status:      201,
		ContentType: "text/plain; charset=utf-8",
		Body:        "{not json but that's fine}",
		Headers:     map[string]string{"x-request-source": "relaybox test", "Retry-After": "120"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	n := cfg.Normalize()
	if n.Headers["X-Request-Source"] != "relaybox test" {
		t.Fatalf("header names should be canonicalized: %v", n.Headers)
	}
}

func TestBuildSetsServerHeadersAndBody(t *testing.T) {
	cfg := Config{Status: 202, ContentType: "application/json", Body: `{"ok":true}`, Headers: map[string]string{"X-Custom": "1"}}
	resp := cfg.Build(http.MethodPost)
	if resp.Status != 202 || string(resp.Body) != `{"ok":true}` {
		t.Fatalf("unexpected response %+v", resp)
	}
	checks := map[string]string{
		"Content-Type":            "application/json",
		"Content-Length":          "11",
		"X-Custom":                "1",
		"Content-Security-Policy": "sandbox; default-src 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "no-store",
	}
	for k, want := range checks {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestBuildHeadHasNoBody(t *testing.T) {
	resp := Default().Build(http.MethodHead)
	if len(resp.Body) != 0 {
		t.Fatalf("HEAD response has a body: %q", resp.Body)
	}
	if resp.Header.Get("Content-Length") != "17" {
		t.Fatalf("HEAD should advertise the GET body length, got %q", resp.Header.Get("Content-Length"))
	}
}

func TestBuildNoContentStatus(t *testing.T) {
	resp := Config{Status: 204}.Build(http.MethodPost)
	if len(resp.Body) != 0 || resp.Header.Get("Content-Length") != "" || resp.Header.Get("Content-Type") != "" {
		t.Fatalf("204 response must not carry a body or entity headers: %+v", resp)
	}
}

func TestBuildOptionsAddsAllow(t *testing.T) {
	resp := Default().Build(http.MethodOptions)
	if !strings.Contains(resp.Header.Get("Allow"), "POST") {
		t.Fatalf("Allow header missing: %v", resp.Header)
	}
}

func TestIsJSONContentType(t *testing.T) {
	for ct, want := range map[string]bool{
		"application/json":                  true,
		"application/json; charset=utf-8":   true,
		"application/vnd.api+json":          true,
		"text/plain":                        false,
		"application/x-www-form-urlencoded": false,
	} {
		if got := IsJSONContentType(ct); got != want {
			t.Errorf("IsJSONContentType(%q) = %v, want %v", ct, got, want)
		}
	}
}
