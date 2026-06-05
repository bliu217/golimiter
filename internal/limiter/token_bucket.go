package limiter

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

type TokenBucket struct {
	capacity       float64
	tokens         float64
	refillRate     float64
	lastRefillTime time.Time
	clock          Clock
	mu             sync.Mutex
}

type InMemoryTokenBucketLimiter struct {
	buckets    map[string]*TokenBucket
	capacity   float64
	refillRate float64
	clock      Clock
	mu         sync.Mutex
}

func (l *InMemoryTokenBucketLimiter) Allow(key string, cost float64) (AllowResult, error) {
	if key == "" {
		return AllowResult{}, errors.New("key cannot be empty")
	}
	l.mu.Lock()
	bucket, exists := l.buckets[key]
	if !exists {
		var err error
		bucket, err = newTokenBucketWithClock(l.capacity, l.refillRate, l.clock)
		if err != nil {
			l.mu.Unlock()
			return AllowResult{}, fmt.Errorf("failed to create token bucket: %w", err)
		}
		l.buckets[key] = bucket
	}

	l.mu.Unlock()
	return bucket.Allow(cost)
}

func (l *InMemoryTokenBucketLimiter) Reset() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buckets = make(map[string]*TokenBucket)
	return nil
}

func NewInMemoryTokenBucketLimiter(capacity, refillRate float64) (Limiter, error) {
	l, err := newInMemoryTokenBucketLimiterWithClock(capacity, refillRate, realClock{})
	if err != nil {
		return nil, err
	}
	return l, nil
}

func newInMemoryTokenBucketLimiterWithClock(capacity, refillRate float64, clock Clock) (*InMemoryTokenBucketLimiter, error) {
	if capacity <= 0 {
		return nil, errors.New("capacity must be greater than 0")
	}
	if refillRate <= 0 {
		return nil, errors.New("refill rate must be greater than 0")
	}
	if clock == nil {
		clock = realClock{}
	}

	return &InMemoryTokenBucketLimiter{
		buckets:    make(map[string]*TokenBucket),
		capacity:   capacity,
		refillRate: refillRate,
		clock:      clock,
	}, nil
}

func (tb *TokenBucket) Allow(cost float64) (AllowResult, error) {
	if cost <= 0 {
		return AllowResult{}, errors.New("cost must be positive")
	}
	if cost > tb.capacity {
		return AllowResult{}, errors.New("cost exceeds bucket capacity")
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()
	now := tb.clock.Now()
	elapsed := now.Sub(tb.lastRefillTime)
	if elapsed > 0 {
		refillTokens := elapsed.Seconds() * tb.refillRate
		tb.tokens = min(tb.capacity, tb.tokens+refillTokens)
		tb.lastRefillTime = now
	}
	if tb.tokens < cost {
		return AllowResult{
			Allowed:          false,
			Remaining:        remainingTokens(tb.tokens),
			ResetTimeSeconds: resetTimeSeconds(cost, tb.tokens, tb.refillRate),
		}, nil
	}
	tb.tokens -= cost
	return AllowResult{
		Allowed:          true,
		Remaining:        remainingTokens(tb.tokens),
		ResetTimeSeconds: 0,
	}, nil
}

func NewTokenBucket(capacity, refillRate float64) (*TokenBucket, error) {
	return newTokenBucketWithClock(capacity, refillRate, realClock{})
}

func newTokenBucketWithClock(capacity, refillRate float64, clock Clock) (*TokenBucket, error) {
	if capacity <= 0 {
		return nil, errors.New("capacity must be greater than 0")
	}
	if refillRate <= 0 {
		return nil, errors.New("refill rate must be greater than 0")
	}
	if clock == nil {
		clock = realClock{}
	}
	return &TokenBucket{
		capacity:       capacity,
		tokens:         capacity,
		refillRate:     refillRate,
		lastRefillTime: clock.Now(),
		clock:          clock,
	}, nil
}

func remainingTokens(tokens float64) int32 {
	if tokens <= 0 {
		return 0
	}
	return int32(math.Floor(tokens))
}

func resetTimeSeconds(cost, tokens, refillRate float64) int64 {
	if tokens >= cost || refillRate <= 0 {
		return 0
	}
	return int64(math.Ceil((cost - tokens) / refillRate))
}
