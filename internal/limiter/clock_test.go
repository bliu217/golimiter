package limiter

import (
	"sync"
	"time"
)

// fakeClock is a deterministic Clock implementation for tests. Its Now() value
// only changes when Advance is called, allowing time-dependent logic (refill
// math, etc.) to be exercised without real sleeps.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
