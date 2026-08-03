# Minimal Hacker News SSR (Vite + React)

Demo for **goqjs** React SSR. Default data path uses the public
[Hacker News Firebase API](https://github.com/HackerNews/API). With `-hnapi`,
reads go through a local SQLite store (`hnapi` package) and `/api/*` business
endpoints — see [`hn-cache.md`](./hn-cache.md).

## Go modules

`examples/hn-ssr` is its own module (`goqjs/examples/hn-ssr`); `hnapi/` is a
subpackage. The root `goqjs` module is unchanged. Locally link them with a
workspace file (**do not commit** — already gitignored):

```bash
# from repo root
go work init . ./examples/hn-ssr
# or ensure go.work contains:
#   use (
#     .
#     ./examples/hn-ssr
#   )
```

`examples/hn-ssr/go.mod` uses `replace goqjs => ../..` so `require goqjs` works
(module path has no dot). `crawshaw.io/sqlite` is a normal Go module dependency
(self-contained SQLite; needs cgo).

## Pages

| Route | Data |
|-------|------|
| `/` | Top 30 stories |
| `/item/:id` | Story + comments (depth ≤ 2) |

## Dev (Node + Vite)

```bash
npm install
npm run dev          # http://localhost:5173
```

SSR path: Express → `loadData(url)` → `renderToString` → hydrate.
Without `__HN_API_BASE__`, `api.js` still fans out to Firebase.

```bash
npm run build && npm start   # Node host + dist/
```

## Prod (Go + goqjs, embedded dist)

```bash
make prod            # ensures node_modules + dist, then native binary
./hn-ssr -addr :8080 -c 2
./hn-ssr -addr :8080 -c 2 -cache              # FIFO cache HN Firebase GETs (JS path)
./hn-ssr -addr :8080 -c 2 -hnapi              # SQLite + /api/* (default DB :memory:)
./hn-ssr -addr :8080 -c 2 -hnapi -cache       # -cache applies to Syncer Firebase GETs
./hn-ssr -addr :8080 -c 2 -hnapi -hnapi-db :memory:   # explicit in-memory (same as omit)
./hn-ssr -addr :8080 -c 2 -hnapi -hnapi-db ./hn.db
./hn-ssr -addr :8080 -c 2 -hnapi -hnapi-db 'file:./hn.db'   # equivalent URI form
./hn-ssr -addr :8080 -c 2 -hnapi -hnapi-db 'file:/tmp/hn.db'

./hn-ssr -addr :8080 -client-js=off            # SSR HTML only (no hydrate bundle)

# Cross-compile (needs Zig on PATH):
make prod-linux-x64
make pdist prod-linux-x64
```

From the repo root: `make hn-ssr` (native `prod` only; needs `go.work`).

### `-hnapi` behavior

- Opens SQLite via `-hnapi-db`: omit or pass `:memory:` for an in-process DB
  (mapped to crawshaw `file:…?mode=memory&cache=shared`); or a path (`./hn.db`) /
  URI (`file:./hn.db`). Plain paths get a `file:` prefix. On-disk WAL may leave
  `*.db-wal` / `*.db-shm` — normal.
- Serves `GET /api/topstories?limit=` → `Item[]` and `GET /api/item/{id}?depth=` → nested item.
- Injects `globalThis.__HN_API_BASE__` for SSR and `window.__HN_API_BASE__="/api"` in HTML.
- `api.js` then uses those endpoints (no JS recursion). Sync pulls from Firebase into SQLite
  (read-through; `SyncTop` also warms comment trees to `DefaultDepth`).

## Layout

```text
go.mod                 module goqjs/examples/hn-ssr
hnapi/                 Store + Syncer (no HTTP)
hnapi_http.go          /api handlers (main package)
src/api.js             Firebase or /api (via __HN_API_BASE__)
src/loadData.js        URL → page data
main.go                embed dist/ + goqjs pool + optional -hnapi
```

No client router — each navigation is a full document request (SSR).
