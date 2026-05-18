package grpc

import (
	"context"
	"sync"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"github.com/bliu217/golimiter/internal/limiter"
)

type RateLimiterServer struct {
	pb.UnimplementedRateLimiterServer
	limiter limiter.Limiter
	deps    limiter.Deps
	mu      sync.Mutex
}

func NewRateLimiterServer(l limiter.Limiter, deps limiter.Deps) *RateLimiterServer {
	return &RateLimiterServer{
		limiter: l,
		deps:    deps,
		mu:      sync.Mutex{},
	}
}

func (s *RateLimiterServer) Configure(
	ctx context.Context,
	req *pb.ConfigureRequest,
) (*pb.ConfigureResponse, error) {
	l, err := limiter.NewLimiterFromConfigWithDeps(req, s.deps)
	if err != nil {
		return &pb.ConfigureResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	s.mu.Lock()
	s.limiter = l
	s.mu.Unlock()
	return &pb.ConfigureResponse{
		Success: true,
		Message: "configuration updated successfully",
	}, nil
}

func (s *RateLimiterServer) Allow(
	ctx context.Context,
	req *pb.AllowRequest,
) (*pb.AllowResponse, error) {
	bucket_key := req.Key + ":" + req.Resource
	allowed, err := s.limiter.Allow(bucket_key, float64(req.Cost))
	if err != nil {
		return nil, err
	}

	reason := ""
	if !allowed {
		reason = "rate limit exceeded"
	}

	return &pb.AllowResponse{
		Allowed: allowed,
		Reason:  reason,
	}, nil
}
