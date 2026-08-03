// Shared fib helpers for Bun/Deno bench servers.
// Keep algorithm in sync with serve-fib.js (and examples/serve-fib.js).

export function fib(x) {
  if (x < 2) return x;
  return fib(x - 1) + fib(x - 2);
}

/** @param {URL} url */
export function parseN(url, fallback = 10) {
  const raw = url.searchParams.get("n");
  if (raw == null || raw === "") return fallback;
  const n = Number(raw);
  return Number.isFinite(n) && n >= 0 ? (n | 0) : fallback;
}

export function fibJSON(n) {
  return `{"n":${n},"fib":${fib(n)}}\n`;
}
