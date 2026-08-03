import ReactDOMServer from 'react-dom/server'
import App from './App.jsx'
import { loadData } from './loadData.js'

// QuickJS polyfills used by React's scheduler / server renderer.
if (typeof queueMicrotask !== 'function') {
  globalThis.queueMicrotask = function (fn) {
    Promise.resolve().then(fn)
  }
}
if (typeof MessageChannel === 'undefined') {
  globalThis.MessageChannel = function MessageChannel() {
    var self = this
    this.port1 = { onmessage: null }
    this.port2 = {
      postMessage: function (data) {
        queueMicrotask(function () {
          if (typeof self.port1.onmessage === 'function') {
            self.port1.onmessage({ data: data })
          }
        })
      },
    }
  }
}
if (typeof performance === 'undefined' || typeof performance.now !== 'function') {
  globalThis.performance = {
    now: function () {
      return Date.now()
    },
  }
}
if (typeof process === 'undefined') {
  globalThis.process = { env: { NODE_ENV: 'production' } }
}

async function render(url) {
  const t0 = Date.now()
  const data = await loadData(url)
  const t1 = Date.now()
  const html = ReactDOMServer.renderToString(<App data={data} />)
  const t2 = Date.now()
  return {
    html,
    data,
    load_ms: t1 - t0,
    render_ms: t2 - t1,
  }
}

globalThis.__hn_render = render
