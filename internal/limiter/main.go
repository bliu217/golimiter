package limiter

import (
	"context"
	"errors"
	"strings"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"github.com/bliu217/golimiter/internal/config"
	"github.com/redis/go-redis/v9"
)

type AllowResult struct {
	Allowed          bool
	Remaining        int32
	ResetTimeSeconds int64
}

type Limiter interface {
	Allow(key string, cost float64) (AllowResult, error)
	Reset() error
}

type Deps struct {
	RedisClient    RedisClient
	RedisKeyPrefix string
}

type RedisClient interface {
	redis.Scripter
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

func NewLimiterFromConfig(req *pb.ConfigureRequest) (Limiter, error) {
	return NewLimiterFromConfigWithDeps(req, Deps{})
}

func NewLimiterFromConfigWithDeps(req *pb.ConfigureRequest, deps Deps) (Limiter, error) {
	switch req.Algorithm {
	case pb.Algorithm_TOKEN_BUCKET:
		cfg := req.GetTokenBucket()
		if deps.RedisClient != nil {
			return NewRedisTokenBucketLimiter(
				deps.RedisClient,
				cfg.Capacity,
				cfg.RefillRate,
				deps.RedisKeyPrefix,
			)
		}
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

func NewLimiterFromYAMLConfig(cfg *config.Config, deps Deps) (Limiter, error) {
	storage := strings.ToLower(strings.TrimSpace(cfg.Storage))
	if storage == "" {
		storage = "memory"
	}

	switch storage {
	case "memory":
		return newLimiterFromAlgorithm(cfg.Limiter)
	case "redis":
		switch cfg.Limiter.Algorithm {
		case "token_bucket":
			return NewRedisTokenBucketLimiter(
				deps.RedisClient,
				cfg.Limiter.TokenBucket.Capacity,
				cfg.Limiter.TokenBucket.RefillRate,
				cfg.Redis.KeyPrefix,
			)
		default:
			return nil, errors.New("unsupported limiter algorithm: " + cfg.Limiter.Algorithm)
		}
	default:
		return nil, errors.New("unsupported limiter storage: " + cfg.Storage)
	}
}

func newLimiterFromAlgorithm(cfg config.LimiterConfig) (Limiter, error) {
	switch cfg.Algorithm {
	case "token_bucket":
		return NewInMemoryTokenBucketLimiter(cfg.TokenBucket.Capacity, cfg.TokenBucket.RefillRate)
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
