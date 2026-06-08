package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bliu217/golimiter/internal/limiter"
	servergrpc "github.com/bliu217/golimiter/internal/server/grpc"
)

func readSingleSummaryFile(t *testing.T, dir string) jsonSummary {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read summary directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("summary file count = %d, want 1", len(entries))
	}

	path := filepath.Join(dir, entries[0].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary file: %v", err)
	}
	var summary jsonSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("unmarshal summary file: %v", err)
	}
	return summary
}

func TestRun_InProcessMemoryServerEndToEnd(t *testing.T) {
	l, err := servergrpc.NewTokenBucketLimiterForTests(2, 0.000001, limiter.Deps{})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	srv, err := servergrpc.StartTestServer(servergrpc.TestServerConfig{
		Limiter: l,
		Deps:    limiter.Deps{},
	})
	if err != nil {
		t.Fatalf("start test server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
	})

	outputDir := t.TempDir()
	cfg := simConfig{
		addr:        srv.Addr(),
		requests:    5,
		concurrency: 1,
		key:         "user",
		keys:        1,
		resource:    "api",
		cost:        1,
		timeout:     time.Second,
		reset:       true,
		retries:     0,
		backoff:     time.Millisecond,
		rate:        0,
		outputDir:   outputDir,
	}

	if err := run(context.Background(), cfg, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}

	summary := readSingleSummaryFile(t, outputDir)
	if summary.Totals.Requests != 5 {
		t.Fatalf("total requests = %d, want 5", summary.Totals.Requests)
	}
	if summary.Totals.Allowed != 2 {
		t.Fatalf("allowed = %d, want 2", summary.Totals.Allowed)
	}
	if summary.Totals.Denied != 3 {
		t.Fatalf("denied = %d, want 3", summary.Totals.Denied)
	}
	if summary.Totals.Errors != 0 {
		t.Fatalf("errors = %d, want 0", summary.Totals.Errors)
	}
}
