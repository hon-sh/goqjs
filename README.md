# goqjs

Minimal Go + [QuickJS](https://bellard.org/quickjs/) demo: one JS runtime, concurrent async jobs, real-time stdout.

## Setup

QuickJS `2026-06-04` is vendored under `third_party/quickjs/` (from Bellard's tarball).

## Run

```bash
go run ./cmd/goqjs -f examples/sleep.js 3 5 6
go run ./cmd/goqjs -f examples/fib.js 32 33 34
go run ./cmd/goqjs -f examples/fact.js 20000 20000
go run ./cmd/goqjs -e 'async function(c) { await resp.write(c, "hi"); }' 1 2
```

Exactly one of `-e` / `-f` is required: a JS **function expression** bound as `run`. Remaining args each start one concurrent `r.Run(arg)` (ints parsed when possible, else strings).

| example | role |
|---------|------|
| `examples/sleep.js` | async + `sleep` — I/O-style multiplexing |
| `examples/fib.js` | recursive fib — CPU-bound on one Runtime |
| `examples/fact.js` | BigInt factorial — CPU-bound on one Runtime |

## How it works

- **C/QuickJS owns the event loop** (`js_std_loop` from `quickjs-libc`).
- `runtime.New(ctx, run)` installs `sleep` / `resp`, binds the `run` function expression as the `Run` entrypoint, and keeps the loop alive via a wake pipe + `os.setReadHandler`.
- `r.Run(args...)` JSON-encodes args, wakes the loop thread, invokes that function (awaiting Promises), and blocks until settle.
- Context cancel clears the wake handler so `js_std_loop` can exit when idle; Go does not auto-cancel.
