package server

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"github.com/abood-lmao/relaybox/internal/capture"
)

// dashboardCSP allows only the dashboard's own files. Captured payloads are
// rendered as text, never as markup, so nothing else is needed.
const dashboardCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// proxyHeaders mark requests relayed by a proxy or tunnel. In local mode
// the dashboard refuses them, because the real client may be remote.
var proxyHeaders = []string{
	"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto",
	"X-Real-Ip", "X-Client-Ip", "True-Client-Ip", "Cf-Connecting-Ip", "Fastly-Client-Ip", "Fly-Client-Ip",
}

// Handler returns the root handler. Capture endpoints are dispatched first
// and are public by design. Everything else is the dashboard and its API,
// which are limited to this machine unless remote access is configured.
func (a *App) Handler() http.Handler {
	apiHandler := a.api.Handler()
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deny(w, r, http.StatusForbidden, "cross_origin_rejected",
			"Cross-origin requests are not accepted. Use the dashboard, or send the request without browser origin headers.")
	}))

	dashboard := http.NewServeMux()
	dashboard.Handle("/api/", cop.Handler(a.requireAuth(apiHandler)))
	dashboard.Handle("/", a.ui)

	var guarded http.Handler = dashboardHeaders(dashboard)
	if !a.cfg.AllowRemoteDashboard {
		guarded = localOnly(guarded)
	}

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasPrefix(p, capture.PathPrefix):
			a.capture.ServeHTTP(w, r)
		case p == "/healthz" || p == "/readyz":
			w.Header().Set("X-Content-Type-Options", "nosniff")
			apiHandler.ServeHTTP(w, r)
		default:
			guarded.ServeHTTP(w, r)
		}
	})
	return observe(a.log, root)
}

// requireAuth checks the session cookie or bearer token when a dashboard
// token is configured. The auth routes stay open so the login page works.
func (a *App) requireAuth(next http.Handler) http.Handler {
	if !a.auth.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/") || a.auth.Authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="relaybox"`)
		deny(w, r, http.StatusUnauthorized, "unauthorized", "Log in with the dashboard token.")
	})
}

// localOnly accepts loopback clients that address the server by a loopback
// name and did not come through a proxy. The Host check stops DNS
// rebinding: a malicious page cannot reach the API under its own domain.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if msg := localProblem(r); msg != "" {
			deny(w, r, http.StatusForbidden, "local_only", msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func localProblem(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.Unmap().IsLoopback() {
		return "The dashboard only accepts connections from this machine. To allow remote access, set RELAYBOX_ALLOW_REMOTE_DASHBOARD=true and RELAYBOX_DASHBOARD_TOKEN."
	}
	if !isLoopbackName(r.Host) {
		return "Open the dashboard at localhost, 127.0.0.1, or [::1]. Other host names are refused to block DNS rebinding."
	}
	for _, h := range proxyHeaders {
		if _, ok := r.Header[h]; ok {
			return "The dashboard does not accept requests relayed by a proxy or tunnel in local mode. To allow remote access, set RELAYBOX_ALLOW_REMOTE_DASHBOARD=true and RELAYBOX_DASHBOARD_TOKEN."
		}
	}
	return ""
}

func isLoopbackName(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Zone() == "" && addr.Unmap().IsLoopback()
}

func dashboardHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", dashboardCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		next.ServeHTTP(w, r)
	})
}

// deny writes a JSON error for API paths and plain text elsewhere.
func deny(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}

// statusWriter records the response status for logging and recovery.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// observe logs each request and turns panics into 500 responses. Query
// strings are never logged, and endpoint identifiers are shortened because
// they work like passwords.
func observe(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("panic while handling request", "method", r.Method, "path", maskPath(r.URL.Path),
					"panic", v, "stack", string(debug.Stack()))
				if sw.status == 0 {
					deny(sw, r, http.StatusInternalServerError, "internal_error", "Something went wrong. Check the server log for details.")
				}
			}
			level := slog.LevelDebug
			if sw.status >= 500 {
				level = slog.LevelWarn
			}
			log.Log(r.Context(), level, "http request", "method", r.Method, "path", maskPath(r.URL.Path),
				"status", sw.status, "bytes", sw.bytes, "duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(sw, r)
	})
}

func maskPath(p string) string {
	rest, ok := strings.CutPrefix(p, capture.PathPrefix)
	if !ok {
		return p
	}
	id, sub, _ := strings.Cut(rest, "/")
	if len(id) > 4 {
		id = id[:4] + "..."
	}
	if sub != "" {
		return capture.PathPrefix + id + "/" + sub
	}
	return capture.PathPrefix + id
}
