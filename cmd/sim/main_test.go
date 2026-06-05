package main

import (
	"errors"
	"io"
	"testing"
	"time"
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
		{allowed: true, latency: 10 * time.Millisecond, remaining: 4},
		{allowed: false, latency: 20 * time.Millisecond, remaining: 0, resetTime: 1},
		{err: errors.New("rpc failed"), latency: 30 * time.Millisecond},
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseConfig(tc.args, io.Discard); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
