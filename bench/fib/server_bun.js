import { parseN, fibJSON } from "./fib.js";

const port = Number(process.env.PORT || 19100);
const JSON_HEADERS = { "content-type": "application/json" };

Bun.serve({
  port,
  hostname: "127.0.0.1",
  fetch(req) {
    const url = new URL(req.url);
    const path = url.pathname;
    if (path !== "/fib" && path !== "/") {
      return new Response("not found\n", { status: 404 });
    }
    return new Response(fibJSON(parseN(url)), { headers: JSON_HEADERS });
  },
});

console.error(`bench bun: listening on 127.0.0.1:${port}`);
