// Package api implements the REST API under /api/v1 and the health checks.
// Access control (local-only mode, authentication, cross-origin protection)
// is applied by the server package before requests reach these handlers.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abood-lmao/relaybox/internal/auth"
	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/config"
	"github.com/abood-lmao/relaybox/internal/events"
	"github.com/abood-lmao/relaybox/internal/ratelimit"
	"github.com/abood-lmao/relaybox/internal/replay"
	"github.com/abood-lmao/relaybox/internal/retention"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/validate"
)

// Request body limits for API calls.
const (
	maxJSONBody   = 1 << 20
	maxReplayBody = replay.MaxBodyBytes*2 + 1<<20 // base64 overhead and headers
)

// Event types published by the API.
const (
	EventEndpointsChanged = "endpoints.changed"
	EventSettingsUpdated  = "settings.updated"
)

// Deps are the services the API uses.
type Deps struct {
	Config    config.Config
	DB        *storage.DB
	Settings  *settings.Service
	Capture   *capture.Service
	Replay    *replay.Service
	Retention *retention.Runner
	Events    *events.Broker
	Auth      *auth.Authenticator
	Logger    *slog.Logger
}

// API holds the handlers.
type API struct {
	cfg       config.Config
	db        *storage.DB
	settings  *settings.Service
	capture   *capture.Service
	replay    *replay.Service
	retention *retention.Runner
	events    *events.Broker
	auth      *auth.Authenticator
	log       *slog.Logger
	login     *ratelimit.Limiter
	now       func() time.Time
	routes    []string
}

// New returns the API.
func New(d Deps) *API {
	log := d.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &API{
		cfg: d.Config, db: d.DB, settings: d.Settings, capture: d.Capture, replay: d.Replay,
		retention: d.Retention, events: d.Events, auth: d.Auth, log: log,
		login: ratelimit.New(10.0/60, 10, 10000),
		now:   time.Now,
	}
}

// Handler returns a handler for /api/ paths and the health checks. Plain
// text 404 and 405 responses produced by the router are rewritten as JSON
// errors, so every API response has the same shape.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	a.routes = nil
	a.register(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(&jsonErrorWriter{ResponseWriter: w}, r)
	})
}

func (a *API) register(mux *http.ServeMux) {
	a.handle(mux, "GET /healthz", a.healthz)
	a.handle(mux, "GET /readyz", a.readyz)

	a.handle(mux, "GET /api/v1/meta", a.getMeta)
	a.handle(mux, "GET /api/v1/stats", a.getStats)

	a.handle(mux, "GET /api/v1/endpoints", a.listEndpoints)
	a.handle(mux, "POST /api/v1/endpoints", a.createEndpoint)
	a.handle(mux, "GET /api/v1/endpoints/{id}", a.getEndpoint)
	a.handle(mux, "PATCH /api/v1/endpoints/{id}", a.updateEndpoint)
	a.handle(mux, "DELETE /api/v1/endpoints/{id}", a.deleteEndpoint)
	a.handle(mux, "POST /api/v1/endpoints/{id}/rotate-url", a.rotateEndpointURL)
	a.handle(mux, "POST /api/v1/mock-preview", a.previewMock)

	a.handle(mux, "GET /api/v1/requests", a.listRequests)
	a.handle(mux, "DELETE /api/v1/requests", a.bulkDeleteRequests)
	a.handle(mux, "POST /api/v1/requests/compare", a.compareRequests)
	a.handle(mux, "GET /api/v1/requests/{id}", a.getRequest)
	a.handle(mux, "DELETE /api/v1/requests/{id}", a.deleteRequest)
	a.handle(mux, "GET /api/v1/requests/{id}/body", a.downloadBody)
	a.handle(mux, "GET /api/v1/requests/{id}/replay", a.replayDraft)
	a.handle(mux, "POST /api/v1/requests/{id}/replay", a.sendReplay)
	a.handle(mux, "GET /api/v1/requests/{id}/replays", a.listReplays)
	a.handle(mux, "GET /api/v1/requests/{id}/export", a.exportRequest)

	a.handle(mux, "POST /api/v1/fixtures/export", a.exportFixture)
	a.handle(mux, "POST /api/v1/fixtures/import", a.importFixture)

	a.handle(mux, "GET /api/v1/settings", a.getSettings)
	a.handle(mux, "PATCH /api/v1/settings", a.updateSettings)
	a.handle(mux, "POST /api/v1/maintenance/cleanup", a.runCleanup)

	a.handle(mux, "GET /api/v1/events", a.streamEvents)
	a.handle(mux, "GET /api/v1/openapi.yaml", a.openAPI)

	a.handle(mux, "GET /api/v1/auth/session", a.session)
	a.handle(mux, "POST /api/v1/auth/login", a.loginHandler)
	a.handle(mux, "POST /api/v1/auth/logout", a.logout)

	// The catch-all matches every method, so it has to work out for itself
	// whether the path exists with another method.
	mux.Handle("/api/", a.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var allowed []string
		for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			probe := &http.Request{Method: m, URL: r.URL, Host: r.Host, Header: http.Header{}}
			if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/api/" {
				allowed = append(allowed, m)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			return &Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed",
				Message: "Use " + strings.Join(allowed, ", ") + " for this path."}
		}
		return &Error{Status: http.StatusNotFound, Code: "not_found", Message: "No API route matches this path."}
	}))
}

