package runtime

// Core JS boot: platform globals only (no console/fetch/resp).
const coreBoot = `import * as std from 'std';
import * as os from 'os';
globalThis.std = std;
globalThis.os = os;
globalThis.setTimeout = os.setTimeout;
globalThis.clearTimeout = os.clearTimeout;
globalThis.__hon_async_waiters = Object.create(null);
globalThis.__hon_async_settle = function(id, ok, payload) {
  var w = __hon_async_waiters[id];
  if (!w) return;
  delete __hon_async_waiters[id];
  if (ok) w.resolve(payload);
  else w.reject(new Error(typeof payload === 'string' ? payload : String(payload)));
};
globalThis.__hon_invoke = function(id, jsonArgs) {
  let args;
  try { args = JSON.parse(jsonArgs); }
  catch (e) { __hon_done(id, false, String(e)); return; }
  if (typeof globalThis.__hon_run !== 'function') {
    __hon_done(id, false, '__hon_run is not defined');
    return;
  }
  Promise.resolve(__hon_run.apply(undefined, args)).then(
    function() { __hon_done(id, true, null); },
    function(e) {
      var msg = (e && e.stack) ? String(e.stack) : String(e);
      __hon_done(id, false, msg);
    }
  );
};
`

// asyncStartJS is evaluated once so async hosts can register waiters from JS helpers.
const asyncHelperJS = `
globalThis.__hon_async = function(name, payloadObj) {
  var payload = JSON.stringify(payloadObj === undefined ? null : payloadObj);
  return new Promise(function(resolve, reject) {
    var id = __hon_async_start(name, payload);
    __hon_async_waiters[id] = { resolve: resolve, reject: reject };
  });
};
`
