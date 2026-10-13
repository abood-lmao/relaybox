package replay

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Network limits for replay requests.
const (
	dialTimeout           = 5 * time.Second
	DefaultTimeout        = 30 * time.Second
	MaxResponseBodyBytes  = 1 << 20
	maxResponseHeaderSize = 64 << 10
)

// Request is an outbound replay request.
type Request struct {
	URL    *url.URL
	Method string
	Header http.Header
	Body   []byte
}

// Result describes what happened. Err is set when no HTTP response was
// received.
type Result struct {
	Status        int
	Header        http.Header
	Body          []byte
	BodyTruncated bool
	Size          int64
	Duration      time.Duration
	Err           error
}

// Sender performs replay requests through a Guard.
type Sender struct {
	client  *http.Client
	timeout time.Duration
	agent   string
}

// NewSender returns a sender. Proxies from the environment are ignored on
// purpose: the guard must see the real destination address. Redirects are
// returned to the caller rather than followed, and TLS certificates are
// always verified.
func NewSender(g *Guard, userAgent string, timeout time.Duration) *Sender {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            g.DialContext,
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  timeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: maxResponseHeaderSize,
		DisableKeepAlives:      true,
	}
	return &Sender{
		client: &http.Client{
			Transport: tr,
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		timeout: timeout,
		agent:   userAgent,
	}
}

// Send performs the request and reads at most MaxResponseBodyBytes of the
// response body.
func (s *Sender) Send(ctx context.Context, r Request) Result {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL.String(), bytes.NewReader(r.Body))
	if err != nil {
		return Result{Err: err, Duration: time.Since(start)}
	}
	req.Header = r.Header.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", s.agent)
	}
	req.ContentLength = int64(len(r.Body))
	if len(r.Body) == 0 {
		req.Body = http.NoBody
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return Result{Err: err, Duration: time.Since(start)}
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes+1))
	res := Result{Status: resp.StatusCode, Header: resp.Header, Duration: time.Since(start)}
	if len(body) > MaxResponseBodyBytes {
		body = body[:MaxResponseBodyBytes]
		res.BodyTruncated = true
	}
	if readErr != nil {
		res.BodyTruncated = true
	}
	res.Body = body
	res.Size = int64(len(body))
	if resp.ContentLength > res.Size {
		res.Size = resp.ContentLength
	}
	return res
}

// Summary describes a result in one line for the replay history.
func (s *Sender) Summary(res Result) string {
	if res.Err == nil {
		text := http.StatusText(res.Status)
		if text == "" {
			text = "status"
		}
		return fmt.Sprintf("%d %s in %s", res.Status, text, formatDuration(res.Duration))
	}
	return DescribeError(res.Err, s.timeout)
}

// DescribeError turns a transport error into a short message for the user.
func DescribeError(err error, timeout time.Duration) string {
	var (
		blocked   *BlockedError
		dnsErr    *net.DNSError
		unknownCA x509.UnknownAuthorityError
		hostErr   x509.HostnameError
		certErr   x509.CertificateInvalidError
		recordErr tls.RecordHeaderError
	)
	switch {
	case errors.As(err, &blocked):
		return "Blocked: " + blocked.Reason
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("Could not resolve %s.", dnsErr.Name)
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return fmt.Sprintf("Timed out (limit %s).", timeout)
	case errors.Is(err, context.Canceled):
		return "Cancelled."
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "actively refused"):
		return "Connection refused."
	case errors.As(err, &unknownCA):
		return "TLS certificate is not signed by a trusted authority."
	case errors.As(err, &hostErr):
		return "TLS certificate does not match the host name."
	case errors.As(err, &certErr):
		return "TLS certificate is invalid: " + certErr.Error()
	case errors.As(err, &recordErr):
		return "The server did not respond with TLS. Check whether the URL should use http://."
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return "Request failed: " + msg
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}
