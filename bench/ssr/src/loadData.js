/** Fake I/O: fixed sleep, then a deterministic item array (no network). */

export function sleep(ms) {
  return new Promise(function (resolve) {
    setTimeout(resolve, ms)
  })
}

/** Minimal query parse — avoids URLSearchParams (missing in QuickJS). */
export function parseParams(query) {
  var q = typeof query === 'string' ? query : ''
  if (q.charAt(0) === '?') q = q.slice(1)
  var n = 200
  var delay = 10
  var parts = q.split('&')
  for (var i = 0; i < parts.length; i++) {
    var kv = parts[i].split('=')
    var key = kv[0]
    var val = kv.length > 1 ? kv.slice(1).join('=') : ''
    if (key === 'n') {
      var nn = Number(val)
      if (nn === nn && nn >= 1) n = nn
    } else if (key === 'delay') {
      var dd = Number(val)
      if (dd === dd && dd >= 0) delay = dd
    }
  }
  if (n > 10000) n = 10000
  if (delay > 5000) delay = 5000
  return { n: n | 0, delay: delay | 0 }
}

/** Build a fixed-shape list. Same inputs → same items (no randomness). */
export function buildItems(n) {
  var items = new Array(n)
  for (var i = 0; i < n; i++) {
    items[i] = {
      id: i,
      title: 'Item #' + i,
      score: (i * 17 + 3) % 100,
      active: i % 5 !== 0,
      premium: i % 11 === 0,
      tags: ['a' + (i % 7), 'tag-' + (i % 13), i % 3 === 0 ? 'featured' : 'plain'],
    }
  }
  return items
}

export async function loadData(query) {
  var params = parseParams(query)
  await sleep(params.delay)
  return {
    n: params.n,
    delay: params.delay,
    items: buildItems(params.n),
  }
}
