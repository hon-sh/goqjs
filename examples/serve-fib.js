async function(req, res) {
  // Keep fib algorithm in sync with bench/fib.js
  const n = Number((req.query.match(/(?:^|&)n=(\d+)/) || [])[1] || 10);
  function fib(x) {
    if (x < 2) return x;
    return fib(x - 1) + fib(x - 2);
  }
  const v = fib(n);
  res.statusCode = 200;
  await res.end(JSON.stringify({ n: n, fib: v }) + "\n");
}
