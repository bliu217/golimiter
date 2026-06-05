# Simulator CLI

`cmd/sim` is a Go CLI test harness for the gRPC rate limiter service. It sends
configurable `Allow` requests, optionally resets limiter state before a run, and
prints a compact traffic summary. It can also retry transient RPC errors with
exponential backoff, pace request starts with a target rate, categorize final
errors, and write JSON summaries for later analysis.

## Prerequisites

Start the limiter service from the repository root:

```sh
go run ./cmd/limiter
```

The limiter reads `config/limiter.yaml`, so choose the desired backend there
before running the simulator. The default config uses the in-memory token bucket.

## Usage

```sh
go run ./cmd/sim [flags]
```

Flags:

- `-addr`: rate limiter gRPC address. Default: `localhost:50051`.
- `-requests`: total number of `Allow` requests to send. Default: `100`.
- `-concurrency`: number of concurrent workers. Default: `10`.
- `-key`: base request key. Default: `user`.
- `-keys`: number of distinct keys to cycle through. Default: `1`.
- `-resource`: resource name for each request. Default: `default`.
- `-cost`: request cost. Default: `1`.
- `-timeout`: per-RPC timeout. Default: `2s`.
- `-reset`: call the limiter `Reset` RPC before sending traffic.
- `-retries`: retry attempts per request after the initial attempt. Default: `0`.
- `-backoff`: base exponential backoff between retries. Default: `50ms`.
- `-rate`: maximum request dispatch rate per second. Default: `0`, which means unlimited.
- `-output-dir`: directory for JSON summary files. Default: `cmd/sim/summaries`.

When `-keys` is greater than `1`, request keys are generated as
`<key>-<n>`. For example, `-key user -keys 3` cycles through `user-0`,
`user-1`, and `user-2`.

## Examples

Basic smoke test:

```sh
go run ./cmd/sim -addr localhost:50051 -requests 20
```

Reset state before a run:

```sh
go run ./cmd/sim -addr localhost:50051 -requests 100 -concurrency 10 -reset
```

Simulate traffic across multiple users:

```sh
go run ./cmd/sim -addr localhost:50051 -requests 200 -concurrency 20 -key user -keys 10
```

Send a higher-concurrency burst to one resource:

```sh
go run ./cmd/sim -addr localhost:50051 -requests 500 -concurrency 50 -resource checkout -cost 1 -reset
```

Retry transient RPC failures with exponential backoff:

```sh
go run ./cmd/sim -addr localhost:50051 -requests 100 -retries 3 -backoff 100ms
```

Pace traffic at a target request rate:

```sh
go run ./cmd/sim -addr localhost:50051 -requests 300 -concurrency 20 -rate 25
```

Write JSON summaries to a custom directory:

```sh
go run ./cmd/sim -addr localhost:50051 -requests 100 -output-dir /tmp/golimiter-summaries
```

## Output

The simulator prints the run configuration first, then a summary:

- `total`: number of requests attempted.
- `attempts`: total `Allow` RPC attempts, including retries.
- `retry_attempts`: retry attempts made after initial request attempts.
- `allowed`: responses accepted by the limiter.
- `denied`: responses rejected by the limiter.
- `errors`: RPC or transport errors.
- `error_categories`: final request errors grouped by category, such as
  `unavailable`, `deadline_exceeded`, `canceled`, `internal`, `unknown`, and
  `non_grpc_error`.
- `elapsed`: wall-clock runtime.
- `requests_per_second`: attempted requests divided by elapsed time.
- `min_latency`, `avg_latency`, `max_latency`: observed RPC latency.
- `latest_remaining`: latest `remaining` value returned by the limiter.
- `latest_reset_time`: latest `reset_time` value returned by the limiter.

Use `allowed` and `denied` to confirm rate-limit behavior, and use the latency
numbers as a quick signal while developing the limiter service locally.

## JSON Summaries

Each run writes a timestamped JSON summary to `-output-dir`. The default
directory is `cmd/sim/summaries`, which is ignored by git. Summary files include
the run config, request and retry totals, error category counts, elapsed time,
requests per second, latency stats, and the latest rate-limit metadata.
