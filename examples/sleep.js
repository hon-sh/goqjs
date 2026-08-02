async function(c) {
  for (let i = 0; i < c; ++i) {
    console.log("[" + c + "]" + i);
    await sleep(1000);
  }
}
