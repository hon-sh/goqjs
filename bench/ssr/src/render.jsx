import React from 'react'
import ReactDOMServer from 'react-dom/server'
import App from './App.jsx'
import { loadData } from './loadData.js'

export async function render(query) {
  const t0 = Date.now()
  const data = await loadData(query)
  const t1 = Date.now()
  const body = ReactDOMServer.renderToString(<App data={data} />)
  const t2 = Date.now()
  const html =
    '<!DOCTYPE html><html><head><meta charset="utf-8"><title>ssr-bench</title></head><body>' +
    body +
    '</body></html>'
  return {
    html,
    n: data.n,
    delay: data.delay,
    load_ms: t1 - t0,
    render_ms: t2 - t1,
  }
}
