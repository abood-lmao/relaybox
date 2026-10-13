package redact

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func defaultPolicy() Policy { return NewPolicy(DefaultHeaders, DefaultFields) }

func TestHeadersRedactsSensitiveValues(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer sk_live_123")
	h.Set("Cookie", "session=abc; theme=dark")
	h.Set("X-Api-Key", "key-123")
	h.Set("Content-Type", "application/json")
	h.Set("X-Hub-Signature-256", "sha256=deadbeef")

	out, reds := defaultPolicy().Headers(h)

	if got := out["Authorization"][0]; got != "Bearer [REDACTED]" {
		t.Errorf("Authorization = %q", got)
	}
	if got := out["Cookie"][0]; got != "session=[REDACTED]; theme=[REDACTED]" {
		t.Errorf("Cookie = %q", got)
	}
	if got := out["X-Api-Key"][0]; got != Placeholder {
		t.Errorf("X-Api-Key = %q", got)
	}
	if got := out["Content-Type"][0]; got != "application/json" {
		t.Errorf("Content-Type changed: %q", got)
	}
	if got := out["X-Hub-Signature-256"][0]; got != "sha256=deadbeef" {
		t.Errorf("signature headers are not secret and should be kept: %q", got)
	}
	if len(reds) != 3 {
		t.Fatalf("redactions = %+v", reds)
	}
	for _, r := range reds {
		if r.Location != "header" {
			t.Errorf("unexpected location %q", r.Location)
		}
	}
	if h.Get("Authorization") != "Bearer sk_live_123" {
		t.Error("input header must not be modified")
	}
}

func TestHeaderRedactionIsIdempotent(t *testing.T) {
	h := http.Header{"Authorization": {"Bearer [REDACTED]"}, "Cookie": {"a=[REDACTED]"}}
	out, _ := defaultPolicy().Headers(h)
	if out["Authorization"][0] != "Bearer [REDACTED]" || out["Cookie"][0] != "a=[REDACTED]" {
		t.Fatalf("re-redaction changed values: %v", out)
	}
}

func TestAuthorizationWithoutSchemeIsFullyRedacted(t *testing.T) {
	out, _ := defaultPolicy().Headers(http.Header{"Authorization": {"rawsecretvalue"}})
	if out["Authorization"][0] != Placeholder {
		t.Fatalf("got %q", out["Authorization"][0])
	}
}

func TestQueryRedactionPreservesOrderAndEncoding(t *testing.T) {
	pairs, raw, reds := defaultPolicy().Query("b=2&token=abc%20def&a=hello+world&API-KEY=zzz&empty")
	want := []Pair{{"b", "2"}, {"token", Placeholder}, {"a", "hello world"}, {"API-KEY", Placeholder}, {"empty", ""}}
	if len(pairs) != len(want) {
		t.Fatalf("pairs = %v", pairs)
	}
	for i := range want {
		if pairs[i] != want[i] {
			t.Errorf("pair %d = %v, want %v", i, pairs[i], want[i])
		}
	}
	if raw != "b=2&token=%5BREDACTED%5D&a=hello+world&API-KEY=%5BREDACTED%5D&empty" {
		t.Errorf("raw = %q", raw)
	}
	if len(reds) != 2 || reds[0].Location != "query" {
		t.Errorf("redactions = %+v", reds)
	}
}

func TestJSONBodyRedactionKeepsFormatting(t *testing.T) {
	body := `{
  "event": "payment.succeeded",
  "data": {
    "client_secret": "pi_123_secret_456",
    "amount": 4200,
    "nested": {"apiKey": {"a": [1, 2, {"b": "c"}]}, "keep": true},
    "list": [{"password": "hunter2"}, {"password": null}]
  },
  "Token" : 12.5e3
}`
	out, reds := defaultPolicy().Body("application/json", []byte(body), false)
	got := string(out)

	for _, secret := range []string{"pi_123_secret_456", "hunter2", "12.5e3", `"a": [1, 2`} {
		if strings.Contains(got, secret) {
			t.Errorf("redacted output still contains %q:\n%s", secret, got)
		}
	}
	if !json.Valid(out) {
		t.Fatalf("output is not valid JSON:\n%s", got)
	}
	if !strings.Contains(got, "\"amount\": 4200,\n") || !strings.Contains(got, `"keep": true`) {
		t.Errorf("unrelated content or formatting changed:\n%s", got)
	}
	wantPaths := map[string]bool{
		"$.data.client_secret":    true,
		"$.data.nested.apiKey":    true,
		"$.data.list[0].password": true,
		"$.data.list[1].password": true,
		"$.Token":                 true,
	}
	if len(reds) != len(wantPaths) {
		t.Fatalf("redactions = %+v", reds)
	}
	for _, r := range reds {
		if !wantPaths[r.Name] || r.Location != "body" {
			t.Errorf("unexpected redaction %+v", r)
		}
	}
}

func TestJSONRedactionWithoutContentTypeUsesSniffing(t *testing.T) {
	out, reds := defaultPolicy().Body("", []byte(`[{"secret":"x"}]`), false)
	if string(out) != `[{"secret":"[REDACTED]"}]` || len(reds) != 1 || reds[0].Name != "$[0].secret" {
		t.Fatalf("out=%s reds=%+v", out, reds)
	}
}

