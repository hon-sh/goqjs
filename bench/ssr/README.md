# SSR HTTP bench (Bun / Deno / goqjs)

Curated write-up: **[`../BENCH-ssr.md`](../BENCH-ssr.md)**.

Controlled React SSR workload — **no network fetch**:

1. `await sleep(delay)` — fake I/O
2. Build a **deterministic** `Array(n)` of items
3. `ReactDOMServer.renderToString` of a long list with **per-item conditionals**

Same Vite IIFE (`dist/server/ssr.js`) for **Bun, Deno, and goqjs** so React work is identical.

## Query

| param | default | meaning |
|-------|---------|---------|
| `n` | `200` | list length (clamped 1…10000) |
| `delay` | `10` | sleep ms before building data (0…5000) |

Example: `http://127.0.0.1:19200/?n=500&delay=20`

## Setup

```bash
cd bench/ssr
npm install
npm run build          # goqjs IIFE → dist/server/ssr.js
```

## Run one server

```bash
# Bun
PORT=19200 bun server_bun.js

# Deno (needs read permission for dist/)
deno serve --allow-read --host 127.0.0.1 --port 19200 server_deno.js

# goqjs (from repo root)
go build -o bench/.bin/bench-ssr ./bench/ssr
./bench/.bin/bench-ssr -c 2 -addr 127.0.0.1:19200 -ssr bench/ssr/dist/server/ssr.js
```

## Full comparison

```bash
make -C bench ssr
# DENO_PARALLEL=1 GOQJS_C=8 make -C bench ssr
# or: ./bench/ssr/run.sh
```

Order: bun → deno → goqjs `-c 1` → goqjs `-c $nproc`. Outputs under `bench/results/ssr-<stamp>.*` (gitignored). Promote a snapshot into [`../BENCH-ssr.md`](../BENCH-ssr.md) after review.

### Scenarios

| scenario | default | what it stresses |
|----------|---------|------------------|
| latency | `n=100 delay=5` c=1 | single-request cost |
| concurrency | `n=200 delay=20` c=50 | I/O multiplexing under load |
| render-heavy | `n=1500 delay=0` c=8 | React render CPU |

Env overrides: `LATENCY_N`, `LATENCY_DELAY`, `CONCUR_N`, `CONCUR_DELAY`, `RENDER_N`, `RENDER_DELAY`, `GOQJS_C`, `DENO_PARALLEL`, `PORT`.

## Layout

| path | role |
|------|------|
| `src/loadData.js` | sleep + `buildItems` |
| `src/App.jsx` | long list + conditionals |
| `src/render.jsx` | shared `render(query)` |
| `src/entry-goqjs.jsx` | sets `globalThis.__bench_render` |
| `dist/server/ssr.js` | Vite IIFE (Bun / Deno / goqjs all load this) |
| `server_bun.js` / `server_deno.js` | HTTP hosts (`eval` IIFE) |
| `handler.js` + `main.go` | goqjs pool host |
| `run.sh` | sequential orchestrator |

## Why not hn-ssr / live fetch?

- HN Firebase adds **network jitter** and API coupling.
- This bench keeps delay **deterministic** so Bun / Deno / goqjs compare event-loop + render, not WAN latency.
