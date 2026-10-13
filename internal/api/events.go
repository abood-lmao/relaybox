package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/abood-lmao/relaybox/internal/events"
)

const heartbeatInterval = 25 * time.Second

// streamEvents sends live notifications as Server-Sent Events. Messages
// only carry IDs and small metadata; clients fetch details from the API.
func (a *API) streamEvents(w http.ResponseWriter, r *http.Request) error {
	sub, backlog, resync, err := a.events.Subscribe(r.Header.Get("Last-Event-ID"))
	switch {
	case errors.Is(err, events.ErrTooManyClients):
		return &Error{Status: http.StatusServiceUnavailable, Code: "too_many_streams",
			Message: "Too many live update connections are open. Close other dashboard tabs."}
	case err != nil:
		return &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Live updates are unavailable."}
	}
	defer a.events.Unsubscribe(sub)

	// The stream outlives the server's read and write timeouts. An expired
	// read deadline would also cancel the request context.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if _, err := fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return nil
	}
	if resync {
		if _, err := fmt.Fprint(w, "event: resync\ndata: {}\n\n"); err != nil {
			return nil
		}
	}
	for _, ev := range backlog {
		if writeEvent(w, ev) != nil {
			return nil
		}
	}
	if rc.Flush() != nil {
		return nil
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case ev, ok := <-sub.C:
			if !ok {
				return nil // the broker closed the stream; the browser reconnects
			}
			if writeEvent(w, ev) != nil {
				return nil
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return nil
			}
		}
		if rc.Flush() != nil {
			return nil
		}
	}
}

func writeEvent(w http.ResponseWriter, ev events.Event) error {
	_, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, ev.Data)
	return err
}
