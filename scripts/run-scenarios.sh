#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/run-scenarios.sh <memory|redis-1|redis-3>

Runs the burst, sustained, cardinality, and saturation scenarios against a
running limiter. Start the matching topology first:

  memory   go run ./cmd/limiter
  redis-1  docker compose up --build redis limiter
  redis-3  docker compose -f docker-compose.yml -f docker-compose.multi.yml \
             up --build redis limiter-1 limiter-2 limiter-3
EOF
}

if [[ $# -ne 1 ]]; then
  usage
  exit 2
fi

TARGET="$1"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

case "$TARGET" in
  memory|redis-1)
    ADDR="localhost:50051"
    ;;
  redis-3)
    ADDR="localhost:50051,localhost:50052,localhost:50053"
    ;;
  *)
    echo "run-scenarios: unknown target $TARGET" >&2
    usage
    exit 2
    ;;
esac

OUTPUT_ROOT="$ROOT/benchmarks/results/$TARGET"
rm -rf "$OUTPUT_ROOT"
mkdir -p "$OUTPUT_ROOT"

SIM_BIN="$(mktemp -d)/golimiter-sim"
go build -o "$SIM_BIN" ./cmd/sim

wait_for_addr() {
  local addr="$1"
  local host="${addr%%:*}"
  local port="${addr##*:}"
  local i
  for i in $(seq 1 40); do
    if bash -c "echo >/dev/tcp/${host}/${port}" 2>/dev/null; then
      return 0
    fi
    sleep 0.25
  done
  echo "run-scenarios: timed out waiting for $addr" >&2
  return 1
}

IFS=',' read -r -a ADDRS <<<"$ADDR"
for addr in "${ADDRS[@]}"; do
  addr="$(echo "$addr" | xargs)"
  wait_for_addr "$addr"
done

run_sim() {
  local name="$1"
  shift
  local out_dir="$OUTPUT_ROOT/$name"
  mkdir -p "$out_dir"
  echo "=== $TARGET / $name ==="
  "$SIM_BIN" -addr "$ADDR" -output-dir "$out_dir" "$@"
}

WIDE_CAPACITY="1000000000"
WIDE_REFILL="1000000000"

run_sim burst-hotkey \
  -configure -capacity 10 -refill-rate 0.001 \
  -requests 200 -concurrency 50 -keys 1 -reset

run_sim sustained-limit \
  -configure -capacity 10 -refill-rate 5 \
  -requests 300 -concurrency 20 -rate 25 -reset

run_sim cardinality-hot \
  -configure -capacity "$WIDE_CAPACITY" -refill-rate "$WIDE_REFILL" \
  -requests 2000 -concurrency 100 -keys 1 -reset

run_sim cardinality-wide \
  -configure -capacity "$WIDE_CAPACITY" -refill-rate "$WIDE_REFILL" \
  -requests 2000 -concurrency 100 -keys 1000 -reset

for concurrency in 1 10 50 100 200; do
  run_sim "saturation-c${concurrency}" \
    -configure -capacity "$WIDE_CAPACITY" -refill-rate "$WIDE_REFILL" \
    -requests 5000 -concurrency "$concurrency" -keys 1 -reset
done

python3 - "$OUTPUT_ROOT" "$TARGET" <<'PY'
import json
import sys
from pathlib import Path

root = Path(sys.argv[1])
target = sys.argv[2]

def load(name: str) -> dict:
    files = sorted((root / name).glob("summary-*.json"))
    if not files:
        raise SystemExit(f"missing summary for {name}")
    return json.loads(files[-1].read_text())

def ms(value) -> str:
    return f"{value:.3f}"

def f4(value) -> str:
    return f"{value:.4f}"

def f2(value) -> str:
    return f"{value:.2f}"

print(f"\n## {target} correctness and cardinality\n")
print("| scenario | allowed | denied | errors | allowed/s | expected tokens | oversub | oversub ratio | p50 ms | p99 ms | offered RPS |")
print("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
for name in ("burst-hotkey", "sustained-limit", "cardinality-hot", "cardinality-wide"):
    data = load(name)
    totals = data["totals"]
    enf = data["enforcement"]
    lat = data["latency"]
    print(
        f"| {name} | {totals['allowed']} | {totals['denied']} | {totals['errors']} | "
        f"{f2(data['allowed_per_second'])} | {f4(enf['expected_tokens'])} | "
        f"{f4(enf['oversubscription'])} | {f4(enf['oversubscription_ratio'])} | "
        f"{ms(lat['p50_ms'])} | {ms(lat['p99_ms'])} | {f2(data['requests_per_second'])} |"
    )

print(f"\n## {target} saturation\n")
print("| concurrency | offered RPS | allowed/s | errors | p50 ms | p99 ms | max ms |")
print("|---:|---:|---:|---:|---:|---:|---:|")
for concurrency in (1, 10, 50, 100, 200):
    name = f"saturation-c{concurrency}"
    data = load(name)
    totals = data["totals"]
    lat = data["latency"]
    print(
        f"| {concurrency} | {f2(data['requests_per_second'])} | {f2(data['allowed_per_second'])} | "
        f"{totals['errors']} | {ms(lat['p50_ms'])} | {ms(lat['p99_ms'])} | {ms(lat['max_ms'])} |"
    )
PY
