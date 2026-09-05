package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/bliu217/golimiter/generated/proto/limiter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequestKey(t *testing.T) {
	tests := []struct {
		name      string
		base      string
		keyCount  int
		requestID int
		want      string
	}{
		{"single_key_uses_base", "user", 1, 0, "user"},
		{"zero_key_count_uses_base", "user", 0, 3, "user"},
		{"cycles_multiple_keys", "user", 3, 4, "user-1"},
		{"first_multiple_key", "client", 5, 0, "client-0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := requestKey(tc.base, tc.keyCount, tc.requestID)
			if got != tc.want {
				t.Fatalf("requestKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	results := []requestResult{
		{allowed: true, latency: 10 * time.Millisecond, attempts: 1, remaining: 4},
		{allowed: false, latency: 20 * time.Millisecond, attempts: 1, remaining: 0, resetTime: 1},
		{err: errors.New("rpc failed"), errorCategory: "non_grpc_error", latency: 30 * time.Millisecond, attempts: 3},
	}

	report := summarize(results, 2*time.Second, simConfig{cost: 1})
	if report.total != 3 {
		t.Fatalf("total = %d, want 3", report.total)
	}
	if report.allowed != 1 {
		t.Fatalf("allowed = %d, want 1", report.allowed)
	}
	if report.denied != 1 {
		t.Fatalf("denied = %d, want 1", report.denied)
	}
	if report.errors != 1 {
		t.Fatalf("errors = %d, want 1", report.errors)
	}
	if report.attempts != 5 {
		t.Fatalf("attempts = %d, want 5", report.attempts)
	}
	if report.retryAttempts != 2 {
		t.Fatalf("retryAttempts = %d, want 2", report.retryAttempts)
	}
	if report.errorCategories["non_grpc_error"] != 1 {
		t.Fatalf("non_grpc_error count = %d, want 1", report.errorCategories["non_grpc_error"])
	}
	if report.minLatency != 10*time.Millisecond {
		t.Fatalf("minLatency = %s, want 10ms", report.minLatency)
	}
	if report.avgLatency() != 20*time.Millisecond {
		t.Fatalf("avgLatency = %s, want 20ms", report.avgLatency())
	}
	if report.maxLatency != 30*time.Millisecond {
		t.Fatalf("maxLatency = %s, want 30ms", report.maxLatency)
	}
	if got := report.requestsPerSecond(); got != 1.5 {
		t.Fatalf("requestsPerSecond = %v, want 1.5", got)
	}
	if report.latestRemaining != 0 {
		t.Fatalf("latestRemaining = %d, want 0", report.latestRemaining)
	}
	if report.latestResetTime != 1 {
		t.Fatalf("latestResetTime = %d, want 1", report.latestResetTime)
	}
	if report.p50Latency() != 20*time.Millisecond {
		t.Fatalf("p50Latency = %s, want 20ms", report.p50Latency())
	}
	if report.p95Latency() != 30*time.Millisecond {
		t.Fatalf("p95Latency = %s, want 30ms", report.p95Latency())
	}
	if report.p99Latency() != 30*time.Millisecond {
		t.Fatalf("p99Latency = %s, want 30ms", report.p99Latency())
	}
	if got := report.allowedPerSecond(); got != 0.5 {
		t.Fatalf("allowedPerSecond = %v, want 0.5", got)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	latencies := make([]time.Duration, 10)
	for i := range latencies {
		latencies[i] = time.Duration(i+1) * time.Millisecond
	}
	if got := percentile(latencies, 50); got != 5*time.Millisecond {
		t.Fatalf("p50 = %s, want 5ms", got)
	}
	if got := percentile(latencies, 95); got != 10*time.Millisecond {
		t.Fatalf("p95 = %s, want 10ms", got)
	}
	if got := percentile(latencies, 99); got != 10*time.Millisecond {
		t.Fatalf("p99 = %s, want 10ms", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Fatalf("empty p50 = %s, want 0", got)
	}
}

func TestOversubscription(t *testing.T) {
	results := make([]requestResult, 12)
	for i := range results {
		results[i] = requestResult{allowed: true, latency: time.Millisecond, attempts: 1}
	}
	report := summarize(results, 0, simConfig{capacity: 10, refillRate: 0, cost: 1})
	if got := report.expectedTokens(); got != 10 {
		t.Fatalf("expectedTokens = %v, want 10", got)
	}
	if got := report.oversubscription(); got != 2 {
		t.Fatalf("oversubscription = %v, want 2", got)
	}
	if got := report.oversubscriptionRatio(); got != 0.2 {
		t.Fatalf("oversubscriptionRatio = %v, want 0.2", got)
	}
}

func TestOversubscriptionSkippedWithoutCapacity(t *testing.T) {
	results := []requestResult{{allowed: true, latency: time.Millisecond, attempts: 1}}
	report := summarize(results, time.Second, simConfig{cost: 1})
	if report.expectedTokens() != 0 || report.oversubscription() != 0 {
		t.Fatalf("expectedTokens/oversubscription = %v/%v, want 0/0", report.expectedTokens(), report.oversubscription())
	}
}

func TestAddrsParsesCommaSeparatedList(t *testing.T) {
	cfg := simConfig{addr: "localhost:50051, localhost:50052,localhost:50053"}
	got := cfg.addrs()
	want := []string{"localhost:50051", "localhost:50052", "localhost:50053"}
	if len(got) != len(want) {
		t.Fatalf("addrs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("addrs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig([]string{
		"-addr", "127.0.0.1:50052",
		"-requests", "25",
		"-concurrency", "5",
		"-key", "client",
		"-keys", "4",
		"-resource", "search",
		"-cost", "2",
		"-timeout", "500ms",
		"-reset",
		"-retries", "3",
		"-backoff", "25ms",
		"-rate", "12.5",
		"-output-dir", "tmp/summaries",
		"-capacity", "10",
		"-refill-rate", "5",
		"-configure",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.addr != "127.0.0.1:50052" {
		t.Fatalf("addr = %q, want 127.0.0.1:50052", cfg.addr)
	}
	if cfg.requests != 25 || cfg.concurrency != 5 || cfg.keys != 4 {
		t.Fatalf("requests/concurrency/keys = %d/%d/%d, want 25/5/4", cfg.requests, cfg.concurrency, cfg.keys)
	}
	if cfg.key != "client" || cfg.resource != "search" {
		t.Fatalf("key/resource = %q/%q, want client/search", cfg.key, cfg.resource)
	}
	if cfg.cost != 2 {
		t.Fatalf("cost = %d, want 2", cfg.cost)
	}
	if cfg.timeout != 500*time.Millisecond {
		t.Fatalf("timeout = %s, want 500ms", cfg.timeout)
	}
	if !cfg.reset {
		t.Fatal("reset = false, want true")
	}
	if cfg.retries != 3 {
		t.Fatalf("retries = %d, want 3", cfg.retries)
	}
	if cfg.backoff != 25*time.Millisecond {
		t.Fatalf("backoff = %s, want 25ms", cfg.backoff)
	}
	if cfg.rate != 12.5 {
		t.Fatalf("rate = %v, want 12.5", cfg.rate)
	}
	if cfg.outputDir != "tmp/summaries" {
		t.Fatalf("outputDir = %q, want tmp/summaries", cfg.outputDir)
	}
	if cfg.capacity != 10 {
		t.Fatalf("capacity = %v, want 10", cfg.capacity)
	}
	if cfg.refillRate != 5 {
		t.Fatalf("refillRate = %v, want 5", cfg.refillRate)
	}
	if !cfg.configure {
		t.Fatal("configure = false, want true")
	}
}

func TestParseConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"negative_requests", []string{"-requests", "-1"}},
		{"zero_concurrency", []string{"-concurrency", "0"}},
		{"zero_keys", []string{"-keys", "0"}},
		{"zero_cost", []string{"-cost", "0"}},
		{"zero_timeout", []string{"-timeout", "0s"}},
		{"negative_retries", []string{"-retries", "-1"}},
		{"zero_backoff", []string{"-backoff", "0s"}},
		{"negative_rate", []string{"-rate", "-1"}},
		{"empty_output_dir", []string{"-output-dir", ""}},
		{"negative_capacity", []string{"-capacity", "-1"}},
		{"negative_refill_rate", []string{"-refill-rate", "-1"}},
		{"configure_without_capacity", []string{"-configure", "-refill-rate", "5"}},
		{"configure_without_refill", []string{"-configure", "-capacity", "10"}},
		{"empty_addr_list", []string{"-addr", " , "}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseConfig(tc.args, io.Discard); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBackoffDuration(t *testing.T) {
	base := 50 * time.Millisecond
	tests := []struct {
		retryAttempt int
		want         time.Duration
	}{
		{0, base},
		{1, base},
		{2, 100 * time.Millisecond},
		{3, 200 * time.Millisecond},
		{4, 400 * time.Millisecond},
	}
	for _, tc := range tests {
		if got := backoffDuration(base, tc.retryAttempt); got != tc.want {
			t.Fatalf("backoffDuration(%d) = %s, want %s", tc.retryAttempt, got, tc.want)
		}
	}
}

func TestRateInterval(t *testing.T) {
	if got := rateInterval(10); got != 100*time.Millisecond {
		t.Fatalf("rateInterval(10) = %s, want 100ms", got)
	}
	if got := rateInterval(0); got != 0 {
		t.Fatalf("rateInterval(0) = %s, want 0", got)
	}
}

func TestErrorCategory(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"deadline_context", context.DeadlineExceeded, "deadline_exceeded"},
		{"canceled_context", context.Canceled, "canceled"},
		{"unavailable", status.Error(codes.Unavailable, "down"), "unavailable"},
		{"internal", status.Error(codes.Internal, "boom"), "internal"},
		{"unknown", status.Error(codes.Unknown, "unknown"), "unknown"},
		{"plain_error", errors.New("plain"), "non_grpc_error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorCategory(tc.err); got != tc.want {
				t.Fatalf("errorCategory() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSendAllowRetriesUntilSuccess(t *testing.T) {
	client := &fakeRateLimiterClient{
		failures: 2,
		response: &pb.AllowResponse{
			Allowed:   true,
			Remaining: 7,
			ResetTime: 0,
		},
	}
	cfg := simConfig{
		key:      "user",
		keys:     1,
		resource: "api",
		cost:     1,
		timeout:  time.Second,
		retries:  2,
		backoff:  time.Nanosecond,
	}

	result := sendAllow(context.Background(), cfg, client, 0)
	if result.err != nil {
		t.Fatalf("sendAllow err = %v, want nil", result.err)
	}
	if !result.allowed {
		t.Fatal("allowed = false, want true")
	}
	if result.attempts != 3 {
		t.Fatalf("attempts = %d, want 3", result.attempts)
	}
	if client.calls != 3 {
		t.Fatalf("client calls = %d, want 3", client.calls)
	}
}

func TestSendAllowDoesNotRetryDenial(t *testing.T) {
	client := &fakeRateLimiterClient{
		response: &pb.AllowResponse{
			Allowed:   false,
			Remaining: 0,
			ResetTime: 1,
		},
	}
	cfg := simConfig{
		key:      "user",
		keys:     1,
		resource: "api",
		cost:     1,
		timeout:  time.Second,
		retries:  3,
		backoff:  time.Nanosecond,
	}

	result := sendAllow(context.Background(), cfg, client, 0)
	if result.err != nil {
		t.Fatalf("sendAllow err = %v, want nil", result.err)
	}
	if result.allowed {
		t.Fatal("allowed = true, want false")
	}
	if result.attempts != 1 {
		t.Fatalf("attempts = %d, want 1", result.attempts)
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want 1", client.calls)
	}
}

func TestWriteJSONSummary(t *testing.T) {
	cfg := simConfig{
		addr:        "localhost:50051",
		requests:    3,
		concurrency: 1,
		key:         "user",
		keys:        1,
		resource:    "api",
		cost:        1,
		timeout:     time.Second,
		retries:     2,
		backoff:     50 * time.Millisecond,
		rate:        10,
		outputDir:   t.TempDir(),
	}
	report := summarize([]requestResult{
		{allowed: true, attempts: 1, latency: 10 * time.Millisecond},
		{err: status.Error(codes.Unavailable, "down"), errorCategory: "unavailable", attempts: 3, latency: 20 * time.Millisecond},
	}, 2*time.Second, cfg)

	path, err := writeJSONSummary(cfg, report, time.Date(2026, 6, 5, 14, 55, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("writeJSONSummary: %v", err)
	}
	if filepath.Base(path) != "summary-20260605T145500Z.json" {
		t.Fatalf("summary filename = %q", filepath.Base(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var got jsonSummary
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if got.Config.OutputDir != cfg.outputDir {
		t.Fatalf("output_dir = %q, want %q", got.Config.OutputDir, cfg.outputDir)
	}
	if got.Totals.Requests != 2 || got.Totals.Attempts != 4 || got.Totals.RetryAttempts != 2 {
		t.Fatalf("totals = %+v, want requests=2 attempts=4 retries=2", got.Totals)
	}
	if got.ErrorCategories["unavailable"] != 1 {
		t.Fatalf("unavailable errors = %d, want 1", got.ErrorCategories["unavailable"])
	}
	if got.AllowedPerSec != 0.5 {
		t.Fatalf("allowed_per_second = %v, want 0.5", got.AllowedPerSec)
	}
	if got.Latency.P50MS != 10 {
		t.Fatalf("p50_ms = %v, want 10", got.Latency.P50MS)
	}
}

func TestRunRequestsRoundRobinsClients(t *testing.T) {
	first := &fakeRateLimiterClient{response: &pb.AllowResponse{Allowed: true}}
	second := &fakeRateLimiterClient{response: &pb.AllowResponse{Allowed: true}}
	cfg := simConfig{
		requests:    4,
		concurrency: 1,
		key:         "user",
		keys:        1,
		resource:    "api",
		cost:        1,
		timeout:     time.Second,
		backoff:     time.Millisecond,
	}

	results := runRequests(context.Background(), cfg, []pb.RateLimiterClient{first, second})
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4", len(results))
	}
	if first.calls != 2 || second.calls != 2 {
		t.Fatalf("client calls = %d/%d, want 2/2", first.calls, second.calls)
	}
}

type fakeRateLimiterClient struct {
	pb.RateLimiterClient
	failures int
	calls    int
	response *pb.AllowResponse
}

func (f *fakeRateLimiterClient) Allow(
	ctx context.Context,
	in *pb.AllowRequest,
	opts ...grpc.CallOption,
) (*pb.AllowResponse, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, status.Error(codes.Unavailable, "temporary outage")
	}
	return f.response, nil
}

func (f *fakeRateLimiterClient) Configure(
	ctx context.Context,
	in *pb.ConfigureRequest,
	opts ...grpc.CallOption,
) (*pb.ConfigureResponse, error) {
	return &pb.ConfigureResponse{Success: true, Message: "ok"}, nil
}

func (f *fakeRateLimiterClient) Reset(
	ctx context.Context,
	in *pb.ResetRequest,
	opts ...grpc.CallOption,
) (*pb.ResetResponse, error) {
	return &pb.ResetResponse{Success: true}, nil
}
