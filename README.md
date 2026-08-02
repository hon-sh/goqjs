# goqjs

Minimal Go + [QuickJS](https://bellard.org/quickjs/) demo: one JS runtime (or a pool), concurrent async jobs, host IO via stdlib.

See `docs/seed-1.md` (engine) and `docs/seed-2.md` (host / stdlib decisions).

## Setup

QuickJS `2026-06-04` is vendored under `third_party/quickjs/`.

## How it works

- **C/QuickJS owns the event loop** (`js_std_loop`).
- Core boot (in Go) exposes timers + invoke — not `console` / `fetch` / HTTP.
- `stdlib.Install` injects host APIs before the first `Run`; later injection panics.
- `pool.New` builds N workers; serve wraps user `run(req,res)` and binds each request by id.

## Examples

### CLI (`goqjs`)

```bash
go run ./cmd/goqjs -f examples/sleep.js 3 5 6
go run ./cmd/goqjs -c 2 -f examples/fib.js 32 33 34 35
make test
```

`-c N` starts an N-wide Runtime pool. Exactly one of `-e` / `-f` supplies the JS **function expression** for `run`. Remaining args each start one concurrent `pool.Run(arg)`.

`cmd/goqjs` installs stdlib `console` and a convenience `sleep`.

| example | role |
|---------|------|
| `examples/sleep.js` | async timer multiplexing |
| `examples/fib.js` | CPU-bound; use `-c 2` |
| `examples/fact.js` | BigInt factorial — CPU-bound |

### HTTP (`goqjs-serve`)

```bash
go run ./cmd/goqjs-serve -f examples/serve-hello.js
go run ./cmd/goqjs-serve -c 2 -f examples/serve-sleep.js -addr :8080
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

Minimal Hacker News SSR (Firebase API) with a Go + goqjs host that embeds `dist/`:

```bash
cd examples/hn-ssr && npm install
make hn-ssr                          # from repo root
./examples/hn-ssr/hn-ssr -addr :8080
# or: cd examples/hn-ssr && make prod && ./hn-ssr -addr :8080
```

Dev with Vite/Node: `cd examples/hn-ssr && npm run dev`.

See [`examples/hn-ssr/README.md`](examples/hn-ssr/README.md).
