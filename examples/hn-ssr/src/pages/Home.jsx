import { hostname, timeAgo } from '../api.js'

export default function Home({ stories }) {
  if (!stories?.length) {
    return <p className="empty">No stories.</p>
  }

  return (
    <ol className="stories" start={1} style={{ listStyle: 'none', margin: 0, padding: 0 }}>
      {stories.map((story, i) => (
        <li key={story.id} className="story">
          <span className="rank">{i + 1}.</span>
          <div className="title-line">
            {story.url ? (
              <a className="title" href={story.url} rel="noreferrer">
                {story.title}
              </a>
            ) : (
              <a className="title" href={`/item/${story.id}`}>
                {story.title}
              </a>
            )}
            {story.url ? <span className="domain">({hostname(story.url)})</span> : null}
          </div>
          <div className="meta">
            {story.score ?? 0} points by {story.by} {timeAgo(story.time)} |{' '}
            <a href={`/item/${story.id}`}>{story.descendants ?? 0} comments</a>
          </div>
        </li>
      ))}
    </ol>
  )
}
