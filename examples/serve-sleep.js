async function(req, res) {
  // Demonstrate async I/O multiplexing on one Runtime / pool worker.
  await sleep(200);
  res.statusCode = 200;
  await res.write("slept; ");
  await res.end("path=" + req.path + "\n");
}
