package limiter

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/token_bucket.lua
var tokenBucketLua string

type RedisTokenBucketLimiter struct {
	rdb        RedisClient
	script     *redis.Script
	capacity   float64
	refillRate float64
	keyPrefix  string
	clock      Clock
}

func NewRedisTokenBucketLimiter(rdb RedisClient, capacity, refillRate float64, keyPrefix string) (Limiter, error) {
	return newRedisTokenBucketLimiterWithClock(rdb, capacity, refillRate, keyPrefix, nil)
}

func newRedisTokenBucketLimiterWithClock(
	rdb RedisClient,
	capacity, refillRate float64,
	keyPrefix string,
	clock Clock,
) (*RedisTokenBucketLimiter, error) {
	if rdb == nil {
		return nil, errors.New("redis client cannot be nil")
	}
	if capacity <= 0 {
		return nil, errors.New("capacity must be greater than 0")
	}
	if refillRate <= 0 {
		return nil, errors.New("refill rate must be greater than 0")
	}
	if strings.TrimSpace(keyPrefix) == "" {
		keyPrefix = "golimiter"
	}

	return &RedisTokenBucketLimiter{
		rdb:        rdb,
		script:     redis.NewScript(tokenBucketLua),
		capacity:   capacity,
		refillRate: refillRate,
		keyPrefix:  keyPrefix,
		clock:      clock,
	}, nil
}

func (l *RedisTokenBucketLimiter) Allow(key string, cost float64) (AllowResult, error) {
	if key == "" {
		return AllowResult{}, errors.New("key cannot be empty")
	}
	if cost <= 0 {
		return AllowResult{}, errors.New("cost must be positive")
	}
	if cost > l.capacity {
		return AllowResult{}, errors.New("cost exceeds bucket capacity")
	}

	nowOverride := int64(-1)
	if l.clock != nil {
		nowOverride = l.clock.Now().UnixMicro()
	}

	result, err := l.script.Run(
		context.Background(),
		l.rdb,
		[]string{fmt.Sprintf("%s:tb:{%s}", l.keyPrefix, key)},
		l.capacity,
		l.refillRate,
		cost,
		nowOverride,
	).Result()
	if err != nil {
		return AllowResult{}, fmt.Errorf("redis token bucket script failed: %w", err)
	}

	raw, ok := result.([]interface{})
	if !ok || len(raw) < 3 {
		return AllowResult{}, fmt.Errorf("unexpected redis token bucket script result type %T", result)
	}

	allowedInt, err := toInt64(raw[0])
	if err != nil {
		return AllowResult{}, fmt.Errorf("failed to parse allowed flag: %w", err)
	}
	tokens, err := toFloat64(raw[1])
	if err != nil {
		return AllowResult{}, fmt.Errorf("failed to parse remaining tokens: %w", err)
	}
	resetTimeSeconds, err := toInt64(raw[2])
	if err != nil {
		return AllowResult{}, fmt.Errorf("failed to parse reset time: %w", err)
	}

	return AllowResult{
		Allowed:          allowedInt == 1,
		Remaining:        remainingTokens(tokens),
		ResetTimeSeconds: resetTimeSeconds,
	}, nil
}

func (l *RedisTokenBucketLimiter) Reset() error {
	ctx := context.Background()
	var cursor uint64
	pattern := fmt.Sprintf("%s:tb:*", l.keyPrefix)
	for {
		keys, nextCursor, err := l.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("scan redis token bucket keys: %w", err)
		}
		if len(keys) > 0 {
			if err := l.rdb.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("delete redis token bucket keys: %w", err)
			}
		}
		if nextCursor == 0 {
			return nil
		}
		cursor = nextCursor
	}
}

func toInt64(v interface{}) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case uint64:
		return int64(n), nil
	case string:
		return strconv.ParseInt(n, 10, 64)
	default:
		return 0, fmt.Errorf("unexpected numeric type %T", v)
	}
}

func toFloat64(v interface{}) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case int:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case string:
		return strconv.ParseFloat(n, 64)
	default:
		return 0, fmt.Errorf("unexpected numeric type %T", v)
	}
}
