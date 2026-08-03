#!/usr/bin/env bash
# Sequential SSR bench: bun → deno → goqjs (-c 1) → goqjs (-c N).
# Workload: sleep(delay) + build fixed Array(n) + React renderToString (long list).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SSR="$(cd "$(dirname "$0")" && pwd)"
PORT="${PORT:-19200}"
HOST="127.0.0.1"
BASE="http://${HOST}:${PORT}/"
OUT_DIR="${ROOT}/bench/results"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT_TXT="${OUT_DIR}/ssr-${STAMP}.txt"
OUT_MD="${OUT_DIR}/ssr-${STAMP}.md"
METRICS_DIR="${OUT_DIR}/ssr-${STAMP}.metrics"
NPROC="$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 2)"
GOQJS_C="${GOQJS_C:-$NPROC}"
DENO_PARALLEL="${DENO_PARALLEL:-0}"

# Query shape: n=list length, delay=fake I/O ms
LATENCY_N="${LATENCY_N:-100}"
LATENCY_DELAY="${LATENCY_DELAY:-5}"
CONCUR_N="${CONCUR_N:-200}"
CONCUR_DELAY="${CONCUR_DELAY:-20}"
RENDER_N="${RENDER_N:-1500}"
RENDER_DELAY="${RENDER_DELAY:-0}"

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
need npm

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

echo "=== goqjs SSR bench ${STAMP} ==="
echo "host=${HOST} port=${PORT} goqjs_c=${GOQJS_C} nproc=${NPROC} deno_parallel=${DENO_PARALLEL}"
echo "oha=${OHA_VER}"
echo "bun=${BUN_VER}"
echo "deno=${DENO_LINE}"
echo "deno.version=${DENO_VER_NUM}"
echo "latency: n=${LATENCY_N} delay=${LATENCY_DELAY}"
echo "concurrency: n=${CONCUR_N} delay=${CONCUR_DELAY}"
echo "render-heavy: n=${RENDER_N} delay=${RENDER_DELAY}"
echo

echo "npm install + build (goqjs IIFE)…"
(cd "$SSR" && npm install --silent && npm run build)
echo

BIN="${ROOT}/bench/.bin/bench-ssr"
mkdir -p "${ROOT}/bench/.bin"
echo "building bench-ssr → ${BIN}"
(cd "$ROOT" && go build -o "$BIN" ./bench/ssr)
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

qs() {
  local n=$1 delay=$2
  echo "n=${n}&delay=${delay}"
}

wait_ready() {
  local i
  local url="${BASE}?$(qs 5 0)"
  for i in $(seq 1 80); do
    if curl -fsS "$url" >/dev/null 2>&1; then
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
  curl -fsS "${BASE}?$(qs 20 0)" >/dev/null
}

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
    "${name} / latency  n=${LATENCY_N} delay=${LATENCY_DELAY} c=1 nreq=100" \
    "${BASE}?$(qs "$LATENCY_N" "$LATENCY_DELAY")" -n 100 -c 1

  run_oha "$name" concurrency \
    "${name} / concurrency  n=${CONCUR_N} delay=${CONCUR_DELAY} c=50 z=5s" \
    "${BASE}?$(qs "$CONCUR_N" "$CONCUR_DELAY")" -z 5s -c 50

  run_oha "$name" render-heavy \
    "${name} / render-heavy  n=${RENDER_N} delay=${RENDER_DELAY} c=8 nreq=40" \
    "${BASE}?$(qs "$RENDER_N" "$RENDER_DELAY")" -n 40 -c 8

  cleanup
  echo
  echo ">>> stopped ${name}"
  echo
}

bench_target "bun" bun "${SSR}/server_bun.js"

DENO_ARGS=(serve --allow-read --host "$HOST" --port "$PORT")
if [[ "${DENO_PARALLEL}" == "1" ]]; then
  DENO_ARGS=(serve --parallel --allow-read --host "$HOST" --port "$PORT")
fi
bench_target "deno" deno "${DENO_ARGS[@]}" "${SSR}/server_deno.js"

bench_target "goqjs-c1" "$BIN" -c 1 -addr "${HOST}:${PORT}" -ssr "${SSR}/dist/server/ssr.js"
bench_target "goqjs-c${GOQJS_C}" "$BIN" -c "$GOQJS_C" -addr "${HOST}:${PORT}" -ssr "${SSR}/dist/server/ssr.js"

python3 "${ROOT}/bench/report.py" \
  --metrics-dir "$METRICS_DIR" \
  --out "$OUT_MD" \
  --title "SSR HTTP bench ${STAMP}" \
  --meta "host=${HOST}" \
  --meta "port=${PORT}" \
  --meta "goqjs_c=${GOQJS_C}" \
  --meta "nproc=${NPROC}" \
  --meta "deno_parallel=${DENO_PARALLEL}" \
  --meta "oha=${OHA_VER}" \
  --meta "bun=${BUN_VER}" \
  --meta "deno=${DENO_LINE}" \
  --meta "deno.version=${DENO_VER_NUM}" \
  --meta "latency_n=${LATENCY_N}" \
  --meta "latency_delay=${LATENCY_DELAY}" \
  --meta "concur_n=${CONCUR_N}" \
  --meta "concur_delay=${CONCUR_DELAY}" \
  --meta "render_n=${RENDER_N}" \
  --meta "render_delay=${RENDER_DELAY}"

echo "=== done ==="
echo "  log: ${OUT_TXT}"
echo "  md:  ${OUT_MD}"
echo "  curated write-up (after review): bench/BENCH-ssr.md"
