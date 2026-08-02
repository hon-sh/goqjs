# goqjs

Minimal Go + [QuickJS](https://bellard.org/quickjs/) demo: one JS runtime, concurrent async jobs, real-time stdout.

## Setup

QuickJS `2026-06-04` is vendored under `third_party/quickjs/` (from Bellard's tarball).

## Run

```bash
cd _misc/goqjs
go run ./cmd/goqjs 3 5 6
```

Starts **one** QuickJS instance and three concurrent jobs (`c=3`, `c=5`, `c=6`). Each job:

```js
for (let i = 0; i < c; ++i) {
  await resp.write(c, String(i));
  await sleep(1000);
}
```

Writes are prefixed with the call's `c` (e.g. `[3]0`) and flushed immediately; interleaved output over ~6s is expected. Exit when all jobs finish.

```bash
go run ./cmd/goqjs 2
```

## How it works

- **C/QuickJS owns the event loop** (`js_std_loop` from `quickjs-libc`).
- `sleep(ms)` is `os.sleepAsync` — a Promise resolved by the QJS timer poll, so it does not block the JS thread.
- `resp.write` is a cgo-exported Go callback that prints to stdout and syncs immediately.
- Go locks one OS thread for the runtime; main waits until `js_std_loop` returns (idle: no pending jobs/timers).
