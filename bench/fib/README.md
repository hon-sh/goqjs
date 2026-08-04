# Fib HTTP bench: Bun / Deno / gohon-serve

Compares **request latency**, **concurrency**, and **CPU-heavy concurrent** load on the same recursive `fib(n)` handler.

Curated write-up: **[`../BENCH-fib.md`](../BENCH-fib.md)**.

## Requires

| tool | notes |
|------|--------|
| [oha](https://github.com/hatoo/oha) | on `PATH` (verified with **1.15.x**) |
| bun | verified with **1.3.x** |
| deno | **>= 2.x** required (`run.sh` checks); verified with **2.9.x** |
| go, curl | build `gohon-serve` / readiness probe |

`run.sh` prints `oha` / `bun` / `deno` versions into the result file and **exits if Deno major < 2**.

## Run (sequential — fairer on one machine)

```bash
make -C bench fib
# multi-worker Deno (closer to hon -c N on CPU-bound load):
DENO_PARALLEL=1 make -C bench fib
# or: ./bench/fib/run.sh
```

Order: **bun → deno → hon `-c 1` → hon `-c $nproc`**. One server at a time on `PORT` (default `19100`).

Output (under `bench/results/`, gitignored):

- `fib-<timestamp>.txt` — full log
- `fib-<timestamp>.md` — comparison tables
- `fib-<timestamp>.metrics/*.json` — raw oha JSON

Promote a snapshot into [`../BENCH-fib.md`](../BENCH-fib.md) after review.

Regenerate markdown:

```bash
python3 bench/report.py --from-txt bench/results/20260802-223605.txt \
  --out bench/results/20260802-223605.md
```

### Tunables (env)

| var | default | meaning |
|-----|---------|---------|
| `PORT` | `19100` | listen port |
| `HON_C` | `nproc` | hon pool size for the multi-worker pass |
| `DENO_PARALLEL` | `0` | `1` → `deno serve --parallel` |
| `LATENCY_N` | `20` | fib n for latency pass |
| `CONCUR_N` | `20` | fib n for concurrency pass |
| `CPU_N` | `32` | fib n for CPU-parallel pass |

### Scenarios (per runtime)

1. **latency** — `oha -n 200 -c 1 ?n=$LATENCY_N`
2. **concurrency** — `oha -z 5s -c 50 ?n=$CONCUR_N`
3. **cpu-parallel** — `oha -n 40 -c 8 ?n=$CPU_N`

## Layout

| file | role |
|------|------|
| `fib.js` | shared `fib` / `parseN` for Bun & Deno |
| `server_bun.js` | `Bun.serve` |
| `server_deno.js` | default export for `deno serve` |
| `serve-fib.js` | gohon-serve handler (keep in sync with `fib.js`) |
| `run.sh` | sequential orchestrator + version gate |

Demo copy for `gohon-serve` CLI docs: `examples/serve-fib.js` (same algorithm).

## Deno notes

- Prefer **`deno serve --host 127.0.0.1 --port $PORT`** (what `run.sh` uses).
- Handler stays **synchronous** and returns `new Response(string)` for Deno 2’s serve hot path.
- For multi-core CPU passes, set `DENO_PARALLEL=1`; default single worker is closer to hon `-c 1`.

## Notes

- Stresses **JS CPU + serve wiring**, not routers.
- Warmup: one `?n=5` before each target’s oha group.
