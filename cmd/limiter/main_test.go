package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type pingRedisClient struct {
	failures int
	calls    int
}

func (c *pingRedisClient) Ping(ctx context.Context) *redis.StatusCmd {
	c.calls++
	cmd := redis.NewStatusCmd(ctx)
	if c.calls <= c.failures {
		cmd.SetErr(errors.New("connection refused"))
		return cmd
	}
	cmd.SetVal("PONG")
	return cmd
}

func TestPingRedisRetriesThenSucceeds(t *testing.T) {
	t.Parallel()
	client := &pingRedisClient{failures: 2}
	err := pingRedis(context.Background(), client, "redis:6379", 5, time.Millisecond)
	if err != nil {
		t.Fatalf("pingRedis() = %v, want nil", err)
	}
	if client.calls != 3 {
		t.Fatalf("Ping calls = %d, want 3", client.calls)
	}
}

func TestPingRedisExhaustsAttempts(t *testing.T) {
	t.Parallel()
	client := &pingRedisClient{failures: 10}
	err := pingRedis(context.Background(), client, "redis:6379", 3, time.Millisecond)
	if err == nil {
		t.Fatal("pingRedis() = nil, want error")
	}
	if client.calls != 3 {
		t.Fatalf("Ping calls = %d, want 3", client.calls)
	}
}
