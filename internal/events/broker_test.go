package events

import (
	"fmt"
	"testing"
)

func TestPublishReachesSubscribers(t *testing.T) {
	b := New(4, 16)
	sub, backlog, resync, err := b.Subscribe("")
	if err != nil || backlog != nil || resync {
		t.Fatalf("Subscribe: %v %v %v", backlog, resync, err)
	}
	b.Publish("request.created", map[string]int{"id": 7})
	ev := <-sub.C
	if ev.Type != "request.created" || string(ev.Data) != `{"id":7}` || ev.Seq != 1 {
		t.Fatalf("unexpected event %+v", ev)
	}
}

func TestReconnectReplaysMissedEvents(t *testing.T) {
	b := New(4, 16)
	for i := 0; i < 5; i++ {
		b.Publish("x", i)
	}
	last := fmt.Sprintf("%s-%d", b.epoch, 3)
	_, backlog, resync, err := b.Subscribe(last)
	if err != nil || resync {
		t.Fatalf("Subscribe: resync=%v err=%v", resync, err)
	}
	if len(backlog) != 2 || backlog[0].Seq != 4 || backlog[1].Seq != 5 {
		t.Fatalf("backlog = %+v", backlog)
	}
}

func TestReconnectRequestsResyncWhenHistoryIsGone(t *testing.T) {
	b := New(4, 3)
	for i := 0; i < 10; i++ {
		b.Publish("x", i)
	}
	cases := []string{
		fmt.Sprintf("%s-%d", b.epoch, 2), // evicted from history
		"oldepoch-5",                     // server restarted
		"garbage",
		fmt.Sprintf("%s-%d", b.epoch, 99), // from the future
	}
	for _, id := range cases {
		_, backlog, resync, _ := b.Subscribe(id)
		if !resync || backlog != nil {
			t.Errorf("Last-Event-ID %q: resync=%v backlog=%v", id, resync, backlog)
		}
	}
}

func TestUpToDateClientNeedsNothing(t *testing.T) {
	b := New(4, 3)
	b.Publish("x", 1)
	_, backlog, resync, _ := b.Subscribe(fmt.Sprintf("%s-%d", b.epoch, 1))
	if resync || len(backlog) != 0 {
		t.Fatalf("resync=%v backlog=%v", resync, backlog)
	}
}

func TestClientLimit(t *testing.T) {
	b := New(2, 4)
	s1, _, _, _ := b.Subscribe("")
	if _, _, _, err := b.Subscribe(""); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := b.Subscribe(""); err != ErrTooManyClients {
		t.Fatalf("third subscriber: %v", err)
	}
	b.Unsubscribe(s1)
	b.Unsubscribe(s1) // idempotent
	if _, _, _, err := b.Subscribe(""); err != nil {
		t.Fatalf("slot should be free again: %v", err)
	}
}

func TestSlowSubscriberIsDisconnected(t *testing.T) {
	b := New(2, 4)
	sub, _, _, _ := b.Subscribe("")
	for i := 0; i < 100; i++ {
		b.Publish("x", i)
	}
	count := 0
	for range sub.C {
		count++
	}
	if count != cap(sub.C) || b.ClientCount() != 0 {
		t.Fatalf("received %d events, %d clients remain", count, b.ClientCount())
	}
}

func TestCloseDisconnectsEveryone(t *testing.T) {
	b := New(2, 4)
	sub, _, _, _ := b.Subscribe("")
	b.Close()
	if _, ok := <-sub.C; ok {
		t.Fatal("channel should be closed")
	}
	if _, _, _, err := b.Subscribe(""); err != ErrClosed {
		t.Fatalf("Subscribe after Close: %v", err)
	}
	b.Publish("x", 1) // must not panic
}