// handlerFunc returns an error instead of writing error responses itself.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (a *API) handle(mux *http.ServeMux, pattern string, h handlerFunc) {
	a.routes = append(a.routes, pattern)
	mux.Handle(pattern, a.wrap(h))
}

// jsonErrorWriter replaces the router's plain text 404 and 405 responses
// with JSON errors.
type jsonErrorWriter struct {
	http.ResponseWriter
	swallow bool
}

func (j *jsonErrorWriter) WriteHeader(code int) {
	if (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) &&
		strings.HasPrefix(j.Header().Get("Content-Type"), "text/plain") {
		j.swallow = true
		detail := errorDetail{Code: "not_found", Message: "No API route matches this path."}
		if code == http.StatusMethodNotAllowed {
			detail = errorDetail{Code: "method_not_allowed", Message: "Use " + j.Header().Get("Allow") + " for this path."}
		}
		writeJSON(j.ResponseWriter, code, errorBody{detail})
		return
	}
	j.ResponseWriter.WriteHeader(code)
}

func (j *jsonErrorWriter) Write(b []byte) (int, error) {
	if j.swallow {
		return len(b), nil
	}
	return j.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer (for
// flushing event streams).
func (j *jsonErrorWriter) Unwrap() http.ResponseWriter { return j.ResponseWriter }

func (a *API) wrap(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			a.writeError(w, r, err)
		}
	})
}

// Error is an API error with a stable code.
type Error struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
	Details []string
}

func (e *Error) Error() string { return e.Message }

func badRequest(code, format string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Message: fmt.Sprintf(format, args...)}
}

func notFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: what + " not found."}
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Details []string          `json:"details,omitempty"`
}

func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		apiErr *Error
		verr   validate.Errors
		mbe    *http.MaxBytesError
	)
	switch {
	case errors.As(err, &apiErr):
	case errors.As(err, &verr):
		apiErr = &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed",
			Message: "Some fields are invalid.", Fields: verr}
	case errors.Is(err, storage.ErrNotFound):
		apiErr = notFound("Resource")
	case errors.As(err, &mbe):
		apiErr = &Error{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large",
			Message: fmt.Sprintf("The request body is larger than %d bytes.", mbe.Limit)}
	default:
		a.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		apiErr = &Error{Status: http.StatusInternalServerError, Code: "internal_error",
			Message: "Something went wrong. Check the server log for details."}
	}
	writeJSON(w, apiErr.Status, errorBody{errorDetail{Code: apiErr.Code, Message: apiErr.Message, Fields: apiErr.Fields, Details: apiErr.Details}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a JSON request body into dst. It requires a JSON content
// type, rejects unknown fields, and enforces a size limit.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return &Error{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type",
			Message: "Send the request body as JSON with Content-Type: application/json."}
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return describeJSONError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return badRequest("invalid_json", "The request body must contain a single JSON object.")
	}
	return nil
}

func describeJSONError(err error) error {
	var (
		syn *json.SyntaxError
		typ *json.UnmarshalTypeError
		mbe *http.MaxBytesError
	)
	switch {
	case errors.As(err, &mbe):
		return err
	case errors.As(err, &syn):
		return badRequest("invalid_json", "The request body is not valid JSON (error at byte %d).", syn.Offset)
	case errors.As(err, &typ):
		return badRequest("invalid_json", "Field %q must be of type %s.", typ.Field, typ.Type)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return badRequest("invalid_json", "The request body is empty or incomplete.")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return badRequest("invalid_json", "Unknown field %s.", strings.TrimPrefix(err.Error(), "json: unknown field "))
	}
	return badRequest("invalid_json", "The request body could not be read.")
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("invalid_id", "The ID in the URL must be a positive integer.")
	}
	return id, nil
}

func (a *API) publish(eventType string, payload any) {
	if a.events != nil {
		a.events.Publish(eventType, payload)
	}
}

func (a *API) healthz(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	return nil
}

func (a *API) readyz(w http.ResponseWriter, r *http.Request) error {
	if err := a.db.Ping(r.Context()); err != nil {
		a.log.Warn("readiness check failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "database": "unreachable"})
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "database": "ok"})
	return nil
}
