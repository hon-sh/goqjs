async function(req, res) {
  res.statusCode = 200;
  await res.end("hello " + req.method + " " + req.path + "\n");
}
