package limiter

import (
	// "time"

	"errors"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"github.com/bliu217/golimiter/internal/config"
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

func NewLimiterFromYAMLConfig(cfg config.LimiterConfig) (Limiter, error) {
	switch cfg.Algorithm {
	case "token_bucket":
		return NewInMemoryTokenBucketLimiter(
			cfg.TokenBucket.Capacity,
			cfg.TokenBucket.RefillRate,
		)

	// case "fixed_window":
	// 	return NewFixedWindowLimiter(
	// 		cfg.FixedWindow.Limit,
	// 		time.Duration(cfg.FixedWindow.WindowSeconds)*time.Second,
	// 	)

	// case "sliding_window":
	// 	return NewSlidingWindowLimiter(
	// 		cfg.SlidingWindow.Limit,
	// 		time.Duration(cfg.SlidingWindow.WindowSeconds)*time.Second,
	// 	)

	default:
		return nil, errors.New("unsupported limiter algorithm: " + cfg.Algorithm)
	}
}