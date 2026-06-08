package grpc

import (
	"context"
	"errors"
	"fmt"
	"net"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"github.com/bliu217/golimiter/internal/limiter"
	grpcpkg "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestServerConfig defines dependencies for starting an in-process gRPC server.
type TestServerConfig struct {
	Limiter limiter.Limiter
	Deps    limiter.Deps
}

// RunningTestServer is a started in-process gRPC server.
type RunningTestServer struct {
	addr   string
	server *grpcpkg.Server
	lis    net.Listener
}

// NewTokenBucketLimiterForTests builds either an in-memory or Redis-backed limiter.
func NewTokenBucketLimiterForTests(capacity, refillRate float64, deps limiter.Deps) (limiter.Limiter, error) {
	if deps.RedisClient != nil {
		return limiter.NewRedisTokenBucketLimiter(
			deps.RedisClient,
			capacity,
			refillRate,
			deps.RedisKeyPrefix,
		)
	}
	return limiter.NewInMemoryTokenBucketLimiter(capacity, refillRate)
}

// StartTestServer starts a real gRPC server bound to an ephemeral localhost port.
func StartTestServer(cfg TestServerConfig) (*RunningTestServer, error) {
	if cfg.Limiter == nil {
		return nil, errors.New("limiter cannot be nil")
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen test grpc server: %w", err)
	}

	s := grpcpkg.NewServer()
	pb.RegisterRateLimiterServer(s, NewRateLimiterServer(cfg.Limiter, cfg.Deps))

	go func() {
		_ = s.Serve(lis)
	}()

	return &RunningTestServer{
		addr:   lis.Addr().String(),
		server: s,
		lis:    lis,
	}, nil
}

// Addr returns the server listening address.
func (s *RunningTestServer) Addr() string {
	return s.addr
}

// Close gracefully shuts down the server and closes the listener.
func (s *RunningTestServer) Close() error {
	s.server.GracefulStop()
	if err := s.lis.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("close listener: %w", err)
	}
	return nil
}

// DialTestServer creates an insecure client connection to the in-process server.
func DialTestServer(ctx context.Context, addr string) (*grpcpkg.ClientConn, error) {
	conn, err := grpcpkg.NewClient(addr, grpcpkg.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial test grpc server: %w", err)
	}
	return conn, nil
}
