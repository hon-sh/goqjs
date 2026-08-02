import { getTopStories, getItemWithComments } from './api.js'

/**
 * Load page data for a URL. Shared by SSR (Node now; goqjs later) and client navigations.
 */
export async function loadData(url) {
  const { pathname } = new URL(url, 'http://local')

  if (pathname === '/' || pathname === '') {
    const stories = await getTopStories(30)
    return { page: 'home', stories }
  }

  const m = pathname.match(/^\/item\/(\d+)\/?$/)
  if (m) {
    const item = await getItemWithComments(m[1], 2)
    return { page: 'item', item }
  }

  return { page: 'notfound' }
}
