package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"github.com/bliu217/golimiter/internal/config"
	"github.com/bliu217/golimiter/internal/limiter"
	grpcServer "github.com/bliu217/golimiter/internal/server/grpc"
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
		if err := rdb.Ping(context.Background()).Err(); err != nil {
			log.Fatalf("failed to connect to redis at %s: %v", redisAddr, err)
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

	addr := fmt.Sprintf(":%d", cfg.Server.GRPCPort)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	log.Printf("rate limiter gRPC server listening on %s using %s", addr, cfg.Limiter.Algorithm)

	if err := server.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
