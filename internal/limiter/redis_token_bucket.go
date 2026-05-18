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
	rdb        redis.Scripter
	script     *redis.Script
	capacity   float64
	refillRate float64
	keyPrefix  string
	clock      Clock
}

func NewRedisTokenBucketLimiter(rdb redis.Scripter, capacity, refillRate float64, keyPrefix string) (Limiter, error) {
	return newRedisTokenBucketLimiterWithClock(rdb, capacity, refillRate, keyPrefix, nil)
}

func newRedisTokenBucketLimiterWithClock(
	rdb redis.Scripter,
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

func (l *RedisTokenBucketLimiter) Allow(key string, cost float64) (bool, error) {
	if key == "" {
		return false, errors.New("key cannot be empty")
	}
	if cost <= 0 {
		return false, errors.New("cost must be positive")
	}
	if cost > l.capacity {
		return false, errors.New("cost exceeds bucket capacity")
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
		return false, fmt.Errorf("redis token bucket script failed: %w", err)
	}

	raw, ok := result.([]interface{})
	if !ok || len(raw) < 1 {
		return false, fmt.Errorf("unexpected redis token bucket script result type %T", result)
	}

	allowedInt, err := toInt64(raw[0])
	if err != nil {
		return false, fmt.Errorf("failed to parse allowed flag: %w", err)
	}

	return allowedInt == 1, nil
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
