async function(n) {
  function fib(x) {
    if (x < 2) return x;
    return fib(x - 1) + fib(x - 2);
  }
  const v = fib(n);
  await resp.write(n, String(v));
}
