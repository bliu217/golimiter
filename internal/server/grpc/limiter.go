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
	mu      sync.RWMutex
}

func NewRateLimiterServer(l limiter.Limiter, deps limiter.Deps) *RateLimiterServer {
	return &RateLimiterServer{
		limiter: l,
		deps:    deps,
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
	s.mu.RLock()
	result, err := s.limiter.Allow(bucket_key, float64(req.Cost))
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}

	reason := ""
	if !result.Allowed {
		reason = "rate limit exceeded"
	}

	return &pb.AllowResponse{
		Allowed:   result.Allowed,
		Reason:    reason,
		Remaining: result.Remaining,
		ResetTime: result.ResetTimeSeconds,
	}, nil
}

func (s *RateLimiterServer) Reset(
	ctx context.Context,
	req *pb.ResetRequest,
) (*pb.ResetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.limiter.Reset(); err != nil {
		return nil, err
	}
	return &pb.ResetResponse{Success: true}, nil
}
