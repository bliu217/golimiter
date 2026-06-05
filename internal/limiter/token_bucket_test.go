package limiter

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// epsilon is the tolerance used when comparing accumulated float64 token counts.
const epsilon = 1e-9

func approxEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= epsilon
}

// --- NewTokenBucket -----------------------------------------------------------

func TestNewTokenBucket(t *testing.T) {
	tests := []struct {
		name        string
		capacity    float64
		refillRate  float64
		wantErr     bool
		errContains string
	}{
		{"valid_args", 10, 5, false, ""},
		{"fractional_valid", 1.5, 0.5, false, ""},
		{"zero_capacity", 0, 5, true, "capacity"},
		{"negative_capacity", -1, 5, true, "capacity"},
		{"zero_refill", 10, 0, true, "refill rate"},
		{"negative_refill", 10, -1, true, "refill rate"},
		{"both_invalid_reports_capacity_first", 0, 0, true, "capacity"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tb, err := NewTokenBucket(tc.capacity, tc.refillRate)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				if tb != nil {
					t.Fatalf("expected nil bucket on error, got %+v", tb)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tb == nil {
				t.Fatal("expected non-nil bucket")
			}
			if tb.capacity != tc.capacity {
				t.Errorf("capacity = %v, want %v", tb.capacity, tc.capacity)
			}
			if tb.refillRate != tc.refillRate {
				t.Errorf("refillRate = %v, want %v", tb.refillRate, tc.refillRate)
			}
			if tb.tokens != tc.capacity {
				t.Errorf("initial tokens = %v, want %v (full)", tb.tokens, tc.capacity)
			}
			if tb.lastRefillTime.IsZero() {
				t.Error("lastRefillTime not initialized")
			}
			if tb.clock == nil {
				t.Error("clock not initialized")
			}
		})
	}
}

func TestNewTokenBucket_InitialLastRefillTimeMatchesClock(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	clk := newFakeClock(start)
	tb, err := newTokenBucketWithClock(10, 1, clk)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !tb.lastRefillTime.Equal(start) {
		t.Errorf("lastRefillTime = %v, want %v", tb.lastRefillTime, start)
	}
}

// --- TokenBucket.Allow: validation -------------------------------------------

func TestTokenBucket_Allow_Validation(t *testing.T) {
	tests := []struct {
		name        string
		cost        float64
		wantOK      bool
		wantErr     bool
		errContains string
	}{
		{"zero_cost", 0, false, true, "positive"},
		{"negative_cost", -1, false, true, "positive"},
		{"cost_exceeds_capacity", 11, false, true, "exceeds bucket capacity"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clk := newFakeClock(time.Unix(0, 0))
			tb, err := newTokenBucketWithClock(10, 1, clk)
			if err != nil {
				t.Fatalf("setup: %v", err)
			}
			tokensBefore := tb.tokens
			result, err := tb.Allow(tc.cost)
			if result.Allowed != tc.wantOK {
				t.Errorf("allowed = %v, want %v", result.Allowed, tc.wantOK)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errContains)
			}
			if tb.tokens != tokensBefore {
				t.Errorf("tokens changed after validation error: before=%v after=%v", tokensBefore, tb.tokens)
			}
		})
	}

	t.Run("cost_equals_capacity_on_full_bucket", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, err := newTokenBucketWithClock(10, 1, clk)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		result, err := tb.Allow(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Allowed {
			t.Fatal("expected allow=true when cost==capacity on full bucket")
		}
		if result.Remaining != 0 {
			t.Errorf("remaining = %v, want 0", result.Remaining)
		}
		if tb.tokens != 0 {
			t.Errorf("tokens = %v, want 0", tb.tokens)
		}
	})
}

// --- TokenBucket.Allow: consume semantics (no refill) -------------------------

