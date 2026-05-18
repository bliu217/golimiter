package limiter

import (
	"strings"
	"testing"
	"time"
)

// limiterFactory builds a Limiter for the conformance suite. The factory is
// expected to thread `clk` through to whatever per-key bucket state it
// maintains so that refill behavior can be driven deterministically.
type limiterFactory func(t *testing.T, capacity, refillRate float64, clk Clock) Limiter

// runLimiterConformance exercises behavior that should hold for any Limiter
// implementation, regardless of storage backend (in-memory map, Redis, etc.).
// New backends (e.g. a future RedisTokenBucketLimiter) should add a Test* that
// calls this with their own factory.
func runLimiterConformance(t *testing.T, name string, factory limiterFactory) {
	t.Helper()

	t.Run(name+"/empty_key_returns_error", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		l := factory(t, 5, 1, clk)
		ok, err := l.Allow("", 1)
		if err == nil {
			t.Fatal("expected error on empty key")
		}
		if ok {
			t.Error("ok = true, want false")
		}
	})

	t.Run(name+"/exhausts_then_fails", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		l := factory(t, 5, 1, clk)
		for i := 0; i < 5; i++ {
			ok, err := l.Allow("k", 1)
			if err != nil || !ok {
				t.Fatalf("call %d: ok=%v err=%v", i, ok, err)
			}
		}
		ok, err := l.Allow("k", 1)
		if err != nil {
			t.Fatalf("err = %v on exhausted bucket, want nil", err)
		}
		if ok {
			t.Fatal("expected exhaustion")
		}
	})

	t.Run(name+"/keys_are_isolated", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		l := factory(t, 2, 1, clk)
		if _, err := l.Allow("a", 2); err != nil {
			t.Fatalf("drain 'a': %v", err)
		}
		ok, err := l.Allow("a", 1)
		if err != nil || ok {
			t.Fatalf("'a' should be exhausted: ok=%v err=%v", ok, err)
		}
		ok, err = l.Allow("b", 2)
		if err != nil || !ok {
			t.Fatalf("'b' should be untouched: ok=%v err=%v", ok, err)
		}
	})

	t.Run(name+"/refill_after_clock_advance", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		l := factory(t, 5, 5, clk) // 5 tokens/sec
		for i := 0; i < 5; i++ {
			if _, err := l.Allow("k", 1); err != nil {
				t.Fatalf("drain call %d: %v", i, err)
			}
		}
		ok, _ := l.Allow("k", 1)
		if ok {
			t.Fatal("expected exhaustion before clock advance")
		}
		clk.Advance(time.Second)
		for i := 0; i < 5; i++ {
			ok, err := l.Allow("k", 1)
			if err != nil || !ok {
				t.Fatalf("post-refill call %d: ok=%v err=%v", i, ok, err)
			}
		}
	})

	t.Run(name+"/cost_validation_propagates", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		l := factory(t, 5, 1, clk)
		if _, err := l.Allow("k", 0); err == nil {
			t.Error("expected error for cost=0")
		}
		if _, err := l.Allow("k", -1); err == nil {
			t.Error("expected error for cost<0")
		}
		_, err := l.Allow("k", 100)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Errorf("expected exceeds-capacity error, got %v", err)
		}
	})
}

func TestInMemoryLimiter_Conformance(t *testing.T) {
	runLimiterConformance(t, "InMemory", func(t *testing.T, capacity, refillRate float64, clk Clock) Limiter {
		t.Helper()
		l, err := newInMemoryTokenBucketLimiterWithClock(capacity, refillRate, clk)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		return l
	})
}
