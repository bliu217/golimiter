package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type simConfig struct {
	addr        string
	requests    int
	concurrency int
	key         string
	keys        int
	resource    string
	cost        int32
	timeout     time.Duration
	reset       bool
}

type requestResult struct {
	allowed   bool
	err       error
	latency   time.Duration
	remaining int32
	resetTime int64
}

type summary struct {
	total           int
	allowed         int
	denied          int
	errors          int
	elapsed         time.Duration
	minLatency      time.Duration
	maxLatency      time.Duration
	totalLatency    time.Duration
	latestRemaining int32
	latestResetTime int64
}

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "sim: %v\n", err)
		os.Exit(2)
	}

	if err := run(context.Background(), cfg, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "sim: %v\n", err)
		os.Exit(1)
	}
}

func parseConfig(args []string, output io.Writer) (simConfig, error) {
	cfg := simConfig{
		addr:        "localhost:50051",
		requests:    100,
		concurrency: 10,
		key:         "user",
		keys:        1,
		resource:    "default",
		cost:        1,
		timeout:     2 * time.Second,
	}

	fs := flag.NewFlagSet("sim", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.addr, "addr", cfg.addr, "rate limiter gRPC address")
	fs.IntVar(&cfg.requests, "requests", cfg.requests, "total number of Allow requests to send")
	fs.IntVar(&cfg.concurrency, "concurrency", cfg.concurrency, "number of concurrent workers")
	fs.StringVar(&cfg.key, "key", cfg.key, "base request key")
	fs.IntVar(&cfg.keys, "keys", cfg.keys, "number of distinct keys to cycle through")
	fs.StringVar(&cfg.resource, "resource", cfg.resource, "resource name for each request")
	fs.Var((*int32Value)(&cfg.cost), "cost", "request cost")
	fs.DurationVar(&cfg.timeout, "timeout", cfg.timeout, "per-RPC timeout")
	fs.BoolVar(&cfg.reset, "reset", cfg.reset, "call Reset before the run")

	if err := fs.Parse(args); err != nil {
		return simConfig{}, err
	}
	return cfg, cfg.validate()
}

func (cfg simConfig) validate() error {
	if cfg.addr == "" {
		return errors.New("addr cannot be empty")
	}
	if cfg.requests < 0 {
		return errors.New("requests must be non-negative")
	}
	if cfg.concurrency <= 0 {
		return errors.New("concurrency must be greater than 0")
	}
	if cfg.key == "" {
		return errors.New("key cannot be empty")
	}
	if cfg.keys <= 0 {
		return errors.New("keys must be greater than 0")
	}
	if cfg.resource == "" {
		return errors.New("resource cannot be empty")
	}
	if cfg.cost <= 0 {
		return errors.New("cost must be greater than 0")
	}
	if cfg.timeout <= 0 {
		return errors.New("timeout must be greater than 0")
	}
	return nil
}

func run(ctx context.Context, cfg simConfig, output io.Writer) error {
	conn, err := grpc.NewClient(cfg.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("create grpc client: %w", err)
	}
	defer conn.Close()

	client := pb.NewRateLimiterClient(conn)
	if cfg.reset {
		resetCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
		_, err := client.Reset(resetCtx, &pb.ResetRequest{})
		cancel()
		if err != nil {
			return fmt.Errorf("reset limiter: %w", err)
		}
	}

	printConfig(output, cfg)
	start := time.Now()
	results := runRequests(ctx, cfg, client)
	report := summarize(results, time.Since(start))
	printSummary(output, report)
	return nil
}