func TestJSONPathQuotesUnusualKeys(t *testing.T) {
	p := NewPolicy(nil, []string{"my key"})
	_, reds := p.Body("application/json", []byte(`{"outer key":{"my key":1}}`), false)
	if len(reds) != 1 || reds[0].Name != `$["outer key"]["my key"]` {
		t.Fatalf("reds = %+v", reds)
	}
}

func TestInvalidJSONIsLeftUnchanged(t *testing.T) {
	body := []byte(`{"password": "x", broken`)
	out, reds := defaultPolicy().Body("application/json", body, false)
	if string(out) != string(body) || reds != nil {
		t.Fatalf("invalid JSON should be returned as-is, got %s %+v", out, reds)
	}
}

func TestFormBodyRedaction(t *testing.T) {
	body := []byte("user=alice&password=hunter2&user%5Bapi_key%5D=k&note=a%26b")
	out, reds := defaultPolicy().Body("application/x-www-form-urlencoded; charset=utf-8", body, false)
	want := "user=alice&password=%5BREDACTED%5D&user%5Bapi_key%5D=%5BREDACTED%5D&note=a%26b"
	if string(out) != want {
		t.Fatalf("out = %s\nwant  %s", out, want)
	}
	if len(reds) != 2 || reds[1].Name != "user[api_key]" {
		t.Fatalf("reds = %+v", reds)
	}
}

func TestPlainTextAndBinaryBodiesAreUntouched(t *testing.T) {
	for _, tc := range []struct{ ct, body string }{
		{"text/plain", "password=hunter2"},
		{"application/octet-stream", "\x00\x01password"},
	} {
		out, reds := defaultPolicy().Body(tc.ct, []byte(tc.body), false)
		if string(out) != tc.body || reds != nil {
			t.Errorf("%s body changed: %q", tc.ct, out)
		}
	}
}

func TestEmptyFieldListDisablesBodyRedaction(t *testing.T) {
	p := NewPolicy(DefaultHeaders, nil)
	body := []byte(`{"password":"x"}`)
	out, reds := p.Body("application/json", body, false)
	if string(out) != string(body) || reds != nil {
		t.Fatalf("got %s", out)
	}
}

func TestTruncatedJSONNeverKeepsSecrets(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{
			name: "cut inside sensitive string",
			body: `{"event":"x","password":"hunt`,
			want: `{"event":"x","password":`,
		},
		{
			name: "cut inside sensitive object",
			body: `{"event":"x","client_secret":{"kid":"a","value":"abc`,
			want: `{"event":"x","client_secret":`,
		},
		{
			name: "complete secret before cut is replaced",
			body: `{"token":"abc","items":[1,2,3`,
			want: `{"token":"[REDACTED]","items":[1,2,3`,
		},
		{
			name: "cut inside ordinary value is kept",
			body: `{"secret":"s","note":"hello wor`,
			want: `{"secret":"[REDACTED]","note":"hello wor`,
		},
		{
			name: "malformed before cut is dropped",
			body: `{"a":1,,"password":"x","b":"`,
			want: `{"a":1`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := defaultPolicy().Body("application/json", []byte(tc.body), true)
			if string(out) != tc.want {
				t.Fatalf("got  %s\nwant %s", out, tc.want)
			}
		})
	}
}

func TestMultipartRedaction(t *testing.T) {
	body := "--XyZ\r\n" +
		"Content-Disposition: form-data; name=\"user\"\r\n\r\n" +
		"alice\r\n" +
		"--XyZ\r\n" +
		"Content-Disposition: form-data; name=\"password\"\r\n\r\n" +
		"hunter2\r\n" +
		"--XyZ\r\n" +
		"Content-Disposition: form-data; name=\"file\"; filename=\"a.bin\"\r\n" +
		"Content-Type: application/octet-stream\r\n\r\n" +
		"\x00\x01\x02\r\n" +
		"--XyZ--\r\n"
	want := strings.Replace(body, "hunter2", "[REDACTED]", 1)
	out, reds := defaultPolicy().Body("multipart/form-data; boundary=XyZ", []byte(body), false)
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
	if len(reds) != 1 || reds[0].Name != "password" {
		t.Fatalf("redactions = %+v", reds)
	}

	truncated := "--XyZ\r\nContent-Disposition: form-data; name=\"token\"\r\n\r\nabc12"
	out, _ = defaultPolicy().Body("multipart/form-data; boundary=XyZ", []byte(truncated), true)
	if strings.Contains(string(out), "abc12") {
		t.Fatalf("truncated secret part kept: %q", out)
	}

	out, reds = defaultPolicy().Body("multipart/form-data", []byte(body), false)
	if string(out) != body || reds != nil {
		t.Fatal("missing boundary should leave the body unchanged")
	}
}

func TestNormalizeField(t *testing.T) {
	for in, want := range map[string]string{
		"api_key": "apikey", "apiKey": "apikey", "API-KEY": "apikey", " client.secret ": "clientsecret",
	} {
		if got := NormalizeField(in); got != want {
			t.Errorf("NormalizeField(%q) = %q, want %q", in, got, want)
		}
	}
}
