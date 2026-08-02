import ReactDOMServer from 'react-dom/server'
import App from './App.jsx'
import { loadData } from './loadData.js'

export async function render(url) {
  const data = await loadData(url)
  const html = ReactDOMServer.renderToString(<App data={data} />)
  return { html, data }
}