func runRequests(ctx context.Context, cfg simConfig, client pb.RateLimiterClient) []requestResult {
	jobs := make(chan int)
	results := make(chan requestResult, cfg.requests)

	var wg sync.WaitGroup
	wg.Add(cfg.concurrency)
	for worker := 0; worker < cfg.concurrency; worker++ {
		go func() {
			defer wg.Done()
			for requestID := range jobs {
				results <- sendAllow(ctx, cfg, client, requestID)
			}
		}()
	}

	go func() {
		for requestID := 0; requestID < cfg.requests; requestID++ {
			jobs <- requestID
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	collected := make([]requestResult, 0, cfg.requests)
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func sendAllow(ctx context.Context, cfg simConfig, client pb.RateLimiterClient, requestID int) requestResult {
	reqCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	req := &pb.AllowRequest{
		Key:      requestKey(cfg.key, cfg.keys, requestID),
		Resource: cfg.resource,
		Cost:     cfg.cost,
	}

	start := time.Now()
	resp, err := client.Allow(reqCtx, req)
	latency := time.Since(start)
	if err != nil {
		return requestResult{err: err, latency: latency}
	}
	return requestResult{
		allowed:   resp.Allowed,
		latency:   latency,
		remaining: resp.Remaining,
		resetTime: resp.ResetTime,
	}
}

func requestKey(base string, keyCount int, requestID int) string {
	if keyCount <= 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, requestID%keyCount)
}

func summarize(results []requestResult, elapsed time.Duration) summary {
	report := summary{
		total:   len(results),
		elapsed: elapsed,
	}
	for _, result := range results {
		report.add(result)
	}
	return report
}

func (s *summary) add(result requestResult) {
	if result.err != nil {
		s.errors++
	} else if result.allowed {
		s.allowed++
	} else {
		s.denied++
	}

	if result.latency > 0 {
		if s.minLatency == 0 || result.latency < s.minLatency {
			s.minLatency = result.latency
		}
		if result.latency > s.maxLatency {
			s.maxLatency = result.latency
		}
		s.totalLatency += result.latency
	}

	if result.err == nil {
		s.latestRemaining = result.remaining
		s.latestResetTime = result.resetTime
	}
}

func (s summary) avgLatency() time.Duration {
	if s.total == 0 {
		return 0
	}
	return s.totalLatency / time.Duration(s.total)
}

func (s summary) requestsPerSecond() float64 {
	if s.elapsed <= 0 {
		return 0
	}
	return float64(s.total) / s.elapsed.Seconds()
}

func printConfig(output io.Writer, cfg simConfig) {
	fmt.Fprintf(output, "simulator config\n")
	fmt.Fprintf(output, "  addr: %s\n", cfg.addr)
	fmt.Fprintf(output, "  requests: %d\n", cfg.requests)
	fmt.Fprintf(output, "  concurrency: %d\n", cfg.concurrency)
	fmt.Fprintf(output, "  key: %s\n", cfg.key)
	fmt.Fprintf(output, "  keys: %d\n", cfg.keys)
	fmt.Fprintf(output, "  resource: %s\n", cfg.resource)
	fmt.Fprintf(output, "  cost: %d\n", cfg.cost)
	fmt.Fprintf(output, "  timeout: %s\n", cfg.timeout)
	fmt.Fprintf(output, "  reset: %t\n", cfg.reset)
}

func printSummary(output io.Writer, report summary) {
	fmt.Fprintf(output, "\nsummary\n")
	fmt.Fprintf(output, "  total: %d\n", report.total)
	fmt.Fprintf(output, "  allowed: %d\n", report.allowed)
	fmt.Fprintf(output, "  denied: %d\n", report.denied)
	fmt.Fprintf(output, "  errors: %d\n", report.errors)
	fmt.Fprintf(output, "  elapsed: %s\n", report.elapsed.Round(time.Millisecond))
	fmt.Fprintf(output, "  requests_per_second: %.2f\n", roundFloat(report.requestsPerSecond(), 2))
	fmt.Fprintf(output, "  min_latency: %s\n", report.minLatency.Round(time.Microsecond))
	fmt.Fprintf(output, "  avg_latency: %s\n", report.avgLatency().Round(time.Microsecond))
	fmt.Fprintf(output, "  max_latency: %s\n", report.maxLatency.Round(time.Microsecond))
	fmt.Fprintf(output, "  latest_remaining: %d\n", report.latestRemaining)
	fmt.Fprintf(output, "  latest_reset_time: %d\n", report.latestResetTime)
}

func roundFloat(value float64, places int) float64 {
	scale := math.Pow(10, float64(places))
	return math.Round(value*scale) / scale
}

type int32Value int32

func (v *int32Value) String() string {
	return fmt.Sprintf("%d", int32(*v))
}

func (v *int32Value) Set(s string) error {
	parsed, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*v = int32Value(parsed)
	return nil
}
