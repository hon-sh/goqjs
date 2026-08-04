/** Load the same Vite IIFE hon uses — identical React work across runtimes. */
const code = await Deno.readTextFile(new URL("./dist/server/ssr.js", import.meta.url));
(0, eval)(code);

if (typeof globalThis.__bench_render !== "function") {
  throw new Error("dist/server/ssr.js missing __bench_render (run: npm run build)");
}

const HTML_HEADERS = { "content-type": "text/html; charset=utf-8" };

export default {
  async fetch(req) {
    const url = new URL(req.url);
    if (url.pathname !== "/" && url.pathname !== "/ssr") {
      return new Response("not found\n", { status: 404 });
    }
    const out = await globalThis.__bench_render(url.search.slice(1));
    return new Response(out.html, { headers: HTML_HEADERS });
  },
  onListen({ hostname, port }) {
    console.error(`bench-ssr deno: listening on ${hostname}:${port}`);
  },
};
