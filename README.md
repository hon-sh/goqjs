# goqjs

Minimal Go + [QuickJS](https://bellard.org/quickjs/) demo: one JS runtime (or a pool), concurrent async jobs, host IO via stdlib.

See `docs/seed-1.md` (engine) and `docs/seed-2.md` (host / stdlib decisions).

## Setup

QuickJS `2026-06-04` is vendored under `third_party/quickjs/`.

## Run

```bash
go run ./cmd/goqjs -f examples/sleep.js 3 5 6
go run ./cmd/goqjs -c 2 -f examples/fib.js 32 33 34 35
go run ./cmd/goqjs -f examples/fact.js 20000 20000
go test ./runtime/... ./stdlib/... ./pool/... -count=1
```

`-c N` starts an N-wide Runtime pool (`goqjs/pool`). Exactly one of `-e` / `-f` supplies the JS **function expression** for `run`. Remaining args each start one concurrent `pool.Run(arg)`.

`cmd/goqjs` installs `goqjs/stdlib` console and a convenience `sleep` (`Promise` + `setTimeout`).

| example | role |
|---------|------|
| `examples/sleep.js` | async timer multiplexing |
| `examples/fib.js` | CPU-bound; use `-c 2` for parallel Runtimes |
| `examples/fact.js` | BigInt factorial — CPU-bound |

## How it works

- **C/QuickJS owns the event loop** (`js_std_loop`).
- Core boot (in Go) exposes `setTimeout` / `clearTimeout` and the invoke protocol — not `console` / `fetch` / `resp`.
- `stdlib.Install` injects reusable host APIs before the first `Run`; later injection panics.
- `pool.New` builds N workers; `Run` round-robins. Context cancel tears down loops when idle.
