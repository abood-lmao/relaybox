package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sseEvent struct {
	ID, Type, Data string
}

type sseStream struct {
	t    *testing.T
	resp *http.Response
	rd   *bufio.Reader
}

func openStream(t *testing.T, ctx context.Context, srv *httptest.Server, lastEventID string) (*sseStream, *http.Response) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &sseStream{t: t, resp: resp, rd: bufio.NewReader(resp.Body)}, resp
}

// next returns the next event, skipping comments and the retry hint.
func (s *sseStream) next() sseEvent {
	s.t.Helper()
	var ev sseEvent
	for {
		line, err := s.rd.ReadString('\n')
		if err != nil {
			s.t.Fatalf("stream ended: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
			if ev.Type != "" {
				return ev
			}
		case strings.HasPrefix(line, "id: "):
			ev.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			ev.Type = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestEventStream(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(h.h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, resp := openStream(t, ctx, srv, "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("stream response: %d %v", resp.StatusCode, resp.Header)
	}
	if line, _ := s.rd.ReadString('\n'); line != "retry: 3000\n" {
		t.Fatalf("first line = %q", line)
	}

	ep := h.createEndpoint("live", nil)
	if ev := s.next(); ev.Type != EventEndpointsChanged {
		t.Fatalf("event = %+v", ev)
	}
	h.hook(ep, "POST", "/x", `{"password":"never-in-events","n":1}`, "Content-Type", "application/json")
	ev := s.next()
	if ev.Type != "request.created" || ev.ID == "" {
		t.Fatalf("event = %+v", ev)
	}
	if strings.Contains(ev.Data, "never-in-events") || strings.Contains(ev.Data, `"n":1`) {
		t.Errorf("event carries the body: %s", ev.Data)
	}
	var payload struct {
		ID         int64  `json:"id"`
		EndpointID int64  `json:"endpoint_id"`
		Method     string `json:"method"`
	}
	if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil || payload.ID != h.latestRequestID(ep) || payload.EndpointID != ep.ID || payload.Method != "POST" {
		t.Fatalf("payload = %s (%v)", ev.Data, err)
	}

	// Reconnecting with Last-Event-ID delivers what was missed.
	h.hook(ep, "PUT", "", "")
	h.hook(ep, "DELETE", "", "")
	s2, _ := openStream(t, ctx, srv, ev.ID)
	for _, want := range []string{"PUT", "DELETE"} {
		got := s2.next()
		if got.Type != "request.created" || !strings.Contains(got.Data, want) {
			t.Fatalf("backlog event = %+v, want %s", got, want)
		}
	}

	s3, _ := openStream(t, ctx, srv, "unknown-7")
	if got := s3.next(); got.Type != "resync" {
		t.Fatalf("stale ID should resync, got %+v", got)
	}

	// The harness allows three streams.
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	resp4, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("fourth stream status = %d", resp4.StatusCode)
	}
}

func TestEventStreamEndsWhenBrokerCloses(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(h.h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _ := openStream(t, ctx, srv, "")
	_, _ = s.rd.ReadString('\n')
	h.broker.Close()
	done := make(chan error, 1)
	go func() {
		_, err := s.rd.ReadString('\n')
		for err == nil {
			_, err = s.rd.ReadString('\n')
		}
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream stayed open after the broker closed")
	}
}
