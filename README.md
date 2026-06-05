# Overview
## Purpose
Create a rate-limiting gRPC service for endpoint requests  
Create a shared state to prepare for the deployment of multiple service nodes  
Containerize, monitor, and deploy with Docker + AWS services  
Create a simulator in Go as a test harness for the service  
Create a simple UI to call the simulator with custom parameters  
Load test and expose metrics for the service  

## Learning points
- Practice with gRPC
- Concurrency safety in Go
  - Performance of goroutines vs threads
- Rate limiting algorithms
- Distributed systems
- System design
- Load testing and performance monitoring
  - utilizing my own simulator and k6 to expose different metrics
- In-memory caching for Redis optimization
- Fallback for Redis downtime
# Architecture 
  
## Application Layer Tech Stack
Go - Simulator Client, gRPC Service  
Docker - Multiple nodes  
Redis - Shared state + atomic coordination across nodes  
k6 (TBD) - Load testing  
React - Simple UI for simulator  
Prometheus/Grafana - Metrics + UI  

## AWS Deployment
Clients -> Application Load Balancer -> ECS and ECR -> ElastiCache  
CloudWatch to collect logs/metrics, AppConfig to centralize rate-limiter algorithm selection  

## Distributed State (Redis)
- Atomic Lua scripting (`EVAL` / `EVALSHA` / `SCRIPT LOAD`) keeps refill, limit check, and token decrement in one server-side transaction so concurrent nodes cannot oversubscribe a bucket.
- Server-side `TIME` is used inside the script to avoid clock skew between limiter nodes; tests can override time via script arguments to keep deterministic coverage.
- Per-key state is stored in a single hash with `tokens` and `last_refill_us`, which keeps reads/writes in one round trip and one atomic write path.
- `PEXPIRE` is refreshed on each write so idle buckets are automatically evicted and keyspace growth stays bounded over time.
- Key namespacing uses `{prefix}:tb:{userKey}` to separate environments and keep a stable hash-tag pattern for Redis Cluster slot placement.
- Connection pooling is handled by `go-redis`; per-node pool size should be tuned to expected concurrency and latency targets.
- Failure behavior is currently deny-by-default when Redis is unavailable (errors bubble up); fail-open or fallback modes are tracked as separate follow-up work.
- Cross-node consistency is strong per key because all bucket updates serialize through the same Redis primary.

## Simulator
The simulator is a Go CLI test harness for the gRPC rate limiter. It sends
configurable `Allow` requests to a running limiter service so you can quickly
exercise in-memory or Redis-backed token bucket behavior during local
development.

The CLI can vary request count, concurrency, key cardinality, resource, cost,
per-RPC timeout, retry/backoff behavior, request dispatch rate, and whether to
reset limiter state before a run. Its summary reports allowed, denied, and
error totals along with throughput, latency, error categories, and the latest
`remaining` / `reset_time` metadata returned by the service. Each run also
writes a JSON summary for later analysis.

Example:

```sh
go run ./cmd/sim -addr localhost:50051 -requests 100 -concurrency 10 -keys 5 -reset
```

See [`cmd/sim/README.md`](cmd/sim/README.md) for the full flag reference and
additional examples.

## UML


# Metrics
## Simulator

## k6
