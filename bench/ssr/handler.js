async function(req, res) {
  if (typeof globalThis.__bench_render !== "function") {
    throw new Error("__bench_render missing; run: npm run build");
  }
  try {
    var out = await globalThis.__bench_render(req.query || "");
    res.statusCode = 200;
    await res.end(out.html);
  } catch (e) {
    var msg = "ssr failed";
    try {
      if (e && e.message) msg = String(e.message);
      else msg = String(e);
    } catch (_) {}
    try {
      if (e && e.stack) msg += " | " + String(e.stack);
    } catch (_) {}
    if (globalThis.console && console.log) console.log("BENCH_SSR_ERROR", msg);
    throw new Error(msg);
  }
}
