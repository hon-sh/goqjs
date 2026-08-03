const FIREBASE_BASE = 'https://hacker-news.firebaseio.com/v0'

function hnApiBase() {
  if (typeof globalThis.__HN_API_BASE__ === 'string' && globalThis.__HN_API_BASE__) {
    return globalThis.__HN_API_BASE__
  }
  return null
}

async function getJSON(url, { retries = 2 } = {}) {
  let lastErr
  for (let i = 0; i <= retries; i++) {
    try {
      const res = await fetch(url)
      if (!res.ok) {
        throw new Error(`HN API ${url}: ${res.status}`)
      }
      return await res.json()
    } catch (err) {
      lastErr = err
      if (i < retries) {
        await new Promise((r) => setTimeout(r, 150 * (i + 1)))
      }
    }
  }
  throw lastErr
}

/** Run async tasks with a concurrency limit. */
async function mapPool(items, limit, fn) {
  const out = new Array(items.length)
  let i = 0
  async function worker() {
    while (i < items.length) {
      const idx = i++
      out[idx] = await fn(items[idx], idx)
    }
  }
  const n = Math.min(limit, items.length) || 1
  await Promise.all(Array.from({ length: n }, () => worker()))
  return out
}

export function getItem(id) {
  return getJSON(`${FIREBASE_BASE}/item/${id}.json`)
}

export async function getTopStories(limit = 30) {
  const base = hnApiBase()
  if (base) {
    return getJSON(`${base}/topstories?limit=${limit}`)
  }
  const ids = await getJSON(`${FIREBASE_BASE}/topstories.json`)
  const slice = ids.slice(0, limit)
  const items = await mapPool(slice, 8, (id) => getItem(id))
  return items.filter(Boolean)
}

/** Flatten comments to a shallow tree (depth-limited) for a minimal item page. */
export async function getItemWithComments(id, maxDepth = 2) {
  const base = hnApiBase()
  if (base) {
    return getJSON(`${base}/item/${id}?depth=${maxDepth}`)
  }

  const item = await getItem(id)
  if (!item) return null

  async function loadKids(parent, depth) {
    if (!parent.kids?.length || depth > maxDepth) {
      return { ...parent, comments: [] }
    }
    const kids = await mapPool(parent.kids.slice(0, 40), 8, async (kidId) => {
      const kid = await getItem(kidId)
      if (!kid || kid.deleted || kid.dead) return null
      return loadKids(kid, depth + 1)
    })
    return { ...parent, comments: kids.filter(Boolean) }
  }

  return loadKids(item, 1)
}

export function hostname(url) {
  if (!url) return ''
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return ''
  }
}

export function timeAgo(unix) {
  if (!unix) return ''
  const s = Math.max(1, Math.floor(Date.now() / 1000 - unix))
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  return `${d}d ago`
}
