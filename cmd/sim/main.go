package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
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
	retries     int
	backoff     time.Duration
	rate        float64
	outputDir   string
}

type requestResult struct {
	allowed       bool
	err           error
	errorCategory string
	latency       time.Duration
	attempts      int
	remaining     int32
	resetTime     int64
}

type summary struct {
	total           int
	attempts        int
	retryAttempts   int
	allowed         int
	denied          int
	errors          int
	errorCategories map[string]int
	elapsed         time.Duration
	minLatency      time.Duration
	maxLatency      time.Duration
	totalLatency    time.Duration
	latestRemaining int32
	latestResetTime int64
}

type jsonSummary struct {
	Config          jsonConfig     `json:"config"`
	Totals          jsonTotals     `json:"totals"`
	ErrorCategories map[string]int `json:"error_categories"`
	Elapsed         string         `json:"elapsed"`
	RequestsPerSec  float64        `json:"requests_per_second"`
	Latency         jsonLatency    `json:"latency"`
	LatestRemaining int32          `json:"latest_remaining"`
	LatestResetTime int64          `json:"latest_reset_time"`
}

type jsonConfig struct {
	Addr        string  `json:"addr"`
	Requests    int     `json:"requests"`
	Concurrency int     `json:"concurrency"`
	Key         string  `json:"key"`
	Keys        int     `json:"keys"`
	Resource    string  `json:"resource"`
	Cost        int32   `json:"cost"`
	Timeout     string  `json:"timeout"`
	Reset       bool    `json:"reset"`
	Retries     int     `json:"retries"`
	Backoff     string  `json:"backoff"`
	Rate        float64 `json:"rate"`
	OutputDir   string  `json:"output_dir"`
}

type jsonTotals struct {
	Requests      int `json:"requests"`
	Attempts      int `json:"attempts"`
	RetryAttempts int `json:"retry_attempts"`
	Allowed       int `json:"allowed"`
	Denied        int `json:"denied"`
	Errors        int `json:"errors"`
}

type jsonLatency struct {
	Min   string  `json:"min"`
	Avg   string  `json:"avg"`
	Max   string  `json:"max"`
	MinMS float64 `json:"min_ms"`
	AvgMS float64 `json:"avg_ms"`
	MaxMS float64 `json:"max_ms"`
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
		backoff:     50 * time.Millisecond,
		outputDir:   "cmd/sim/summaries",
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
	fs.IntVar(&cfg.retries, "retries", cfg.retries, "retry attempts per request after the initial attempt")
	fs.DurationVar(&cfg.backoff, "backoff", cfg.backoff, "base exponential backoff between retries")
	fs.Float64Var(&cfg.rate, "rate", cfg.rate, "maximum request dispatch rate per second; 0 means unlimited")
	fs.StringVar(&cfg.outputDir, "output-dir", cfg.outputDir, "directory for JSON summary files")

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
	if cfg.retries < 0 {
		return errors.New("retries must be non-negative")
	}
	if cfg.backoff <= 0 {
		return errors.New("backoff must be greater than 0")
	}
	if cfg.rate < 0 {
		return errors.New("rate must be non-negative")
	}
	if strings.TrimSpace(cfg.outputDir) == "" {
		return errors.New("output-dir cannot be empty")
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
	summaryPath, err := writeJSONSummary(cfg, report, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "  summary_file: %s\n", summaryPath)
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
		var ticker *time.Ticker
		if cfg.rate > 0 {
			ticker = time.NewTicker(rateInterval(cfg.rate))
			defer ticker.Stop()
		}

		for requestID := 0; requestID < cfg.requests; requestID++ {
			if ticker != nil {
				<-ticker.C
			}
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
	req := &pb.AllowRequest{
		Key:      requestKey(cfg.key, cfg.keys, requestID),
		Resource: cfg.resource,
		Cost:     cfg.cost,
	}

	start := time.Now()
	maxAttempts := cfg.retries + 1
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := waitBackoff(ctx, backoffDuration(cfg.backoff, attempt-1)); err != nil {
				return requestResult{
					err:           err,
					errorCategory: errorCategory(err),
					latency:       time.Since(start),
					attempts:      attempt - 1,
				}
			}
		}

		reqCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
		resp, err := client.Allow(reqCtx, req)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		return requestResult{
			allowed:   resp.Allowed,
			latency:   time.Since(start),
			attempts:  attempt,
			remaining: resp.Remaining,
			resetTime: resp.ResetTime,
		}
	}

	return requestResult{
		err:           lastErr,
		errorCategory: errorCategory(lastErr),
		latency:       time.Since(start),
		attempts:      maxAttempts,
	}
}

func waitBackoff(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func backoffDuration(base time.Duration, retryAttempt int) time.Duration {
	if retryAttempt <= 1 {
		return base
	}
	const maxDuration = time.Duration(1<<63 - 1)
	d := base
	for i := 1; i < retryAttempt; i++ {
		if d > maxDuration/2 {
			return maxDuration
		}
		d *= 2
	}
	return d
}

func rateInterval(rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	interval := time.Duration(float64(time.Second) / rate)
	if interval < time.Nanosecond {
		return time.Nanosecond
	}
	return interval
}

func requestKey(base string, keyCount int, requestID int) string {
	if keyCount <= 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, requestID%keyCount)
}

