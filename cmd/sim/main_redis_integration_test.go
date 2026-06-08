//go:build integration

package main

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/bliu217/golimiter/internal/limiter"
	servergrpc "github.com/bliu217/golimiter/internal/server/grpc"
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
		t.Fatalf("redis ping: %v", err)
	}
	return client
}

func TestRun_InProcessRedisServerEndToEnd(t *testing.T) {
	rdb := newIntegrationRedisClient(t)
	deps := limiter.Deps{
		RedisClient:    rdb,
		RedisKeyPrefix: "sim-e2e",
	}
	l, err := servergrpc.NewTokenBucketLimiterForTests(3, 0.000001, deps)
	if err != nil {
		t.Fatalf("new redis limiter: %v", err)
	}
	srv, err := servergrpc.StartTestServer(servergrpc.TestServerConfig{
		Limiter: l,
		Deps:    deps,
	})
	if err != nil {
		t.Fatalf("start test server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
	})

	cfg := simConfig{
		addr:        srv.Addr(),
		requests:    5,
		concurrency: 1,
		key:         "redis-user",
		keys:        1,
		resource:    "api",
		cost:        1,
		timeout:     2 * time.Second,
		reset:       true,
		retries:     0,
		backoff:     time.Millisecond,
		rate:        0,
		outputDir:   t.TempDir(),
	}

	if err := run(context.Background(), cfg, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}

	summary := readSingleSummaryFile(t, cfg.outputDir)
	if summary.Totals.Requests != 5 {
		t.Fatalf("total requests = %d, want 5", summary.Totals.Requests)
	}
	if summary.Totals.Allowed != 3 {
		t.Fatalf("allowed = %d, want 3", summary.Totals.Allowed)
	}
	if summary.Totals.Denied != 2 {
		t.Fatalf("denied = %d, want 2", summary.Totals.Denied)
	}
	if summary.Totals.Errors != 0 {
		t.Fatalf("errors = %d, want 0", summary.Totals.Errors)
	}
}