func TestTokenBucket_Allow_ConsumeSemantics(t *testing.T) {
	t.Run("single_request_decrements_exactly", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, _ := newTokenBucketWithClock(10, 1, clk)
		result, err := tb.Allow(3)
		if err != nil || !result.Allowed {
			t.Fatalf("Allow(3) = %v, %v; want true, nil", result.Allowed, err)
		}
		if result.Remaining != 7 {
			t.Errorf("remaining = %v, want 7", result.Remaining)
		}
		if tb.tokens != 7 {
			t.Errorf("tokens = %v, want 7", tb.tokens)
		}
	})

	t.Run("exhaustion_then_failure_no_negative_balance", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, _ := newTokenBucketWithClock(5, 1, clk)
		for i := 0; i < 5; i++ {
			result, err := tb.Allow(1)
			if err != nil || !result.Allowed {
				t.Fatalf("call %d: Allow(1) = %v, %v", i, result.Allowed, err)
			}
		}
		result, err := tb.Allow(1)
		if err != nil {
			t.Fatalf("expected nil err on insufficient tokens, got %v", err)
		}
		if result.Allowed {
			t.Fatal("expected failure when out of tokens")
		}
		if result.Remaining != 0 {
			t.Errorf("remaining = %v, want 0", result.Remaining)
		}
		if result.ResetTimeSeconds != 1 {
			t.Errorf("resetTimeSeconds = %v, want 1", result.ResetTimeSeconds)
		}
		if tb.tokens != 0 {
			t.Errorf("tokens = %v, want 0 (no negative balance)", tb.tokens)
		}
	})

	t.Run("fractional_cost", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, _ := newTokenBucketWithClock(1.0, 1, clk)
		for i := 0; i < 4; i++ {
			result, err := tb.Allow(0.25)
			if err != nil || !result.Allowed {
				t.Fatalf("call %d: Allow(0.25) = %v, %v", i, result.Allowed, err)
			}
		}
		result, err := tb.Allow(0.25)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if result.Allowed {
			t.Fatal("5th call should fail")
		}
	})

	t.Run("failed_request_does_not_decrement", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, _ := newTokenBucketWithClock(2, 1, clk)
		_, _ = tb.Allow(2)
		if tb.tokens != 0 {
			t.Fatalf("setup: tokens = %v, want 0", tb.tokens)
		}
		result, err := tb.Allow(1)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if result.Allowed {
			t.Fatal("expected failure")
		}
		if tb.tokens != 0 {
			t.Errorf("tokens = %v, want 0 (unchanged after failed call)", tb.tokens)
		}
	})
}

// --- TokenBucket.Allow: refill math via fake clock ----------------------------

func TestTokenBucket_Allow_Refill(t *testing.T) {
	t.Run("one_token_restored_after_one_over_refillRate", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, _ := newTokenBucketWithClock(10, 5, clk) // 5 tokens/sec
		result, err := tb.Allow(10)
		if !result.Allowed || err != nil {
			t.Fatalf("drain: %v %v", result.Allowed, err)
		}
		clk.Advance(200 * time.Millisecond) // exactly 1 token at 5/sec
		result, err = tb.Allow(1)
		if err != nil || !result.Allowed {
			t.Fatalf("Allow(1) after refill: %v %v", result.Allowed, err)
		}
		if !approxEqual(tb.tokens, 0) {
			t.Errorf("tokens = %v, want ~0", tb.tokens)
		}
	})

	t.Run("partial_refill", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, _ := newTokenBucketWithClock(10, 5, clk)
		_, _ = tb.Allow(10)
		clk.Advance(400 * time.Millisecond) // 2 tokens
		result, err := tb.Allow(2)
		if !result.Allowed || err != nil {
			t.Fatalf("Allow(2): %v %v", result.Allowed, err)
		}
		result, err = tb.Allow(0.01)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if result.Allowed {
			t.Fatal("expected failure when only ~0 tokens remain")
		}
	})

	t.Run("refill_capped_at_capacity", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, _ := newTokenBucketWithClock(10, 5, clk)
		_, _ = tb.Allow(10)
		clk.Advance(time.Hour) // would overfill by 18000 tokens uncapped
		result, err := tb.Allow(10)
		if !result.Allowed || err != nil {
			t.Fatalf("Allow(10) after long pause: %v %v", result.Allowed, err)
		}
		if !approxEqual(tb.tokens, 0) {
			t.Errorf("tokens = %v, want ~0 (capacity 10 minus cost 10)", tb.tokens)
		}
		// No further time has advanced; a small additional spend must fail.
		result, err = tb.Allow(0.01)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if result.Allowed {
			t.Fatal("expected failure: refill should have been capped at capacity")
		}
	})

	t.Run("lastRefillTime_updates_when_elapsed_positive", func(t *testing.T) {
		start := time.Unix(0, 0)
		clk := newFakeClock(start)
		tb, _ := newTokenBucketWithClock(10, 5, clk)
		if !tb.lastRefillTime.Equal(start) {
			t.Fatalf("initial lastRefillTime = %v, want %v", tb.lastRefillTime, start)
		}
		clk.Advance(500 * time.Millisecond)
		_, _ = tb.Allow(1)
		want := start.Add(500 * time.Millisecond)
		if !tb.lastRefillTime.Equal(want) {
			t.Errorf("lastRefillTime = %v, want %v", tb.lastRefillTime, want)
		}
	})

	t.Run("elapsed_zero_no_refill", func(t *testing.T) {
		clk := newFakeClock(time.Unix(0, 0))
		tb, _ := newTokenBucketWithClock(10, 5, clk)
		_, _ = tb.Allow(5)
		if tb.tokens != 5 {
			t.Fatalf("setup: tokens = %v, want 5", tb.tokens)
		}
		_, _ = tb.Allow(3)
		if tb.tokens != 2 {
			t.Errorf("tokens = %v, want 2 (no refill expected when clock did not advance)", tb.tokens)
		}
	})

	t.Run("elapsed_negative_no_refill_no_panic", func(t *testing.T) {
		clk := newFakeClock(time.Unix(100, 0))
		tb, _ := newTokenBucketWithClock(10, 5, clk)
		_, _ = tb.Allow(5)
		clk.Advance(-50 * time.Second) // simulated clock skew backwards
		before := tb.tokens
		lastBefore := tb.lastRefillTime
		result, err := tb.Allow(1)
		if err != nil || !result.Allowed {
			t.Fatalf("Allow(1): %v %v", result.Allowed, err)
		}
		if got, want := tb.tokens, before-1; got != want {
			t.Errorf("tokens = %v, want %v (no refill on negative elapsed)", got, want)
		}
		if !tb.lastRefillTime.Equal(lastBefore) {
			t.Errorf("lastRefillTime changed despite negative elapsed: %v -> %v", lastBefore, tb.lastRefillTime)
		}
	})
}

