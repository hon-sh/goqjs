# hon SSR bench — N=100

Snapshot from [`results/ssr-20260803-105302`](./results/ssr-20260803-105302.md) (machine: 16 logical CPUs).  
Workload: `sleep(delay)` → deterministic `Array(n)` → React `renderToString`.  
Same Vite IIFE for Bun / Deno / hon. Deno: `deno_parallel=0`.  
List size **n=100** for latency / concurrency / render-heavy.

How to reproduce: `LATENCY_N=100 CONCUR_N=100 RENDER_N=100 make -C bench ssr`.  
Sibling (heavy list): [`bench-ssr-N1500.md`](./bench-ssr-N1500.md).

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
| concurrency | `n=100 delay=20` (`oha -z 5s -c 50`) |
| render-heavy | `n=100 delay=0` (`oha -n 40 -c 8`) |

---

## Results

### Latency (low concurrency)

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 142.3 | 7.011 | 6.847 | 8.185 | 13.75 |
| deno | 124.6 | 8.008 | 7.995 | 8.858 | 10.04 |
| hon-c1 | 60.9 | 16.39 | 16.25 | 17.87 | 19.98 |
| hon-c16 | 59.8 | 16.72 | 16.31 | 18.72 | 19.34 |

### Concurrency / throughput

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 1,615 | 31.04 | 27.28 | 45.05 | 74.55 |
| deno | 973.6 | 51.65 | 51.76 | 55.73 | 70.56 |
| hon-c1 | 108.1 | 484.1 | 499.6 | 553.9 | 563.9 |
| hon-c16 | 680.6 | 73.97 | 75.67 | 88.60 | 93.51 |

### Render-heavy (n=100, delay=0)

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 1,578 | 4.437 | 4.106 | 6.194 | 6.207 |
| deno | 1,107 | 6.828 | 6.783 | 8.231 | 8.264 |
| hon-c1 | 55.8 | 135.7 | 90.31 | 343.7 | 357.1 |
| hon-c16 | 601.5 | 12.41 | 12.55 | 14.41 | 14.44 |

---

## Analysis

1. **Latency**：仍约 **2.3×** bun（16ms vs 7ms），与 N=1500 档的 latency 几乎同级（latency 本就用 n=100）。
2. **Concurrency**：c16 ≈ bun 的 **42%**、deno 的 **70%**（N=1500 时为 39% / 62%）。列表变短后，池化吞吐更接近 deno。
3. **Render-heavy**：相对 N=1500 改善最大——c1 从 7.5 → **56 RPS**；c16 从 54 → **602 RPS**（≈ bun 的 **38%**，仍约 **2.8×** 慢于 bun 的单请求成本，但绝对延迟 12ms 可用得多）。

对照 N=1500：缩小列表不能消灭引擎差，但能把 hon-c16 的墙钟从「百毫秒级大页」拉回「十毫秒级短页」。

Note：concurrency 阶段偶发 `connection reset` / `broken pipe`（oha 仍报 100% success）；写入时客户端已断开，不影响上表量级判断。
