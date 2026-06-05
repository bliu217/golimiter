# Simulator CLI

`cmd/sim` is a Go CLI test harness for the gRPC rate limiter service. It sends
configurable `Allow` requests, optionally resets limiter state before a run, and
prints a compact traffic summary.

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

## Output

The simulator prints the run configuration first, then a summary:

- `total`: number of requests attempted.
- `allowed`: responses accepted by the limiter.
- `denied`: responses rejected by the limiter.
- `errors`: RPC or transport errors.
- `elapsed`: wall-clock runtime.
- `requests_per_second`: attempted requests divided by elapsed time.
- `min_latency`, `avg_latency`, `max_latency`: observed RPC latency.
- `latest_remaining`: latest `remaining` value returned by the limiter.
- `latest_reset_time`: latest `reset_time` value returned by the limiter.

Use `allowed` and `denied` to confirm rate-limit behavior, and use the latency
numbers as a quick signal while developing the limiter service locally.