func summarize(results []requestResult, elapsed time.Duration) summary {
	report := summary{
		total:           len(results),
		elapsed:         elapsed,
		errorCategories: make(map[string]int),
	}
	for _, result := range results {
		report.add(result)
	}
	return report
}

func (s *summary) add(result requestResult) {
	s.attempts += result.attempts
	if result.attempts > 1 {
		s.retryAttempts += result.attempts - 1
	}

	if result.err != nil {
		s.errors++
		s.errorCategories[result.errorCategory]++
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
	fmt.Fprintf(output, "  retries: %d\n", cfg.retries)
	fmt.Fprintf(output, "  backoff: %s\n", cfg.backoff)
	fmt.Fprintf(output, "  rate: %.2f\n", cfg.rate)
	fmt.Fprintf(output, "  output_dir: %s\n", cfg.outputDir)
}

func printSummary(output io.Writer, report summary) {
	fmt.Fprintf(output, "\nsummary\n")
	fmt.Fprintf(output, "  total: %d\n", report.total)
	fmt.Fprintf(output, "  attempts: %d\n", report.attempts)
	fmt.Fprintf(output, "  retry_attempts: %d\n", report.retryAttempts)
	fmt.Fprintf(output, "  allowed: %d\n", report.allowed)
	fmt.Fprintf(output, "  denied: %d\n", report.denied)
	fmt.Fprintf(output, "  errors: %d\n", report.errors)
	if len(report.errorCategories) > 0 {
		fmt.Fprintf(output, "  error_categories:\n")
		for _, category := range sortedKeys(report.errorCategories) {
			fmt.Fprintf(output, "    %s: %d\n", category, report.errorCategories[category])
		}
	}
	fmt.Fprintf(output, "  elapsed: %s\n", report.elapsed.Round(time.Millisecond))
	fmt.Fprintf(output, "  requests_per_second: %.2f\n", roundFloat(report.requestsPerSecond(), 2))
	fmt.Fprintf(output, "  min_latency: %s\n", report.minLatency.Round(time.Microsecond))
	fmt.Fprintf(output, "  avg_latency: %s\n", report.avgLatency().Round(time.Microsecond))
	fmt.Fprintf(output, "  max_latency: %s\n", report.maxLatency.Round(time.Microsecond))
	fmt.Fprintf(output, "  latest_remaining: %d\n", report.latestRemaining)
	fmt.Fprintf(output, "  latest_reset_time: %d\n", report.latestResetTime)
}

func sortedKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func errorCategory(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	st, ok := status.FromError(err)
	if !ok {
		return "non_grpc_error"
	}
	switch st.Code() {
	case codes.DeadlineExceeded:
		return "deadline_exceeded"
	case codes.Unavailable:
		return "unavailable"
	case codes.Canceled:
		return "canceled"
	case codes.Internal:
		return "internal"
	case codes.Unknown:
		return "unknown"
	default:
		return strings.ToLower(st.Code().String())
	}
}

func writeJSONSummary(cfg simConfig, report summary, now time.Time) (string, error) {
	if err := os.MkdirAll(cfg.outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create summary output directory: %w", err)
	}
	path := filepath.Join(cfg.outputDir, fmt.Sprintf("summary-%s.json", now.UTC().Format("20060102T150405Z")))
	data, err := json.MarshalIndent(newJSONSummary(cfg, report), "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal summary: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write summary: %w", err)
	}
	return path, nil
}

func newJSONSummary(cfg simConfig, report summary) jsonSummary {
	return jsonSummary{
		Config: jsonConfig{
			Addr:        cfg.addr,
			Requests:    cfg.requests,
			Concurrency: cfg.concurrency,
			Key:         cfg.key,
			Keys:        cfg.keys,
			Resource:    cfg.resource,
			Cost:        cfg.cost,
			Timeout:     cfg.timeout.String(),
			Reset:       cfg.reset,
			Retries:     cfg.retries,
			Backoff:     cfg.backoff.String(),
			Rate:        cfg.rate,
			OutputDir:   cfg.outputDir,
		},
		Totals: jsonTotals{
			Requests:      report.total,
			Attempts:      report.attempts,
			RetryAttempts: report.retryAttempts,
			Allowed:       report.allowed,
			Denied:        report.denied,
			Errors:        report.errors,
		},
		ErrorCategories: copyStringIntMap(report.errorCategories),
		Elapsed:         report.elapsed.String(),
		RequestsPerSec:  roundFloat(report.requestsPerSecond(), 2),
		Latency: jsonLatency{
			Min:   report.minLatency.String(),
			Avg:   report.avgLatency().String(),
			Max:   report.maxLatency.String(),
			MinMS: roundFloat(durationMilliseconds(report.minLatency), 3),
			AvgMS: roundFloat(durationMilliseconds(report.avgLatency()), 3),
			MaxMS: roundFloat(durationMilliseconds(report.maxLatency), 3),
		},
		LatestRemaining: report.latestRemaining,
		LatestResetTime: report.latestResetTime,
	}
}

func copyStringIntMap(values map[string]int) map[string]int {
	copied := make(map[string]int, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func durationMilliseconds(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
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
