# Minimal Hacker News SSR (Vite + React)

Demo app for future **goqjs** React SSR integration. Uses the public
[Hacker News Firebase API](https://github.com/HackerNews/API) — no GraphQL.

## Pages

| Route | Data |
|-------|------|
| `/` | Top 30 stories |
| `/item/:id` | Story + comments (depth ≤ 2) |

SSR path: Express → `loadData(url)` (async `fetch`) → `renderToString` → hydrate.

## Run (Node host)

```bash
cd examples/hn-ssr
npm install
npm run dev          # http://localhost:5173
```

```bash
npm run build
npm start            # Node + dist/
```

## Run (Go + goqjs, embedded dist)

```bash
npm run build:go     # vite → dist/ + go build -o hn-ssr
./hn-ssr -addr :8080 -c 2
```

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
vite.goqjs.config.js   bundles React into dist/server/ssr.js
```

No client router — each navigation is a full document request (SSR).
