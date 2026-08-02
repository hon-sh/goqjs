import { hostname, timeAgo } from '../api.js'

function Comment({ comment, depth = 0 }) {
  return (
    <div className={`comment depth-${Math.min(depth, 2)}`}>
      <div className="comment-meta">
        {comment.by} {timeAgo(comment.time)}
      </div>
      <div
        className="comment-text"
        dangerouslySetInnerHTML={{ __html: comment.text || '' }}
      />
      {comment.comments?.map((c) => (
        <Comment key={c.id} comment={c} depth={depth + 1} />
      ))}
    </div>
  )
}

export default function Item({ item }) {
  if (!item) {
    return <p className="empty">Item not found.</p>
  }

  return (
    <article className="item-head">
      <h1>
        {item.url ? (
          <a href={item.url} rel="noreferrer">
            {item.title}
          </a>
        ) : (
          item.title
        )}
        {item.url ? <span className="domain">({hostname(item.url)})</span> : null}
      </h1>
      <div className="meta">
        {item.score ?? 0} points by {item.by} {timeAgo(item.time)} |{' '}
        <a href="/">back</a>
      </div>
      {item.text ? (
        <div
          className="comment-text"
          style={{ marginTop: 8 }}
          dangerouslySetInnerHTML={{ __html: item.text }}
        />
      ) : null}
      <section className="comments">
        {(item.comments || []).map((c) => (
          <Comment key={c.id} comment={c} depth={0} />
        ))}
      </section>
    </article>
  )
}
