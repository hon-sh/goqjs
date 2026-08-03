import React from 'react'

function itemClassName(item) {
  if (!item.active) return 'item muted'
  if (item.premium) return 'item premium'
  if (item.score >= 70) return 'item hot'
  if (item.score < 20) return 'item cold'
  return 'item'
}

function TagList({ tags }) {
  const shown = []
  for (let i = 0; i < tags.length; i++) {
    const t = tags[i]
    // Per-item branching: skip short tags, emphasize featured.
    if (t.length <= 2) continue
    if (t === 'featured') {
      shown.push(
        <span key={t} className="tag featured">
          {t}
        </span>,
      )
    } else if (t.indexOf('tag-') === 0) {
      shown.push(
        <span key={t} className="tag named">
          {t}
        </span>,
      )
    } else {
      shown.push(
        <span key={t} className="tag">
          {t}
        </span>,
      )
    }
  }
  return <span className="tags">{shown}</span>
}

function ItemRow({ item }) {
  const cls = itemClassName(item)
  const title =
    item.score % 3 === 0 ? <strong>{item.title}</strong> : <span>{item.title}</span>
  const badge =
    item.premium && item.active ? (
      <em className="badge">PRO</em>
    ) : !item.active ? (
      <em className="badge off">OFF</em>
    ) : item.score >= 70 ? (
      <em className="badge hot">HOT</em>
    ) : null

  return (
    <li className={cls} data-id={item.id} data-score={item.score}>
      {badge} {title}{' '}
      <span className="score">({item.score})</span> <TagList tags={item.tags} />
    </li>
  )
}

export default function App({ data }) {
  const items = data.items
  const rows = new Array(items.length)
  for (let i = 0; i < items.length; i++) {
    rows[i] = <ItemRow key={items[i].id} item={items[i]} />
  }
  return (
    <main>
      <h1>
        SSR bench — {data.n} items (delay={data.delay}ms)
      </h1>
      <ul className="list">{rows}</ul>
    </main>
  )
}
