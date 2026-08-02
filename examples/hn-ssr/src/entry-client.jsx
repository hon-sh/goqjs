import { hydrateRoot } from 'react-dom/client'
import App from './App.jsx'
import { loadData } from './loadData.js'
import './styles.css'

const el = document.getElementById('root')

async function boot() {
  let data = window.__INITIAL_DATA__
  if (!data) {
    data = await loadData(window.location.pathname + window.location.search)
  }
  hydrateRoot(el, <App data={data} />)
}

boot()