// --- NewInMemoryTokenBucketLimiter -------------------------------------------

func TestNewInMemoryTokenBucketLimiter(t *testing.T) {
	tests := []struct {
		name       string
		capacity   float64
		refillRate float64
		wantErr    bool
	}{
		{"valid", 10, 5, false},
		{"zero_capacity", 0, 5, true},
		{"negative_capacity", -1, 5, true},
		{"zero_refill", 10, 0, true},
		{"negative_refill", 10, -1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := NewInMemoryTokenBucketLimiter(tc.capacity, tc.refillRate)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if l != nil {
					t.Fatalf("expected nil limiter, got %+v", l)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ml, ok := l.(*InMemoryTokenBucketLimiter)
			if !ok {
				t.Fatalf("expected *InMemoryTokenBucketLimiter, got %T", l)
			}
			if ml.buckets == nil {
				t.Error("buckets map not initialized")
			}
			if len(ml.buckets) != 0 {
				t.Errorf("initial bucket count = %d, want 0", len(ml.buckets))
			}
			if ml.capacity != tc.capacity {
				t.Errorf("capacity = %v, want %v", ml.capacity, tc.capacity)
			}
			if ml.refillRate != tc.refillRate {
				t.Errorf("refillRate = %v, want %v", ml.refillRate, tc.refillRate)
			}
			if ml.clock == nil {
				t.Error("clock not initialized")
			}
		})
	}
}

// --- InMemoryTokenBucketLimiter.Allow ----------------------------------------

func newTestLimiter(t *testing.T, capacity, refillRate float64) (*InMemoryTokenBucketLimiter, *fakeClock) {
	t.Helper()
	clk := newFakeClock(time.Unix(0, 0))
	l, err := newInMemoryTokenBucketLimiterWithClock(capacity, refillRate, clk)
	if err != nil {
		t.Fatalf("setup limiter: %v", err)
	}
	return l, clk
}

