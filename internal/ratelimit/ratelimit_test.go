package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestBurstThenRefill(t *testing.T) {
	l := New(2, 3, 100)
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d within burst was rejected", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("expected rejection with a short wait, got ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("keys must be limited independently")
	}

	now = now.Add(500 * time.Millisecond) // refills one token at 2/s
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("token should have refilled")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("only one token should have refilled")
	}
}

func TestNilLimiterAllowsEverything(t *testing.T) {
	l := New(0, 10, 10)
	if l != nil {
		t.Fatal("rate 0 should disable limiting")
	}
	for i := 0; i < 1000; i++ {
		if ok, _ := l.Allow("x"); !ok {
			t.Fatal("nil limiter rejected a request")
		}
	}
}

func TestTrackedKeysAreBounded(t *testing.T) {
	l := New(1, 1, 50)
	for i := 0; i < 1000; i++ {
		l.Allow(fmt.Sprintf("10.0.0.%d", i))
	}
	if n := l.Len(); n > 50 {
		t.Fatalf("tracking %d keys, limit is 50", n)
	}
}
