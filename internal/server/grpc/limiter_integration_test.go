package grpc

import (
	"context"
	"testing"
	"time"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"github.com/bliu217/golimiter/internal/limiter"
)

func newRPCClient(t *testing.T, capacity, refillRate float64) (pb.RateLimiterClient, func()) {
	t.Helper()

	l, err := NewTokenBucketLimiterForTests(capacity, refillRate, limiter.Deps{})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	running, err := StartTestServer(TestServerConfig{
		Limiter: l,
		Deps:    limiter.Deps{},
	})
	if err != nil {
		t.Fatalf("start test server: %v", err)
	}

	conn, err := DialTestServer(context.Background(), running.Addr())
	if err != nil {
		_ = running.Close()
		t.Fatalf("dial test server: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		_ = running.Close()
	}
	return pb.NewRateLimiterClient(conn), cleanup
}

func TestRateLimiterServerRPC_AllowAndReset(t *testing.T) {
	client, cleanup := newRPCClient(t, 2, 0.000001)
	t.Cleanup(cleanup)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := &pb.AllowRequest{Key: "user-a", Resource: "api", Cost: 1}

	first, err := client.Allow(ctx, req)
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if !first.Allowed {
		t.Fatal("first allow should be allowed")
	}

	second, err := client.Allow(ctx, req)
	if err != nil {
		t.Fatalf("second allow: %v", err)
	}
	if !second.Allowed {
		t.Fatal("second allow should be allowed")
	}

	third, err := client.Allow(ctx, req)
	if err != nil {
		t.Fatalf("third allow: %v", err)
	}
	if third.Allowed {
		t.Fatal("third allow should be denied")
	}

	reset, err := client.Reset(ctx, &pb.ResetRequest{})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !reset.Success {
		t.Fatal("reset success should be true")
	}

	afterReset, err := client.Allow(ctx, req)
	if err != nil {
		t.Fatalf("allow after reset: %v", err)
	}
	if !afterReset.Allowed {
		t.Fatal("allow after reset should be allowed")
	}
}

func TestRateLimiterServerRPC_ConfigureSwapsLimiter(t *testing.T) {
	client, cleanup := newRPCClient(t, 1, 0.000001)
	t.Cleanup(cleanup)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := &pb.AllowRequest{Key: "user-a", Resource: "api", Cost: 1}
	if _, err := client.Allow(ctx, req); err != nil {
		t.Fatalf("initial allow: %v", err)
	}
	deniedBeforeConfigure, err := client.Allow(ctx, req)
	if err != nil {
		t.Fatalf("denied before configure: %v", err)
	}
	if deniedBeforeConfigure.Allowed {
		t.Fatal("second request should be denied before configure")
	}

	configureResp, err := client.Configure(ctx, &pb.ConfigureRequest{
		Algorithm: pb.Algorithm_TOKEN_BUCKET,
		Config: &pb.ConfigureRequest_TokenBucket{
			TokenBucket: &pb.TokenBucketConfig{
				Capacity:   3,
				RefillRate: 0.000001,
			},
		},
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if !configureResp.Success {
		t.Fatalf("configure success = false, message = %q", configureResp.Message)
	}

	for i := 1; i <= 3; i++ {
		resp, err := client.Allow(ctx, req)
		if err != nil {
			t.Fatalf("allow %d after configure: %v", i, err)
		}
		if !resp.Allowed {
			t.Fatalf("allow %d after configure should be allowed", i)
		}
	}
	deniedAfterConfigure, err := client.Allow(ctx, req)
	if err != nil {
		t.Fatalf("deny after configure: %v", err)
	}
	if deniedAfterConfigure.Allowed {
		t.Fatal("fourth request after configure should be denied")
	}
}