func TestInMemoryTokenBucketLimiter_Allow(t *testing.T) {
	t.Run("empty_key", func(t *testing.T) {
		l, _ := newTestLimiter(t, 5, 1)
		result, err := l.Allow("", 1)
		if err == nil || !strings.Contains(err.Error(), "key cannot be empty") {
			t.Fatalf("expected key error, got allowed=%v err=%v", result.Allowed, err)
		}
		if result.Allowed {
			t.Error("ok = true, want false")
		}
		if len(l.buckets) != 0 {
			t.Errorf("buckets created for empty key: %d", len(l.buckets))
		}
	})

	t.Run("creates_bucket_on_first_call", func(t *testing.T) {
		l, _ := newTestLimiter(t, 5, 1)
		result, err := l.Allow("a", 1)
		if err != nil || !result.Allowed {
			t.Fatalf("Allow(a,1): %v %v", result.Allowed, err)
		}
		if result.Remaining != 4 {
			t.Errorf("remaining = %v, want 4", result.Remaining)
		}
		if _, ok := l.buckets["a"]; !ok {
			t.Fatal("bucket for 'a' not created")
		}
		if len(l.buckets) != 1 {
			t.Errorf("bucket count = %d, want 1", len(l.buckets))
		}
	})

	t.Run("reuses_existing_bucket", func(t *testing.T) {
		l, _ := newTestLimiter(t, 5, 1)
		_, _ = l.Allow("a", 1)
		first := l.buckets["a"]
		_, _ = l.Allow("a", 1)
		second := l.buckets["a"]
		if first != second {
			t.Error("bucket pointer changed across calls for same key")
		}
		if len(l.buckets) != 1 {
			t.Errorf("bucket count = %d, want 1", len(l.buckets))
		}
	})

	t.Run("keys_are_independent", func(t *testing.T) {
		l, _ := newTestLimiter(t, 5, 1)
		for i := 0; i < 5; i++ {
			result, err := l.Allow("a", 1)
			if err != nil || !result.Allowed {
				t.Fatalf("draining 'a' #%d: %v %v", i, result.Allowed, err)
			}
		}
		result, _ := l.Allow("a", 1)
		if result.Allowed {
			t.Fatal("'a' should be exhausted")
		}
		result, err := l.Allow("b", 1)
		if err != nil || !result.Allowed {
			t.Fatalf("'b' Allow(1): %v %v", result.Allowed, err)
		}
	})

	t.Run("propagates_cost_errors", func(t *testing.T) {
		l, _ := newTestLimiter(t, 5, 1)
		if _, err := l.Allow("a", 0); err == nil || !strings.Contains(err.Error(), "positive") {
			t.Errorf("expected positive-cost error, got %v", err)
		}
		if _, err := l.Allow("a", 1000); err == nil || !strings.Contains(err.Error(), "exceeds bucket capacity") {
			t.Errorf("expected exceeds-capacity error, got %v", err)
		}
	})

	t.Run("per_key_buckets_share_injected_clock", func(t *testing.T) {
		l, clk := newTestLimiter(t, 5, 5)
		_, _ = l.Allow("a", 5) // drain
		result, _ := l.Allow("a", 1)
		if result.Allowed {
			t.Fatal("'a' should be exhausted before clock advance")
		}
		clk.Advance(time.Second)
		result, err := l.Allow("a", 5)
		if err != nil || !result.Allowed {
			t.Fatalf("expected refill via shared clock: %v %v", result.Allowed, err)
		}
	})

	t.Run("reset_clears_existing_buckets", func(t *testing.T) {
		l, _ := newTestLimiter(t, 2, 1)
		_, _ = l.Allow("a", 2)
		result, _ := l.Allow("a", 1)
		if result.Allowed {
			t.Fatal("'a' should be exhausted before reset")
		}
		if err := l.Reset(); err != nil {
			t.Fatalf("reset: %v", err)
		}
		if len(l.buckets) != 0 {
			t.Fatalf("bucket count after reset = %d, want 0", len(l.buckets))
		}
		result, err := l.Allow("a", 2)
		if err != nil || !result.Allowed {
			t.Fatalf("Allow after reset: allowed=%v err=%v", result.Allowed, err)
		}
	})
}

// --- Concurrency tests -------------------------------------------------------

func TestTokenBucket_Allow_Concurrent(t *testing.T) {
	const n = 200
	clk := newFakeClock(time.Unix(0, 0))
	tb, err := newTokenBucketWithClock(float64(n), 1, clk)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	var wg sync.WaitGroup
	var successes int64
	for i := 0; i < n*2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := tb.Allow(1)
			if err != nil {
				t.Errorf("unexpected err: %v", err)
				return
			}
			if result.Allowed {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()
	if successes != int64(n) {
		t.Errorf("successes = %d, want %d (cap)", successes, n)
	}
}

func TestInMemoryLimiter_Allow_Concurrent_SameKey(t *testing.T) {
	const capacity = 100
	clk := newFakeClock(time.Unix(0, 0))
	l, err := newInMemoryTokenBucketLimiterWithClock(capacity, 1, clk)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	var wg sync.WaitGroup
	var successes int64
	for i := 0; i < capacity*3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := l.Allow("shared", 1)
			if err != nil {
				t.Errorf("unexpected err: %v", err)
				return
			}
			if result.Allowed {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()
	if successes != int64(capacity) {
		t.Errorf("successes = %d, want %d", successes, capacity)
	}
	if len(l.buckets) != 1 {
		t.Errorf("bucket count = %d, want 1", len(l.buckets))
	}
}

func TestInMemoryLimiter_Allow_Concurrent_DistinctKeys(t *testing.T) {
	const numKeys = 50
	const callsPerKey = 10
	const capacity = 5
	clk := newFakeClock(time.Unix(0, 0))
	l, err := newInMemoryTokenBucketLimiterWithClock(capacity, 1, clk)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	var wg sync.WaitGroup
	successes := make([]int64, numKeys)
	for k := 0; k < numKeys; k++ {
		for r := 0; r < callsPerKey; r++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				key := fmt.Sprintf("k-%d", k)
				result, err := l.Allow(key, 1)
				if err != nil {
					t.Errorf("unexpected err: %v", err)
					return
				}
				if result.Allowed {
					atomic.AddInt64(&successes[k], 1)
				}
			}(k)
		}
	}
	wg.Wait()
	for k, got := range successes {
		if got != int64(capacity) {
			t.Errorf("key %d successes = %d, want %d", k, got, capacity)
		}
	}
	if len(l.buckets) != numKeys {
		t.Errorf("bucket count = %d, want %d", len(l.buckets), numKeys)
	}
}
