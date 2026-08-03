# HTTP benches: Bun / Deno / goqjs

| bench | workload | how to run | curated notes |
|-------|----------|------------|---------------|
| **[fib](./fib/)** | recursive `fib(n)` — JS engine / CPU | `make -C bench fib` | [`BENCH-fib.md`](./BENCH-fib.md) |
| **[ssr](./ssr/)** | sleep + fixed Array + React long-list SSR | `make -C bench ssr` | [`BENCH-ssr.md`](./BENCH-ssr.md) |

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

After a meaningful run, promote numbers + analysis into `BENCH-fib.md` or `BENCH-ssr.md`.
