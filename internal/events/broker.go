// Package events fans out small notifications to dashboard clients over
// Server-Sent Events.
//
// Events are hints, not storage: the database stays the source of truth. A
// short history lets reconnecting clients catch up using Last-Event-ID; when
// that is not possible (the history moved on, or the server restarted) the
// client is told to resync by refetching from the API.
package events

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Errors returned by Subscribe.
var (
	ErrTooManyClients = errors.New("too many event stream connections")
	ErrClosed         = errors.New("event broker is shut down")
)

// Event is one notification. ID has the form "<epoch>-<seq>", where epoch
// changes on every server start.
type Event struct {
	ID   string
	Seq  uint64
	Type string
	Data json.RawMessage
}

// Subscriber receives events on C. C is closed when the subscriber falls too
// far behind or the broker shuts down; the client should reconnect.
type Subscriber struct {
	C chan Event
}

// Broker distributes events to subscribers.
type Broker struct {
	mu         sync.Mutex
	epoch      string
	seq        uint64
	history    []Event
	historyCap int
	clients    map[*Subscriber]struct{}
	maxClients int
	closed     bool
}

// New returns a broker that accepts up to maxClients concurrent subscribers
// and keeps the last historySize events for reconnecting clients.
func New(maxClients, historySize int) *Broker {
	return &Broker{
		epoch:      strings.ToLower(rand.Text()[:8]),
		historyCap: historySize,
		clients:    make(map[*Subscriber]struct{}),
		maxClients: maxClients,
	}
}

// Publish sends an event to every subscriber without blocking. Subscribers
// whose buffer is full are disconnected so they reconnect and catch up.
func (b *Broker) Publish(eventType string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.seq++
	ev := Event{ID: fmt.Sprintf("%s-%d", b.epoch, b.seq), Seq: b.seq, Type: eventType, Data: data}
	b.history = append(b.history, ev)
	if len(b.history) > b.historyCap {
		b.history = b.history[len(b.history)-b.historyCap:]
	}
	for sub := range b.clients {
		select {
		case sub.C <- ev:
		default:
			delete(b.clients, sub)
			close(sub.C)
		}
	}
}

// Subscribe registers a client. lastEventID is the Last-Event-ID header sent
// by a reconnecting browser. It returns events the client missed, or
// resync=true when missed events cannot be replayed.
func (b *Broker) Subscribe(lastEventID string) (sub *Subscriber, backlog []Event, resync bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, nil, false, ErrClosed
	}
	if len(b.clients) >= b.maxClients {
		return nil, nil, false, ErrTooManyClients
	}
	if lastEventID != "" {
		backlog, resync = b.since(lastEventID)
	}
	sub = &Subscriber{C: make(chan Event, 64)}
	b.clients[sub] = struct{}{}
	return sub, backlog, resync, nil
}

func (b *Broker) since(lastEventID string) ([]Event, bool) {
	epoch, seqStr, ok := strings.Cut(lastEventID, "-")
	seq, err := strconv.ParseUint(seqStr, 10, 64)
	if !ok || err != nil || epoch != b.epoch || seq > b.seq {
		return nil, true
	}
	if seq == b.seq {
		return nil, false
	}
	if len(b.history) == 0 || b.history[0].Seq > seq+1 {
		return nil, true
	}
	var out []Event
	for _, ev := range b.history {
		if ev.Seq > seq {
			out = append(out, ev)
		}
	}
	return out, false
}

// Unsubscribe removes a client. It is safe to call more than once.
func (b *Broker) Unsubscribe(sub *Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.clients[sub]; ok {
		delete(b.clients, sub)
		close(sub.C)
	}
}

// Close disconnects all clients and rejects new subscriptions.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for sub := range b.clients {
		close(sub.C)
	}
	b.clients = map[*Subscriber]struct{}{}
}

// ClientCount returns the number of connected subscribers.
func (b *Broker) ClientCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}
