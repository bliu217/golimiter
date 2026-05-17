package limiter

import (
	// "time"

	"errors"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
)

type Limiter interface {
	Allow(key string, cost float64) (bool, error)
}

func NewLimiterFromConfig(req *pb.ConfigureRequest) (Limiter, error) {
	switch req.Algorithm {
	case pb.Algorithm_TOKEN_BUCKET:
		cfg := req.GetTokenBucket()
		return NewInMemoryTokenBucketLimiter(cfg.Capacity, cfg.RefillRate)

	// case pb.Algorithm_FIXED_WINDOW:
	// 	cfg := req.GetFixedWindow()
	// 	return NewFixedWindowLimiter(cfg.Limit, time.Duration(cfg.WindowSeconds)*time.Second)

	// case pb.Algorithm_SLIDING_WINDOW:
	// 	cfg := req.GetSlidingWindow()
	// 	return NewSlidingWindowLimiter(cfg.Limit, time.Duration(cfg.WindowSeconds)*time.Second)

	default:
		return nil, errors.New("unsupported limiter algorithm")
	}
}