# hon fib HTTP bench notes

Snapshot from `results/20260802-223605` (machine: 16 logical CPUs).  
Workload: recursive `fib(n)` over HTTP (`/fib?n=`), same algorithm for Bun / Deno / gohon-serve.  
Deno ran with `deno_parallel=0` (default single `deno serve`, no `--parallel`).

How to reproduce: see [`fib/README.md`](./fib/README.md) (`make -C bench fib`).

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
| latency `n` | 20 (`oha -n 200 -c 1`) |
| concurrency `n` | 20 (`oha -z 5s -c 50`) |
| cpu-parallel `n` | 32 (`oha -n 40 -c 8`) |

---

## Results

### Latency (low concurrency)

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 7,286 | 0.133 | 0.108 | 0.187 | 1.063 |
| deno | 5,702 | 0.170 | 0.144 | 0.319 | 0.420 |
| hon-c1 | 1,043 | 0.948 | 0.901 | 1.242 | 1.698 |
| hon-c16 | 1,100 | 0.900 | 0.881 | 1.064 | 1.461 |

### Concurrency / throughput

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 14,046 | 3.557 | 3.290 | 4.661 | 6.470 |
| deno | 9,899 | 5.047 | 4.729 | 6.128 | 7.373 |
| hon-c1 | 1,309 | 38.26 | 37.69 | 41.71 | 44.02 |
| hon-c16 | 8,258 | 6.048 | 3.622 | 18.92 | 46.86 |

### CPU-parallel (heavy fib)

| runtime | RPS | avg ms | p50 ms | p95 ms | p99 ms |
|---------|-----|--------|--------|--------|--------|
| bun | 62.2 | 117.2 | 126.5 | 258.5 | 274.3 |
| deno | 38.4 | 190.8 | 208.0 | 245.8 | 272.3 |
| hon-c1 | 4.6 | 1732.6 | 1727.6 | 1768.0 | 1768.0 |
| hon-c16 | 31.3 | 252.9 | 249.2 | 267.8 | 271.0 |

---

## Analysis

### What the numbers imply

1. **Latency**：hon 单请求约 **5–7×** 慢于 bun/deno；**c16 几乎无增益**（1043 → 1100 RPS）。说明瓶颈在「单次请求成本」，不是缺 worker。
2. **Concurrency**：c1 约 **1.3k RPS**；c16 约 **8.3k**，接近 deno（9.9k），仍落后 bun（14k）。多 Runtime 主要抬的是吞吐。
3. **CPU-parallel**：c1 约 **4.6 RPS / ~1.7s**；c16 约 **31 RPS**，接近**未开 parallel 的 deno（38）**，约为 bun 一半。

「看起来要 c16 才和 bun/deno 同一水平」——对**吞吐 / CPU 并发墙钟**大致成立；对**单请求 latency** 并不成立（c16 也追不上）。

### 原因优先级

**1. JS 引擎（主因之一）**

- hon = QuickJS（解释器）；Deno = V8；Bun ≈ JavaScriptCore。
- 递归 `fib` 放大引擎差距。
- 证据：latency 已差数倍（与并发无关）；cpu-parallel 上 **hon-c1 vs deno≈8×**，而本次 deno 也是单 serve 进程。

**2. 线程 / 进程模型**

- hon：每个 Runtime **一条** `LockOSThread` + `js_std_loop`；CPU 工作在同一 isolate 上串行。`-c N` 才是 N 路并行（见 `docs/seed-1.md` / `seed-2.md`）。
- **Bun（本次 `server_bun.js`）**：**没有**把每个 `fetch` 自动分到多条 JS 线程。`Bun.serve` 的 handler 默认仍在 **单个 JSC isolate** 上跑。多核要自己上 `Worker` 或多进程（`reusePort` / `cluster`）。bun 更快，主要来自 **JSC + 原生 HTTP 栈（uWebSockets 等）**，不是「内置请求级 JS 线程池」。
- **Deno（本次 `deno_parallel=0`）**：未开 `--parallel`，同样是单 serve 进程；仍远快于 hon-c1 → 主要是 **V8 单线程算得快**。

因此 cpu-parallel 上 bun > deno ≫ hon-c1，更应读成 **引擎 + 原生 serve 路径**，而不是「bun 默认 16 线程跑 fib」。hon-c16 才是显式多 isolate；和默认 bun/deno 不是同一并行模型。

**3. Go 侧锁（次要）**

- `gohon-serve` 的 `sessionStore.mu` 只保护 `req_id → ResponseWriter` 短临界区；相对 1.7s 级 fib，可忽略。
- 真正的串行点是 **「同一 Runtime 上所有 `Run` 进同一 JS loop」**，不是这把 mutex。

**4. 桥接固定开销（latency 更明显）**

每请求大致：HTTP goroutine → `pool.Run` → wakeup/cgo → JSON meta → 包装 `run(req,res)` → `httpWrite`。  
`fib(20)` 很轻时，桥接占比高；`fib(32)` 时引擎 + 单线程占主导。

### 和设计目标对齐

hon 刻意走 **「Go 宿主 + 单 JS Runtime 事件环」**（类 mini-Node）：

- 擅长：**I/O 等待时多路复用**（同 loop 上多个 async job）
- 不擅长：**单 isolate 硬扛 CPU**；CPU 要靠 **×N Runtime**，且单核上的 QJS 不会变成 V8

因此 bench 用纯 CPU fib 会「难看」是预期内的；它量的是引擎 + 池化，不是 SSR 里 `await fetch` 那种 I/O 形态。

### 后续可对比的实验

- `DENO_PARALLEL=1 make -C bench fib`：deno 多 worker 后，与 hon-c16 是否仍接近。
- **SSR bench**（[`ssr/`](./ssr/) → [`BENCH-ssr.md`](./BENCH-ssr.md)）：sleep + React 长列表，更贴近 I/O 复用路径。
- 固定 `-c` 扫一遍（2/4/8/16）画吞吐曲线，找池大小收益拐点。

---

## Takeaway

| 问题 | 简答 |
|------|------|
| 是引擎吗？ | **是**，QJS 对递归 fib 明显慢于 V8/JSC。 |
| 是 bun/deno「内置多线程跑 JS」吗？ | **基本不是**（本次配置）。bun/deno 默认都是单 isolate 跑 handler；bun 快更多来自 JSC + 原生 HTTP。多核要各自显式开 Worker / `--parallel` / 多进程。 |
| 是 Go 单点锁吗？ | **基本不是**；串行来自单 Runtime JS 线程。 |
| 为何要 c16？ | 用多慢实例堆吞吐，逼近「一个快引擎」的墙钟，不是单请求变快。 |
