package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/abood-lmao/relaybox/internal/config"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/validate"
)

const maxEndpointNameLen = 80

func (a *API) listEndpoints(w http.ResponseWriter, r *http.Request) error {
	eps, err := a.db.ListEndpoints(r.Context())
	if err != nil {
		return err
	}
	items := make([]EndpointView, 0, len(eps))
	for _, e := range eps {
		items = append(items, a.endpointView(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (a *API) getEndpoint(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	e, err := a.db.GetEndpoint(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound("Endpoint")
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, a.endpointView(e))
	return nil
}

type createEndpointInput struct {
	Name         string           `json:"name"`
	Enabled      *bool            `json:"enabled"`
	MockResponse *mockresp.Config `json:"mock_response"`
	MaxBodyBytes *int64           `json:"max_body_bytes"`
}

func (a *API) createEndpoint(w http.ResponseWriter, r *http.Request) error {
	var in createEndpointInput
	if err := decodeJSON(w, r, &in, maxJSONBody); err != nil {
		return err
	}
	errs := validate.Errors{}
	name := validateName(in.Name, errs)
	mock := mockresp.Default()
	if in.MockResponse != nil {
		mock = a.validateMock(*in.MockResponse, errs)
	}
	a.validateBodyLimit(in.MaxBodyBytes, errs)
	if err := errs.Err(); err != nil {
		return err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	e, err := a.db.CreateEndpoint(r.Context(), storage.NewEndpoint{
		Name: name, Enabled: enabled, Mock: mock, MaxBodyBytes: in.MaxBodyBytes,
	})
	if errors.Is(err, storage.ErrDuplicateName) {
		return validate.Errors{"name": "Another endpoint already uses this name."}
	}
	if err != nil {
		return err
	}
	a.log.Info("endpoint created", "endpoint_id", e.ID)
	a.publish(EventEndpointsChanged, map[string]any{"id": e.ID, "action": "created"})
	writeJSON(w, http.StatusCreated, a.endpointView(e))
	return nil
}

type updateEndpointInput struct {
	Name         *string          `json:"name"`
	Enabled      *bool            `json:"enabled"`
	MockResponse *mockresp.Config `json:"mock_response"`
	// MaxBodyBytes distinguishes "absent" (no change) from null (use the
	// default limit), so it is decoded by hand.
	MaxBodyBytes json.RawMessage `json:"max_body_bytes"`
}

func (a *API) updateEndpoint(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in updateEndpointInput
	if err := decodeJSON(w, r, &in, maxJSONBody); err != nil {
		return err
	}
	errs := validate.Errors{}
	var u storage.EndpointUpdate
	if in.Name != nil {
		name := validateName(*in.Name, errs)
		u.Name = &name
	}
	u.Enabled = in.Enabled
	if in.MockResponse != nil {
		mock := a.validateMock(*in.MockResponse, errs)
		u.Mock = &mock
	}
	if len(in.MaxBodyBytes) > 0 {
		u.SetMaxBodyBytes = true
		if string(in.MaxBodyBytes) != "null" {
			var v int64
			if err := json.Unmarshal(in.MaxBodyBytes, &v); err != nil {
				errs.Add("max_body_bytes", "Must be a whole number of bytes or null.")
			} else {
				u.MaxBodyBytes = &v
				a.validateBodyLimit(&v, errs)
			}
		}
	}
	if err := errs.Err(); err != nil {
		return err
	}
	e, err := a.db.UpdateEndpoint(r.Context(), id, u)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound("Endpoint")
	case errors.Is(err, storage.ErrDuplicateName):
		return validate.Errors{"name": "Another endpoint already uses this name."}
	case err != nil:
		return err
	}
	a.publish(EventEndpointsChanged, map[string]any{"id": e.ID, "action": "updated"})
	writeJSON(w, http.StatusOK, a.endpointView(e))
	return nil
}

func (a *API) deleteEndpoint(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	n, err := a.db.DeleteEndpoint(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound("Endpoint")
	}
	if err != nil {
		return err
	}
	a.log.Info("endpoint deleted", "endpoint_id", id, "deleted_requests", n)
	a.publish(EventEndpointsChanged, map[string]any{"id": id, "action": "deleted"})
	if n > 0 {
		a.publish("requests.deleted", map[string]any{"count": n, "reason": "endpoint_deleted"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted_requests": n})
	return nil
}

func (a *API) rotateEndpointURL(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	e, err := a.db.RotateEndpointPublicID(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound("Endpoint")
	}
	if err != nil {
		return err
	}
	a.log.Info("endpoint URL rotated", "endpoint_id", id)
	a.publish(EventEndpointsChanged, map[string]any{"id": id, "action": "updated"})
	writeJSON(w, http.StatusOK, a.endpointView(e))
	return nil
}

// previewMock validates a mock response and returns exactly what a sender
// would receive, without saving anything.
func (a *API) previewMock(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MockResponse mockresp.Config `json:"mock_response"`
		Method       string          `json:"method"`
	}
	if err := decodeJSON(w, r, &in, maxJSONBody); err != nil {
		return err
	}
	errs := validate.Errors{}
	mock := a.validateMock(in.MockResponse, errs)
	method := strings.ToUpper(in.Method)
	if method == "" {
		method = http.MethodPost
	}
	if !slices.Contains(mockresp.AllowedMethods, method) {
		errs.Add("method", "Use one of "+strings.Join(mockresp.AllowedMethods, ", ")+".")
	}
	if err := errs.Err(); err != nil {
		return err
	}
	resp := mock.Build(method)
	writeJSON(w, http.StatusOK, ResponseView{
		Status:     resp.Status,
		StatusText: http.StatusText(resp.Status),
		Headers:    sortedPairs(resp.Header),
		Body:       makeBodyView(resp.Body, resp.Header.Get("Content-Type"), int64(len(resp.Body)), false),
	})
	return nil
}

func validateName(raw string, errs validate.Errors) string {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		errs.Add("name", "Enter a name.")
	case utf8.RuneCountInString(name) > maxEndpointNameLen:
		errs.Add("name", fmt.Sprintf("Use at most %d characters.", maxEndpointNameLen))
	case !utf8.ValidString(name) || !validate.IsHeaderValue(name) || strings.ContainsRune(name, '\t'):
		errs.Add("name", "The name contains control characters.")
	}
	return name
}

func (a *API) validateMock(c mockresp.Config, errs validate.Errors) mockresp.Config {
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
	if err := c.Validate(); err != nil {
		var verr validate.Errors
		if errors.As(err, &verr) {
			for k, v := range verr {
				errs.Add("mock_response."+k, v)
			}
		}
		return c
	}
	return c.Normalize()
}

func (a *API) validateBodyLimit(v *int64, errs validate.Errors) {
	if v == nil {
		return
	}
	if *v < config.MinBodyBytes || *v > a.settings.Ceiling() {
		errs.Add("max_body_bytes", fmt.Sprintf("Must be between %d and %d bytes (RELAYBOX_MAX_BODY_BYTES).", config.MinBodyBytes, a.settings.Ceiling()))
	}
}
