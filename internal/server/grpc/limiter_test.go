package grpc

import (
	"context"
	"sync"
	"testing"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"github.com/bliu217/golimiter/internal/limiter"
)

func newTestRateLimiterServer(t *testing.T, capacity, refillRate float64) *RateLimiterServer {
	t.Helper()
	l, err := limiter.NewInMemoryTokenBucketLimiter(capacity, refillRate)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	return NewRateLimiterServer(l, limiter.Deps{})
}

func TestRateLimiterServer_AllowPopulatesMetadata(t *testing.T) {
	s := newTestRateLimiterServer(t, 2, 1)
	ctx := context.Background()
	req := &pb.AllowRequest{Key: "user-a", Resource: "api", Cost: 1}

	resp, err := s.Allow(ctx, req)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if !resp.Allowed {
		t.Fatal("first request should be allowed")
	}
	if resp.Remaining != 1 {
		t.Fatalf("remaining = %d, want 1", resp.Remaining)
	}
	if resp.ResetTime != 0 {
		t.Fatalf("reset_time = %d, want 0", resp.ResetTime)
	}

	if _, err := s.Allow(ctx, req); err != nil {
		t.Fatalf("second allow: %v", err)
	}
	resp, err = s.Allow(ctx, req)
	if err != nil {
		t.Fatalf("third allow: %v", err)
	}
	if resp.Allowed {
		t.Fatal("third request should be denied")
	}
	if resp.Reason != "rate limit exceeded" {
		t.Fatalf("reason = %q, want rate limit exceeded", resp.Reason)
	}
	if resp.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", resp.Remaining)
	}
	if resp.ResetTime != 1 {
		t.Fatalf("reset_time = %d, want 1", resp.ResetTime)
	}
}

func TestRateLimiterServer_ResetClearsTrafficState(t *testing.T) {
	s := newTestRateLimiterServer(t, 2, 1)
	ctx := context.Background()
	req := &pb.AllowRequest{Key: "user-a", Resource: "api", Cost: 2}

	if _, err := s.Allow(ctx, req); err != nil {
		t.Fatalf("drain: %v", err)
	}
	denied, err := s.Allow(ctx, &pb.AllowRequest{Key: "user-a", Resource: "api", Cost: 1})
	if err != nil {
		t.Fatalf("deny check: %v", err)
	}
	if denied.Allowed {
		t.Fatal("request should be denied before reset")
	}

	reset, err := s.Reset(ctx, &pb.ResetRequest{})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !reset.Success {
		t.Fatal("reset success = false, want true")
	}

	resp, err := s.Allow(ctx, req)
	if err != nil {
		t.Fatalf("allow after reset: %v", err)
	}
	if !resp.Allowed {
		t.Fatal("request should be allowed after reset")
	}
}

func TestRateLimiterServer_ConcurrentAllowConfigureReset(t *testing.T) {
	s := newTestRateLimiterServer(t, 10, 1)
	ctx := context.Background()
	config := &pb.ConfigureRequest{
		Algorithm: pb.Algorithm_TOKEN_BUCKET,
		Config: &pb.ConfigureRequest_TokenBucket{
			TokenBucket: &pb.TokenBucketConfig{
				Capacity:   10,
				RefillRate: 1,
			},
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, _ = s.Allow(ctx, &pb.AllowRequest{Key: "user-a", Resource: "api", Cost: 1})
		}()
		go func() {
			defer wg.Done()
			_, _ = s.Configure(ctx, config)
		}()
		go func() {
			defer wg.Done()
			_, _ = s.Reset(ctx, &pb.ResetRequest{})
		}()
	}
	wg.Wait()
}
