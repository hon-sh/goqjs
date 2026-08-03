/** Load the same Vite IIFE goqjs uses — identical React work across runtimes. */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
const code = readFileSync(join(root, "dist/server/ssr.js"), "utf8");
(0, eval)(code);

if (typeof globalThis.__bench_render !== "function") {
  throw new Error("dist/server/ssr.js missing __bench_render (run: npm run build)");
}

const port = Number(process.env.PORT || 19200);
const HTML_HEADERS = { "content-type": "text/html; charset=utf-8" };

Bun.serve({
  port,
  hostname: "127.0.0.1",
  async fetch(req) {
    const url = new URL(req.url);
    if (url.pathname !== "/" && url.pathname !== "/ssr") {
      return new Response("not found\n", { status: 404 });
    }
    const out = await globalThis.__bench_render(url.search.slice(1));
    return new Response(out.html, { headers: HTML_HEADERS });
  },
});

console.error(`bench-ssr bun: listening on 127.0.0.1:${port}`);
