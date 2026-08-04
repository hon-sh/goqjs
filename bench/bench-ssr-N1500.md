# hon SSR bench — N=1500 (render-heavy list size)

Snapshot from [`results/ssr-20260803-104703`](./results/ssr-20260803-104703.md) (machine: 16 logical CPUs).  
Workload: `sleep(delay)` → deterministic `Array(n)` → React `renderToString` long list.  
Same Vite IIFE for Bun / Deno / hon. Deno: `deno_parallel=0`.

How to reproduce: `RENDER_N=1500 make -C bench ssr` (see [`ssr/README.md`](./ssr/README.md)).  
Sibling run: [`bench-ssr-N100.md`](./bench-ssr-N100.md).

---

## Environment

| key | value |
|-----|-------|
| bun | 1.3.14 |
| deno | 2.9.4 |
| deno_parallel | 0 |
| hon_c (multi) | 16 |
| nproc | 16 |
| oha | 1.15.0 |
| latency | `n=100 delay=5` (`oha -n 100 -c 1`) |
| concurrency | `n=200 delay=20` (`oha -z 5s -c 50`) |
| render-heavy | `n=1500 delay=0` (`oha -n 40 -c 8`) |

---

## Results

### Latency (low concurrency)

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 135.4 | 7.371 | 7.036 | 9.028 | 17.56 |
| deno | 118.3 | 8.433 | 8.583 | 9.562 | 11.82 |
| hon-c1 | 58.7 | 17.03 | 17.09 | 19.50 | 21.24 |
| hon-c16 | 56.9 | 17.56 | 17.61 | 19.77 | 21.05 |

### Concurrency / throughput

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 1,063 | 47.21 | 45.87 | 60.33 | 69.45 |
| deno | 659.7 | 76.05 | 75.88 | 93.19 | 113.2 |
| hon-c1 | 68.3 | 784.0 | 844.0 | 886.1 | 887.5 |
| hon-c16 | 411.7 | 122.8 | 123.9 | 145.2 | 152.7 |

### Render-heavy (n=1500, delay=0)

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 142.8 | 50.58 | 53.85 | 61.80 | 62.43 |
| deno | 135.0 | 58.78 | 59.02 | 63.29 | 63.32 |
| hon-c1 | 7.5 | 979.1 | 958.3 | 1371.6 | 1491.4 |
| hon-c16 | 54.0 | 145.0 | 144.6 | 153.9 | 156.6 |

---

## Analysis

1. **Latency**：hon ≈ **2.3×** bun（17ms vs 7ms）；有 `delay=5` 托底，比 fib 的 5–7× 温和。c16 对单请求几乎无帮助。
2. **Concurrency**：c1 很差（68 RPS）；c16 ≈ bun 的 **39%**、deno 的 **62%**。池化有效，但每请求仍有列表构建 + React CPU，不是纯 I/O。
3. **Render-heavy（N=1500）**：c1 ≈ **19×** 慢于 bun；c16 ≈ **2.6×**。大列表无 sleep = QuickJS 解释器硬扛 `renderToString`。

Takeaway：N=1500 是压力档，暴露引擎差距；产品形态若更接近「短列表 + 等待」，看 N=100 对照。
