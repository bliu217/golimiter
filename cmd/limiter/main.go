package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"github.com/bliu217/golimiter/internal/config"
	"github.com/bliu217/golimiter/internal/limiter"
	grpcServer "github.com/bliu217/golimiter/internal/server/grpc"
)

const (
	redisPingAttempts = 30
	redisPingBackoff  = 500 * time.Millisecond
	shutdownWait      = 25 * time.Second
)

func main() {
	cfg, err := config.Load("config/limiter.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	var deps limiter.Deps
	if strings.EqualFold(strings.TrimSpace(cfg.Storage), "redis") {
		redisAddr := cfg.Redis.Addr
		if strings.TrimSpace(redisAddr) == "" {
			redisAddr = "localhost:6379"
		}

		rdb := redis.NewClient(&redis.Options{
			Addr:         redisAddr,
			Password:     cfg.Redis.Password,
			DB:           cfg.Redis.DB,
			DialTimeout:  time.Duration(cfg.Redis.DialTimeoutMS) * time.Millisecond,
			ReadTimeout:  time.Duration(cfg.Redis.ReadTimeoutMS) * time.Millisecond,
			WriteTimeout: time.Duration(cfg.Redis.WriteTimeoutMS) * time.Millisecond,
			PoolSize:     cfg.Redis.PoolSize,
		})
		if err := pingRedis(context.Background(), rdb, redisAddr, redisPingAttempts, redisPingBackoff); err != nil {
			log.Fatalf("%v", err)
		}
		defer func() {
			_ = rdb.Close()
		}()

		deps = limiter.Deps{
			RedisClient:    rdb,
			RedisKeyPrefix: cfg.Redis.KeyPrefix,
		}
	}

	l, err := limiter.NewLimiterFromYAMLConfig(cfg, deps)
	if err != nil {
		log.Fatalf("failed to create limiter: %v", err)
	}

	handler := grpcServer.NewRateLimiterServer(l, deps)

	server := grpc.NewServer()
	pb.RegisterRateLimiterServer(server, handler)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	addr := fmt.Sprintf(":%d", cfg.Server.GRPCPort)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("rate limiter gRPC server listening on %s using %s", addr, cfg.Limiter.Algorithm)
		errCh <- server.Serve(lis)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		stopped := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(shutdownWait):
			log.Printf("graceful stop timed out after %s, forcing stop", shutdownWait)
			server.Stop()
		}
	}
}

type redisPinger interface {
	Ping(ctx context.Context) *redis.StatusCmd
}

func pingRedis(ctx context.Context, rdb redisPinger, addr string, attempts int, backoff time.Duration) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := rdb.Ping(ctx).Err(); err == nil {
			if i > 0 {
				log.Printf("connected to redis at %s after %d attempts", addr, i+1)
			}
			return nil
		} else {
			lastErr = err
			log.Printf("waiting for redis at %s (%d/%d): %v", addr, i+1, attempts, err)
		}
		if i == attempts-1 {
			break
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("failed to connect to redis at %s: %w", addr, ctx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("failed to connect to redis at %s: %w", addr, lastErr)
}
