# Minimal Hacker News SSR (Vite + React)

Demo for **goqjs** React SSR. Uses the public
[Hacker News Firebase API](https://github.com/HackerNews/API) — no GraphQL.

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

```bash
npm run build && npm start   # Node host + dist/
```

## Prod (Go + goqjs, embedded dist)

```bash
make prod            # ensures node_modules + dist, then native binary
./hn-ssr -addr :8080 -c 2
./hn-ssr -addr :8080 -c 2 -cache   # FIFO loadData cache by URL (max 100)

# Cross-compile (needs Zig on PATH):
make prod-linux-x64              # first time / reuse existing dist
make pdist prod-linux-x64        # force rebuild dist, then binary
```

From the repo root: `make hn-ssr` (native `prod` only).

`dist` is a Make directory target (depends on `node_modules`). Changing JS
sources does **not** invalidate it — use `make pdist` first. `make clean`
removes `dist`, `node_modules`, and binaries.

`main.go` embeds `dist/` (`client` assets + `server/ssr.js`). Each request:
`loadData` (stdlib `fetch`) → `renderToString` in QuickJS → HTML with
`__INITIAL_DATA__` → browser hydrate via client bundle.

## Layout

```text
src/api.js             Firebase HN helpers
src/loadData.js        URL → page data (shared SSR / client)
src/entry-server.jsx   Node SSR export
src/entry-goqjs.jsx    IIFE for QuickJS (sets __hn_render)
src/entry-client.jsx   hydrateRoot
src/App.jsx            home / item (plain <a> navigation)
server.js              Vite middleware (dev) / Node SSR (prod)
main.go                embed dist/ + goqjs pool host
Makefile               prod / prod-linux-x64 / pdist / clean
vite.goqjs.config.js   bundles React into dist/server/ssr.js
```

No client router — each navigation is a full document request (SSR).
