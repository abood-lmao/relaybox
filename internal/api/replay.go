package api

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/abood-lmao/relaybox/internal/replay"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/validate"
)

// HeaderOptionView describes how a captured header is treated in a replay.
type HeaderOptionView struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Selected bool   `json:"selected"`
	Redacted bool   `json:"redacted"`
	Blocked  string `json:"blocked,omitempty"`
}

// replayDraft returns what a replay of the request would send by default,
// for the replay form.
func (a *API) replayDraft(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	req, err := a.db.GetRequest(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		return notFound("Request")
	}
	if err != nil {
		return err
	}
	opts := replay.HeaderOptions(req)
	headers := make([]HeaderOptionView, 0, len(opts))
	for _, o := range opts {
		headers = append(headers, HeaderOptionView{Name: o.Name, Value: o.Value, Selected: o.Selected, Redacted: o.Redacted, Blocked: o.Blocked})
	}
	allowed := a.cfg.ReplayAllowedHosts
	if allowed == nil {
		allowed = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"request":            a.detailSummary(req),
		"method":             req.Method,
		"headers":            headers,
		"body":               makeBodyView(req.Body, req.ContentType, req.BodySize, req.BodyTruncated),
		"url_replay_enabled": a.replay.URLEnabled(),
		"allowed_hosts":      allowed,
		"max_body_bytes":     replay.MaxBodyBytes,
	})
	return nil
}

type replayBody struct {
	Encoding string `json:"encoding"`
	Data     string `json:"data"`
}

type replayInput struct {
	Target     string       `json:"target"`
	URL        string       `json:"url"`
	EndpointID int64        `json:"endpoint_id"`
	Method     string       `json:"method"`
	Headers    *[][2]string `json:"headers"`
	Body       *replayBody  `json:"body"`
	Confirmed  bool         `json:"confirmed"`
}

func (a *API) sendReplay(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in replayInput
	if err := decodeJSON(w, r, &in, maxReplayBody); err != nil {
		return err
	}
	input := replay.Input{
		Target: in.Target, URL: in.URL, EndpointID: in.EndpointID, Method: in.Method, Confirmed: in.Confirmed,
	}
	if in.Headers != nil {
		input.Headers = *in.Headers
		if input.Headers == nil {
			input.Headers = [][2]string{}
		}
	}
	if in.Body != nil {
		switch in.Body.Encoding {
		case "", "utf8":
			input.Body = []byte(in.Body.Data)
		case "base64":
			b, err := base64.StdEncoding.DecodeString(in.Body.Data)
			if err != nil {
				return validate.Errors{"body": "The body is not valid base64."}
			}
			input.Body = b
		default:
			return validate.Errors{"body": "Body encoding must be utf8 or base64."}
		}
		if input.Body == nil {
			input.Body = []byte{}
		}
	}

	out, err := a.replay.Replay(r.Context(), id, input)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound("Request")
	case errors.Is(err, replay.ErrConfirmationRequired):
		return &Error{Status: http.StatusConflict, Code: "confirmation_required",
			Message: "Confirm the destination before sending a replay to it."}
	case errors.Is(err, replay.ErrURLReplayDisabled):
		return &Error{Status: http.StatusForbidden, Code: "replay_disabled",
			Message: "Replay to URLs is disabled while the dashboard is remotely accessible. Set RELAYBOX_ALLOW_REMOTE_REPLAY=true to enable it."}
	case errors.Is(err, replay.ErrTruncatedBody):
		return &Error{Status: http.StatusConflict, Code: "body_truncated",
			Message: "The stored body was truncated at capture, so replaying it would send incomplete data. Provide a body to send instead."}
	case errors.Is(err, replay.ErrEndpointDisabled):
		return &Error{Status: http.StatusConflict, Code: "endpoint_disabled",
			Message: "The target endpoint is disabled. Enable it first."}
	case errors.Is(err, replay.ErrBusy):
		return &Error{Status: http.StatusTooManyRequests, Code: "busy",
			Message: "Several replays are already running. Try again in a moment."}
	case err != nil:
		return err
	}

	resp := map[string]any{"attempt": attemptView(out.Attempt), "response": nil, "created_request_id": nil}
	if out.Response != nil {
		res := out.Response
		resp["response"] = ResponseView{
			Status:     res.Status,
			StatusText: http.StatusText(res.Status),
			Headers:    sortedPairs(res.Header),
			Body:       makeBodyView(res.Body, res.Header.Get("Content-Type"), res.Size, res.BodyTruncated),
		}
	}
	if out.Created != nil {
		resp["created_request_id"] = out.Created.ID
		er := out.EndpointResponse
		resp["response"] = ResponseView{
			Status:     er.Status,
			StatusText: http.StatusText(er.Status),
			Headers:    sortedPairs(er.Header),
			Body:       makeBodyView(er.Body, er.Header.Get("Content-Type"), int64(len(er.Body)), false),
		}
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (a *API) listReplays(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if _, err := a.db.GetRequest(r.Context(), id); errors.Is(err, storage.ErrNotFound) {
		return notFound("Request")
	} else if err != nil {
		return err
	}
	attempts, err := a.replay.History(r.Context(), id)
	if err != nil {
		return err
	}
	items := make([]ReplayAttemptView, 0, len(attempts))
	for _, at := range attempts {
		items = append(items, attemptView(at))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}
