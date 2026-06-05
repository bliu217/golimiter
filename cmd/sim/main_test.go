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

	report := summarize(results, 2*time.Second)
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
	}, 2*time.Second)

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
