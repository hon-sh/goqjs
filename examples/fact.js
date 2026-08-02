async function(n) {
  function fact(x) {
    if (x < 0) throw new Error("n < 0");
    let r = 1n;
    for (let i = 2n; i <= BigInt(x); ++i) {
      r *= i;
    }
    return r;
  }
  const v = fact(n);
  console.log("[" + n + "]" + v);
}
