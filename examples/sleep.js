async function(c) {
  for (let i = 0; i < c; ++i) {
    await resp.write(c, String(i));
    await sleep(1000);
  }
}
