package limiter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCheckouter struct {
	mu     sync.Mutex
	tokens float64
	calls  atomic.Int64
	resets atomic.Int64
	before func()
}

func (f *fakeCheckouter) Checkout(key string, requested float64) (CheckoutResult, error) {
	if f.before != nil {
		f.before()
	}
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	granted := requested
	if granted > f.tokens {
		granted = f.tokens
	}
	f.tokens -= granted
	return CheckoutResult{Granted: granted, Remaining: f.tokens}, nil
}

func (f *fakeCheckouter) Reset() error {
	f.resets.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = 0
	return nil
}

func TestLeasingLimiter_SpendsFromPocketWithoutRecheckout(t *testing.T) {
	inner := &fakeCheckouter{tokens: 100}
	l, err := newLeasingLimiter(inner, 10, 100)
	if err != nil {
		t.Fatalf("new leasing limiter: %v", err)
	}

	for i := 0; i < 10; i++ {
		result, err := l.Allow("user", 1)
		if err != nil || !result.Allowed {
			t.Fatalf("allow %d: allowed=%v err=%v", i, result.Allowed, err)
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("checkout calls = %d, want 1", got)
	}
}

func TestLeasingLimiter_DoesNotOversubscribe(t *testing.T) {
	inner := &fakeCheckouter{tokens: 10}
	l, err := newLeasingLimiter(inner, 10, 10)
	if err != nil {
		t.Fatalf("new leasing limiter: %v", err)
	}

	const workers = 50
	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			result, err := l.Allow("hot", 1)
			if err != nil {
				t.Errorf("allow: %v", err)
				return
			}
			if result.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed = %d, want 10", got)
	}
}

func TestLeasingLimiter_RefillsUntilDemandMet(t *testing.T) {
	inner := &fakeCheckouter{tokens: 1000}
	l, err := newLeasingLimiter(inner, 10, 1000)
	if err != nil {
		t.Fatalf("new leasing limiter: %v", err)
	}

	const workers = 50
	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			result, err := l.Allow("hot", 1)
			if err != nil {
				t.Errorf("allow: %v", err)
				return
			}
			if result.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != workers {
		t.Fatalf("allowed = %d, want %d", got, workers)
	}
}

func TestLeasingLimiter_PartialGrantBelowCostDenies(t *testing.T) {
	inner := &fakeCheckouter{tokens: 0.5}
	l, err := newLeasingLimiter(inner, 10, 10)
	if err != nil {
		t.Fatalf("new leasing limiter: %v", err)
	}
	result, err := l.Allow("user", 1)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if result.Allowed {
		t.Fatal("allowed request with only a fractional token")
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("checkout calls = %d, want 1", got)
	}
}

func TestLeasingLimiter_ResetClearsPocket(t *testing.T) {
	inner := &fakeCheckouter{tokens: 5}
	l, err := newLeasingLimiter(inner, 5, 5)
	if err != nil {
		t.Fatalf("new leasing limiter: %v", err)
	}
	if _, err := l.Allow("user", 1); err != nil {
		t.Fatalf("allow: %v", err)
	}
	if err := l.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if inner.resets.Load() != 1 {
		t.Fatal("inner Reset was not called")
	}
	inner.mu.Lock()
	inner.tokens = 0
	inner.mu.Unlock()

	result, err := l.Allow("user", 1)
	if err != nil {
		t.Fatalf("allow after reset: %v", err)
	}
	if result.Allowed {
		t.Fatal("pocket still had tokens after Reset")
	}
}

func TestLeasingLimiter_SingleflightCoalescesMisses(t *testing.T) {
	var inFlight atomic.Int64
	var maxFlight atomic.Int64
	inner := &fakeCheckouter{tokens: 1000}
	inner.before = func() {
		n := inFlight.Add(1)
		for {
			old := maxFlight.Load()
			if n <= old || maxFlight.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
	}
	l, err := newLeasingLimiter(inner, 10, 1000)
	if err != nil {
		t.Fatalf("new leasing limiter: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(20)
	for i := 0; i < 20; i++ {
		go func() {
			defer wg.Done()
			if _, err := l.Allow("user", 1); err != nil {
				t.Errorf("allow: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := maxFlight.Load(); got != 1 {
		t.Fatalf("concurrent checkouts = %d, want 1", got)
	}
}
