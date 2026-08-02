import Home from './pages/Home.jsx'
import Item from './pages/Item.jsx'

export default function App({ data }) {
  const page = data?.page

  let body
  if (page === 'home') {
    body = <Home stories={data.stories} />
  } else if (page === 'item') {
    body = <Item item={data.item} />
  } else if (page === 'error') {
    body = <p className="error">{data.message || 'Error'}</p>
  } else {
    body = <p className="error">Not found</p>
  }

  return (
    <div className="shell">
      <header className="header">
        <a href="/" className="logo">
          Y
        </a>
        <nav className="nav">
          <a href="/">HN SSR</a>
        </nav>
      </header>
      <main className="main">{body}</main>
      <footer className="footer">
        Minimal React SSR demo · data from Hacker News Firebase API
      </footer>
    </div>
  )
}
