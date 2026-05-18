package limiter

import "time"

// Clock abstracts the system clock so time-sensitive logic (like the
// token-bucket refill math) can be exercised deterministically from tests.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
