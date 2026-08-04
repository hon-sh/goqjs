# hon SSR HTTP bench notes

Controlled React SSR: `sleep` + fixed `Array(n)` + `renderToString` (Bun / Deno / hon, same IIFE).

How to reproduce: see [`ssr/README.md`](./ssr/README.md) (`make -C bench ssr`).

## Curated snapshots

| file | list size focus | source run |
|------|-----------------|------------|
| [`bench-ssr-N1500.md`](./bench-ssr-N1500.md) | render-heavy **n=1500** | `results/ssr-20260803-104703` |
| [`bench-ssr-N100.md`](./bench-ssr-N100.md) | all scenarios **n=100** | `results/ssr-20260803-105302` |

```bash
# N=1500 render-heavy (default RENDER_N)
make -C bench ssr

# N=100 across latency / concurrency / render-heavy
LATENCY_N=100 CONCUR_N=100 RENDER_N=100 make -C bench ssr
```
