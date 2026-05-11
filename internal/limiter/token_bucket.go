package limiter

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type TokenBucket struct {
	capacity     float64
	tokens       float64
	refillRate   float64
	lastRefillTime time.Time
	mu sync.Mutex
}

type InMemoryTokenBucketLimiter struct {
	buckets map[string]*TokenBucket
	capacity     float64
	refillRate   float64
	mu sync.Mutex
}

func (l *InMemoryTokenBucketLimiter) Allow(key string, cost float64) (bool, error) {
	if key == "" {
		return false, errors.New("key cannot be empty")
	}
	l.mu.Lock()
	bucket, exists := l.buckets[key]
	if !exists {
		var err error
		bucket, err = NewTokenBucket(l.capacity, l.refillRate)
		if err != nil {
			l.mu.Unlock()
			return false, fmt.Errorf("failed to create token bucket: %w", err)
		}
		l.buckets[key] = bucket
	}

	l.mu.Unlock()
	return bucket.Allow(cost)
}

func NewInMemoryTokenBucketLimiter(capacity, refillRate float64) (Limiter, error) {
	if capacity <= 0 {
		return nil, errors.New("capacity must be greater than 0")
	}
	if refillRate <= 0 {
		return nil, errors.New("refill rate must be greater than 0")
	}

	return &InMemoryTokenBucketLimiter{
		buckets: make(map[string]*TokenBucket),
		capacity: capacity,
		refillRate: refillRate,
	}, nil
}

func (tb *TokenBucket) Allow(cost float64) (bool, error) {
	if cost <= 0 {
		return false, errors.New("cost must be positive")
	}
	if cost > tb.capacity {
		return false, errors.New("cost exceeds bucket capacity")
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(tb.lastRefillTime)
	if elapsed > 0 {
		refillTokens := elapsed.Seconds() * tb.refillRate
		tb.tokens = min(tb.capacity, tb.tokens+refillTokens)
		tb.lastRefillTime = now
	}
	if tb.tokens < cost {
		return false, nil
	}
	tb.tokens -= cost
	return true, nil
}

func NewTokenBucket(capacity, refillRate float64) (*TokenBucket, error) {
	if capacity <= 0 {
		return nil, errors.New("capacity must be greater than 0")
	}
	if refillRate <= 0 {
		return nil, errors.New("refill rate must be greater than 0")
	}
	return &TokenBucket{
		capacity:     capacity,
		tokens:       capacity,
		refillRate:  refillRate,
		lastRefillTime: time.Now(),
	}, nil
}