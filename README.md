# hon

`hon` is a Go module that embeds a [QuickJS](https://bellard.org/quickjs/)-backed JavaScript runtime in-process. Go owns the host (scheduling, networking, app logic); QuickJS runs the event loop (`js_std_loop`). You can pool multiple runtimes and inject host APIs (`console`, `fetch`, HTTP helpers, or your own) via `stdlib` / `Inject*`.

**Good fit:** embedding JS inside a Go service — React SSR, plugins, or per-request scripts — especially workloads that mix lots of `await` I/O with moderate render/CPU work.

**Not a fit:** replacing Node/Bun as a general JS platform, or expecting one isolate to crush heavy CPU-bound jobs (scale those with `-c N` pools, or keep the heavy work in Go).

- Module: [`github.com/hon-go/hon`](https://github.com/hon-go/hon)
- CLIs: `gohon`, `gohon-serve` (the bare name `hon` is reserved for a Zig + QuickJS sibling)

See `docs/seed-1.md` (engine) and `docs/seed-2.md` (host / stdlib).

## Setup

QuickJS `2026-06-04` is vendored under `third_party/quickjs/`.

## How it works

- **C/QuickJS owns the event loop** (`js_std_loop`).
- Core boot (in Go) exposes timers + invoke — not `console` / `fetch` / HTTP.
- `stdlib.Install` injects host APIs before the first `Run`; later injection panics.
- `pool.New` builds N workers; serve wraps user `run(req,res)` and binds each request by id.

## Examples

### CLI (`gohon`)

```bash
go run ./cmd/gohon -f examples/sleep.js 3 5 6
go run ./cmd/gohon -c 2 -f examples/fib.js 32 33 34 35
make test
```

`-c N` starts an N-wide Runtime pool. Exactly one of `-e` / `-f` supplies the JS **function expression** for `run`. Remaining args each start one concurrent `pool.Run(arg)`.

`cmd/gohon` installs stdlib `console` and a convenience `sleep`.

| example | role |
|---------|------|
| `examples/sleep.js` | async timer multiplexing |
| `examples/fib.js` | CPU-bound; use `-c 2` |
| `examples/fact.js` | BigInt factorial — CPU-bound |

### HTTP (`gohon-serve`)

```bash
go run ./cmd/gohon-serve -f examples/serve-hello.js
go run ./cmd/gohon-serve -c 2 -f examples/serve-sleep.js -addr :8080
curl -s localhost:8080/hi
curl -s 'localhost:8080/fib?n=20'   # with serve-fib.js
```

JS entry is `async function(req, res) { ... }`. Go locates the response via `req_id` (not raw fds).

| example | role |
|---------|------|
| `examples/serve-hello.js` | method/path echo |
| `examples/serve-sleep.js` | async sleep then chunked write |
| `examples/serve-fib.js` | CPU fib from `?n=` |

### HN SSR (Vite + React)

Minimal Hacker News SSR demo lives in a separate repo: [hon-go/hn-ssr](https://github.com/hon-go/hn-ssr).
