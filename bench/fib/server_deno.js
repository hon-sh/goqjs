import { parseN, fibJSON } from "./fib.js";

// Tuned for Deno 2.x `deno serve` hot path:
// - sync handler (no Promise on the happy path)
// - `new Response(string)` fast path
// - reuse headers object
const JSON_HEADERS = { "content-type": "application/json" };

export default {
  fetch(req) {
    const url = new URL(req.url);
    const path = url.pathname;
    if (path !== "/fib" && path !== "/") {
      return new Response("not found\n", { status: 404 });
    }
    return new Response(fibJSON(parseN(url)), { headers: JSON_HEADERS });
  },
  onListen({ hostname, port }) {
    console.error(`bench deno: listening on ${hostname}:${port}`);
  },
};
