//go:build integration

package limiter

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func newIntegrationRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7.2-alpine")
	if err != nil {
		t.Skipf("skipping integration test, redis container unavailable: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	addr, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("redis connection string: %v", err)
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		_ = client.Close()
	})
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping failed: %v", err)
	}

	return client
}

func TestRedisLimiter_Conformance(t *testing.T) {
	ctx := context.Background()
	rdb := newIntegrationRedisClient(t)
	const prefix = "conformance"

	runLimiterConformance(t, "Redis", func(t *testing.T, capacity, refillRate float64, clk Clock) Limiter {
		t.Helper()
		if err := rdb.FlushDB(ctx).Err(); err != nil {
			t.Fatalf("flush redis db: %v", err)
		}
		l, err := newRedisTokenBucketLimiterWithClock(rdb, capacity, refillRate, prefix, clk)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		return l
	})
}

func TestRedisLimiter_SetsTTLAfterAllow(t *testing.T) {
	ctx := context.Background()
	rdb := newIntegrationRedisClient(t)
	const (
		prefix = "ttl"
		key    = "user-a"
	)

	l, err := newRedisTokenBucketLimiterWithClock(rdb, 10, 5, prefix, nil)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	if _, err := l.Allow(key, 1); err != nil {
		t.Fatalf("allow: %v", err)
	}

	ttl, err := rdb.PTTL(ctx, fmt.Sprintf("%s:tb:{%s}", prefix, key)).Result()
	if err != nil {
		t.Fatalf("pttl: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("pttl = %v, want > 0", ttl)
	}
}

func TestRedisLimiter_ScriptSurvivesScriptFlush(t *testing.T) {
	ctx := context.Background()
	rdb := newIntegrationRedisClient(t)

	l, err := newRedisTokenBucketLimiterWithClock(rdb, 5, 1, "noscript", nil)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	if _, err := l.Allow("k", 1); err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if err := rdb.ScriptFlush(ctx).Err(); err != nil {
		t.Fatalf("script flush: %v", err)
	}
	if _, err := l.Allow("k", 1); err != nil {
		t.Fatalf("second allow after script flush: %v", err)
	}
}

func TestRedisLimiter_ConcurrentRequestsRespectCapacity(t *testing.T) {
	ctx := context.Background()
	rdb := newIntegrationRedisClient(t)
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush redis db: %v", err)
	}

	const (
		capacity   = 50.0
		refillRate = 0.01
		callers    = 200
	)

	clk := newFakeClock(time.Unix(0, 0))
	l, err := newRedisTokenBucketLimiterWithClock(rdb, capacity, refillRate, "concurrent", clk)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			ok, err := l.Allow("shared", 1)
			if err != nil {
				t.Errorf("allow failed: %v", err)
				return
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != int64(capacity) {
		t.Fatalf("allowed = %d, want %d", got, int64(capacity))
	}
}
