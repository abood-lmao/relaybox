package fixture

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abood-lmao/relaybox/internal/redact"
	"github.com/abood-lmao/relaybox/internal/storage"
)

func sample() storage.CapturedRequest {
	return storage.CapturedRequest{
		ID:               7,
		EndpointName:     "GitHub",
		EndpointPublicID: "abcdefghijklmnopqrstuvwxyz",
		Method:           "POST",
		Path:             "/events",
		RawQuery:         "a=1&api_key=xyz",
		Headers: map[string][]string{
			"Content-Type":  {"application/json"},
			"X-Secret-Sig":  {"sig"},
			"Host":          {"tunnel.example.com"},
			"Authorization": {"Bearer [REDACTED]"},
		},
		Body:        []byte(`{"user":"a","client_secret":"shh"}`),
		ContentType: "application/json",
		RemoteAddr:  "203.0.113.9",
		ReceivedAt:  time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Redactions:  []redact.Redaction{{Location: "header", Name: "Authorization"}},
	}
}

func TestBuildSanitizes(t *testing.T) {
	policy := redact.NewPolicy(append(redact.DefaultHeaders, "X-Secret-Sig"), redact.DefaultFields)
	f := Build([]storage.CapturedRequest{sample()}, policy, BuildOptions{IncludeBodies: true, Now: time.Now()})
	raw, _ := json.Marshal(f)
	s := string(raw)
	for _, leaked := range []string{"xyz", "shh", `"sig"`, "tunnel.example.com", "abcdefghijklmnopqrstuvwxyz", "203.0.113.9"} {
		if strings.Contains(s, leaked) {
			t.Errorf("export contains %q", leaked)
		}
	}
	r := f.Requests[0]
	if r.Query != "a=1&api_key=%5BREDACTED%5D" || r.Body.Encoding != EncodingUTF8 {
		t.Errorf("request %+v", r)
	}
	if f.Format != Format || f.Version != Version || f.Notice == "" {
		t.Errorf("header %+v", f)
	}
	if len(r.Redactions) < 3 {
		t.Errorf("redactions = %+v", r.Redactions)
	}
}

func TestBuildCanOmitBodies(t *testing.T) {
	f := Build([]storage.CapturedRequest{sample()}, redact.NewPolicy(nil, nil), BuildOptions{IncludeBodies: false})
	if f.Requests[0].Body != nil || !f.Requests[0].BodyOmitted {
		t.Fatalf("%+v", f.Requests[0])
	}
}

func TestRoundTripIncludingBinary(t *testing.T) {
	req := sample()
	req.Body = []byte{0, 1, 2, 0xff}
	req.ContentType = "application/octet-stream"
	f := Build([]storage.CapturedRequest{req}, redact.NewPolicy(nil, nil), BuildOptions{IncludeBodies: true, Now: time.Now()})
	raw, _ := json.Marshal(f)
	parsed, err := Parse(raw, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parsed.Requests[0].Body.Decode()
	if err != nil || string(b) != string(req.Body) || parsed.Requests[0].Body.Encoding != EncodingBase64 {
		t.Fatalf("body %q (%v)", b, err)
	}
	if parsed.Requests[0].Header().Get("Content-Type") != "application/json" {
		t.Fatal("headers lost")
	}
}

func TestParseRejectsMalformedFixtures(t *testing.T) {
	valid := `{"format":"relaybox.fixture","version":1,"exported_at":"2026-10-01T00:00:00Z","requests":[{"method":"POST","path":"/","headers":[]}]}`
	if _, err := Parse([]byte(valid), 1024); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	cases := map[string]string{
		"not json":       `{"format":`,
		"empty":          ``,
		"wrong format":   `{"format":"har","version":1,"requests":[]}`,
		"newer version":  `{"format":"relaybox.fixture","version":2,"requests":[]}`,
		"zero version":   `{"format":"relaybox.fixture","version":0,"requests":[]}`,
		"no requests":    `{"format":"relaybox.fixture","version":1,"requests":[]}`,
		"unknown field":  `{"format":"relaybox.fixture","version":1,"requests":[],"script":"x"}`,
		"wrong type":     `{"format":"relaybox.fixture","version":"1","requests":[]}`,
		"bad method":     `{"format":"relaybox.fixture","version":1,"requests":[{"method":"TRACE","path":"/","headers":[]}]}`,
		"relative path":  `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"../etc","headers":[]}]}`,
		"path with ?":    `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/a?b","headers":[]}]}`,
		"header crlf":    `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[["X-A","1\r\nSet-Cookie: x"]]}]}`,
		"header name":    `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[["Bad Name","1"]]}]}`,
		"bad base64":     `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[],"body":{"encoding":"base64","data":"!!!"}}]}`,
		"bad encoding":   `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[],"body":{"encoding":"gzip","data":""}}]}`,
		"body too large": `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[],"body":{"encoding":"utf8","data":"` + strings.Repeat("a", 2000) + `"}}]}`,
		"trailing data":  valid + `{}`,
		"header tuple":   `{"format":"relaybox.fixture","version":1,"requests":[{"method":"GET","path":"/","headers":[["a","b","c"]]}]}`,
	}
	for name, input := range cases {
		_, err := Parse([]byte(input), 1024)
		var verr *ValidationError
		if !errors.As(err, &verr) || len(verr.Problems) == 0 {
			t.Errorf("%s: expected a validation error, got %v", name, err)
		}
	}
}

func TestParseEnforcesFileSize(t *testing.T) {
	big := make([]byte, MaxBytes+1)
	if _, err := Parse(big, 1024); err == nil || !strings.Contains(err.Error(), "MiB") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseLimitsProblemCount(t *testing.T) {
	var reqs []string
	for i := 0; i < 100; i++ {
		reqs = append(reqs, `{"method":"BAD","path":"x","headers":[]}`)
	}
	input := `{"format":"relaybox.fixture","version":1,"requests":[` + strings.Join(reqs, ",") + `]}`
	_, err := Parse([]byte(input), 1024)
	var verr *ValidationError
	if !errors.As(err, &verr) || len(verr.Problems) != maxProblems {
		t.Fatalf("problems = %d", len(verr.Problems))
	}
}
