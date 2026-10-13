package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/fixture"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/version"
)

const defaultExportLimit = 100

func (a *API) exportRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	includeBody := true
	if v := r.URL.Query().Get("include_body"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return badRequest("invalid_parameter", "include_body must be true or false.")
		}
		includeBody = b
	}
	reqs, err := a.db.GetRequests(r.Context(), []int64{id})
	if err != nil {
		return err
	}
	if len(reqs) == 0 {
		return notFound("Request")
	}
	return a.writeFixture(w, reqs, includeBody, fmt.Sprintf("relaybox-request-%d.json", id))
}

type exportInput struct {
	RequestIDs    []int64 `json:"request_ids"`
	EndpointID    int64   `json:"endpoint_id"`
	Limit         int     `json:"limit"`
	IncludeBodies *bool   `json:"include_bodies"`
}

func (a *API) exportFixture(w http.ResponseWriter, r *http.Request) error {
	var in exportInput
	if err := decodeJSON(w, r, &in, maxJSONBody); err != nil {
		return err
	}
	includeBodies := in.IncludeBodies == nil || *in.IncludeBodies
	var ids []int64
	switch {
	case len(in.RequestIDs) > 0:
		if len(in.RequestIDs) > fixture.MaxRequests {
			return badRequest("invalid_input", "Export at most %d requests at a time.", fixture.MaxRequests)
		}
		ids = in.RequestIDs
	case in.EndpointID > 0:
		limit := in.Limit
		if limit == 0 {
			limit = defaultExportLimit
		}
		if limit < 1 || limit > fixture.MaxRequests {
			return badRequest("invalid_input", "limit must be between 1 and %d.", fixture.MaxRequests)
		}
		if _, err := a.db.GetEndpoint(r.Context(), in.EndpointID); errors.Is(err, storage.ErrNotFound) {
			return notFound("Endpoint")
		} else if err != nil {
			return err
		}
		var err error
		if ids, err = a.db.LatestRequestIDs(r.Context(), in.EndpointID, limit); err != nil {
			return err
		}
	default:
		return badRequest("invalid_input", "Provide request_ids or an endpoint_id.")
	}
	reqs, err := a.db.GetRequests(r.Context(), ids)
	if err != nil {
		return err
	}
	if len(reqs) == 0 {
		return notFound("Requests")
	}
	name := fmt.Sprintf("relaybox-fixture-%s.json", a.now().UTC().Format("20060102-150405"))
	return a.writeFixture(w, reqs, includeBodies, name)
}

func (a *API) writeFixture(w http.ResponseWriter, reqs []storage.CapturedRequest, includeBodies bool, filename string) error {
	f := fixture.Build(reqs, a.settings.Current().Policy, fixture.BuildOptions{
		IncludeBodies: includeBodies,
		Generator:     "relaybox " + version.Version,
		Now:           a.now(),
	})
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(data, '\n'))
	return nil
}

// importFixture validates a fixture and records its requests in the chosen
// endpoint, as if they had been received there. Nothing is sent anywhere.
func (a *API) importFixture(w http.ResponseWriter, r *http.Request) error {
	endpointID, err := strconv.ParseInt(r.URL.Query().Get("endpoint_id"), 10, 64)
	if err != nil || endpointID <= 0 {
		return badRequest("invalid_parameter", "Choose the endpoint to import into (endpoint_id).")
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return &Error{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type",
			Message: "Send the fixture file as JSON with Content-Type: application/json."}
	}
	ep, err := a.db.GetEndpoint(r.Context(), endpointID)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound("Endpoint")
	}
	if err != nil {
		return err
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, fixture.MaxBytes))
	if err != nil {
		return err
	}
	f, err := fixture.Parse(data, a.settings.Ceiling())
	var verr *fixture.ValidationError
	if errors.As(err, &verr) {
		return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_fixture",
			Message: "The fixture file is invalid.", Details: verr.Problems}
	}
	if err != nil {
		return err
	}

	ins := make([]capture.Inbound, 0, len(f.Requests))
	for _, req := range f.Requests {
		body, _ := req.Body.Decode() // validated by Parse
		in := capture.Inbound{
			Method:   req.Method,
			Path:     req.Path,
			RawQuery: req.Query,
			Header:   req.Header(),
			Body:     body,
			Source:   storage.SourceImport,
		}
		if req.Body != nil {
			in.BodyTruncated = req.Body.Truncated
		}
		ins = append(ins, in)
	}
	created, err := a.capture.IngestBatch(r.Context(), ep, ins)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(created))
	for _, c := range created {
		ids = append(ids, c.ID)
	}
	a.log.Info("fixture imported", "endpoint_id", ep.ID, "requests", len(ids))
	writeJSON(w, http.StatusOK, map[string]any{"imported": len(ids), "endpoint_id": ep.ID, "request_ids": ids})
	return nil
}
