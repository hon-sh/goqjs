# goqjs SSR HTTP bench notes

Curated write-up for the React SSR bench (`bench/ssr/`).  
Workload: `sleep(delay)` → deterministic `Array(n)` → `renderToString` long list (per-item conditionals).  
Same Vite IIFE for Bun / Deno / goqjs.

How to reproduce: see [`ssr/README.md`](./ssr/README.md) (`make -C bench ssr`).

Raw oha outputs land in `results/ssr-<stamp>.*` (gitignored). After a meaningful run, paste tables + analysis here (same shape as [`BENCH-fib.md`](./BENCH-fib.md)).

---

## Environment

| key | value |
|-----|-------|
| _(pending first curated run)_ | |

---

## Results

### Latency (low concurrency)

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| | | | | | |

### Concurrency / throughput

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| | | | | | |

### Render-heavy (long list, little/no sleep)

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| | | | | | |

---

## Analysis

_(Fill after first run.)_
