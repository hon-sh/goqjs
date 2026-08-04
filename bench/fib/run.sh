#!/usr/bin/env bash
# Sequential fib HTTP bench: bun → deno → hon (-c 1) → hon (-c N).
# Writes results/fib-<stamp>.txt (full log) + results/fib-<stamp>.md (comparison tables).
# Requires: oha, bun, deno (>=2), go, python3.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FIB="$(cd "$(dirname "$0")" && pwd)"
PORT="${PORT:-19100}"
HOST="127.0.0.1"
BASE="http://${HOST}:${PORT}/fib"
OUT_DIR="${ROOT}/bench/results"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT_TXT="${OUT_DIR}/fib-${STAMP}.txt"
OUT_MD="${OUT_DIR}/fib-${STAMP}.md"
METRICS_DIR="${OUT_DIR}/fib-${STAMP}.metrics"
NPROC="$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 2)"
HON_C="${HON_C:-$NPROC}"
DENO_PARALLEL="${DENO_PARALLEL:-0}"

LATENCY_N="${LATENCY_N:-20}"
CONCUR_N="${CONCUR_N:-20}"
CPU_N="${CPU_N:-32}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing dependency: $1" >&2
    exit 1
  }
}

major_of() {
  echo "$1" | sed -n 's/[^0-9]*\([0-9][0-9]*\).*/\1/p' | head -1
}

need oha
need bun
need deno
need go
need curl
need python3

OHA_VER="$(oha --version 2>&1 | head -1)"
BUN_VER="$(bun --version 2>&1 | head -1)"
DENO_LINE="$(deno --version 2>&1 | head -1)"
DENO_VER_NUM="$(deno eval -p 'Deno.version.deno' 2>/dev/null || true)"
if [[ -z "${DENO_VER_NUM}" ]]; then
  DENO_VER_NUM="$(echo "$DENO_LINE" | sed -n 's/^deno \([0-9.]*\).*/\1/p')"
fi
DENO_MAJOR="$(major_of "${DENO_VER_NUM:-$DENO_LINE}")"

if [[ -z "${DENO_MAJOR}" || "${DENO_MAJOR}" -lt 2 ]]; then
  echo "need Deno >= 2.x (got: ${DENO_LINE})" >&2
  exit 1
fi

mkdir -p "$OUT_DIR" "$METRICS_DIR"
exec > >(tee "$OUT_TXT")
exec 2>&1

echo "=== hon fib bench ${STAMP} ==="
echo "host=${HOST} port=${PORT} hon_c=${HON_C} nproc=${NPROC} deno_parallel=${DENO_PARALLEL}"
echo "oha=${OHA_VER}"
echo "bun=${BUN_VER}"
echo "deno=${DENO_LINE}"
echo "deno.version=${DENO_VER_NUM}"
echo

BIN="${ROOT}/bench/.bin/gohon-serve"
mkdir -p "${ROOT}/bench/.bin"
echo "building gohon-serve → ${BIN}"
(cd "$ROOT" && go build -o "$BIN" ./cmd/gohon-serve)
echo

SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    # Deno (and others) can ignore/delay SIGTERM; never block the bench forever.
    local i
    for i in $(seq 1 30); do
      if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    if kill -0 "$SERVER_PID" 2>/dev/null; then
      echo ">>> cleanup: ${SERVER_PID} still alive after TERM, sending KILL" >&2
      kill -9 "$SERVER_PID" 2>/dev/null || true
    fi
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  SERVER_PID=""
}
trap cleanup EXIT

wait_ready() {
  local i
  for i in $(seq 1 50); do
    if curl -fsS "${BASE}?n=1" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  echo "server did not become ready on ${BASE}" >&2
  return 1
}

start_bg() {
  local name=$1
  shift
  cleanup
  echo ">>> start ${name}: $*"
  env PORT="$PORT" "$@" &
  SERVER_PID=$!
  wait_ready
  curl -fsS "${BASE}?n=5" >/dev/null
}

# One oha run: JSON for tables + short text summary in the log.
run_oha() {
  local runtime=$1
  local scenario=$2
  local label=$3
  local url=$4
  shift 4
  local jf="${METRICS_DIR}/${runtime}.${scenario}.json"

  echo
  echo "----- ${label} -----"
  echo "cmd: oha $* ${url}"
  if oha --output-format json -o "$jf" "$@" "$url"; then
    python3 - "$jf" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
s = d["summary"]
m = d.get("metrics", {}).get("latency_ms", {})
print(
    f"  success={s['successRate']*100:.2f}%  "
    f"rps={s['requestsPerSec']:.1f}  "
    f"avg={m.get('mean', s['average']*1000):.3f}ms  "
    f"p50={m.get('p50', float('nan')):.3f}ms  "
    f"p95={m.get('p95', float('nan')):.3f}ms  "
    f"p99={m.get('p99', float('nan')):.3f}ms  "
    f"max={m.get('max', s['slowest']*1000):.3f}ms"
)
print(f"  json: {p}")
PY
  else
    echo "  oha failed" >&2
  fi
}

bench_target() {
  local name=$1
  shift
  start_bg "$name" "$@"

  run_oha "$name" latency \
    "${name} / latency  n=${LATENCY_N} c=1 nreq=200" \
    "${BASE}?n=${LATENCY_N}" -n 200 -c 1

  run_oha "$name" concurrency \
    "${name} / concurrency  n=${CONCUR_N} c=50 z=5s" \
    "${BASE}?n=${CONCUR_N}" -z 5s -c 50

  run_oha "$name" cpu-parallel \
    "${name} / cpu-parallel  n=${CPU_N} c=8 nreq=40" \
    "${BASE}?n=${CPU_N}" -n 40 -c 8

  cleanup
  echo
  echo ">>> stopped ${name}"
  echo
}

bench_target "bun" bun "${FIB}/server_bun.js"

DENO_ARGS=(serve --host "$HOST" --port "$PORT")
if [[ "${DENO_PARALLEL}" == "1" ]]; then
  DENO_ARGS=(serve --parallel --host "$HOST" --port "$PORT")
fi
bench_target "deno" deno "${DENO_ARGS[@]}" "${FIB}/server_deno.js"

bench_target "hon-c1" "$BIN" -c 1 -addr "${HOST}:${PORT}" -f "${FIB}/serve-fib.js"
bench_target "hon-c${HON_C}" "$BIN" -c "$HON_C" -addr "${HOST}:${PORT}" -f "${FIB}/serve-fib.js"

python3 "${ROOT}/bench/report.py" \
  --metrics-dir "$METRICS_DIR" \
  --out "$OUT_MD" \
  --title "Fib HTTP bench ${STAMP}" \
  --meta "host=${HOST}" \
  --meta "port=${PORT}" \
  --meta "hon_c=${HON_C}" \
  --meta "nproc=${NPROC}" \
  --meta "deno_parallel=${DENO_PARALLEL}" \
  --meta "oha=${OHA_VER}" \
  --meta "bun=${BUN_VER}" \
  --meta "deno=${DENO_LINE}" \
  --meta "deno.version=${DENO_VER_NUM}" \
  --meta "latency_n=${LATENCY_N}" \
  --meta "concur_n=${CONCUR_N}" \
  --meta "cpu_n=${CPU_N}"

echo "=== done ==="
echo "  log: ${OUT_TXT}"
echo "  md:  ${OUT_MD}"
echo "  curated write-up (after review): bench/BENCH-fib.md"
