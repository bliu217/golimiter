package main

import (
	"fmt"
	"log"
	"net"

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

	l, err := limiter.NewLimiterFromYAMLConfig(cfg.Limiter)
	if err != nil {
		log.Fatalf("failed to create limiter: %v", err)
	}

	handler := grpcServer.NewRateLimiterServer(l)

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