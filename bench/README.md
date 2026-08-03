# HTTP benches: Bun / Deno / goqjs

| bench | workload | how to run | curated notes |
|-------|----------|------------|---------------|
| **[fib](./fib/)** | recursive `fib(n)` — JS engine / CPU | `make -C bench fib` | [`BENCH-fib.md`](./BENCH-fib.md) |
| **[ssr](./ssr/)** | sleep + fixed Array + React long-list SSR | `make -C bench ssr` | [`BENCH-ssr.md`](./BENCH-ssr.md) · [`N100`](./bench-ssr-N100.md) · [`N1500`](./bench-ssr-N1500.md) |

```bash
make -C bench          # default: fib
make -C bench fib
make -C bench ssr
```

Prefer **ssr** for I/O multiplexing / React render. **fib** amplifies QuickJS vs V8/JSC JIT — useful for engine comparison, not a stand-in for SSR.

## Shared

| path | role |
|------|------|
| `report.py` | oha JSON / text log → comparison markdown tables |
| `results/` | raw run outputs (gitignored): `fib-<stamp>.*`, `ssr-<stamp>.*` |
| `.bin/` | local `goqjs-serve` / `bench-ssr` binaries (gitignored) |

```bash
# regenerate tables from a metrics dir or text log
python3 bench/report.py --metrics-dir bench/results/fib-<stamp>.metrics \
  --out bench/results/fib-<stamp>.md
```

After a meaningful run, promote numbers + analysis into `BENCH-fib.md`, or SSR snapshots like `bench-ssr-N100.md` / `bench-ssr-N1500.md`.

## 结论（简要）

goqjs 追不上 bun/deno，主因是 **QuickJS 无 JIT**，不是 Go 侧一把大锁，也不是 bun/deno 默认就把每个请求丢到多条 JS 线程。单请求变快要靠引擎；多核吞吐要靠 **`-c N` 多 Runtime**。

**fib** 把这个差距放到最大：递归纯 CPU 极利好 V8/JSC。单请求大约慢一个数量级；开到 c16 才能在墙钟上摸到「未开 parallel 的 deno」附近，仍明显落后 bun。适合测引擎，不适合代表 SSR。

**SSR** 更接近产品路径。有假 I/O（sleep）时，单请求大约只慢 **两倍出头**（例如 ~16ms vs ~7ms），比 fib 温和得多。池化仍然关键：c1 在高并发下很容易塌；c16 可以把吞吐拉到 deno 的六七成、bun 的四成左右。真正难看的是 **大列表、零等待**（N=1500）：c1 接近慢二十倍，c16 也大约慢两三倍、绝对延迟上百毫秒。把列表降到 N=100 后，c16 渲染大约 **12ms**、六百 RPS 量级——相对 bun 仍慢，但绝对开销已经「能用」。

一句话：goqjs 适合 **Go 宿主 + 中等 React 渲染 + 大量 await I/O**，尖峰用池；不适合指望单 isolate 硬扛超大 `renderToString`，更别用 fib 当 SSR 成绩单。
