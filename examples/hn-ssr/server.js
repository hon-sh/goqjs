import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import express from 'express'
import { createServer as createViteServer } from 'vite'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const isProd = process.env.NODE_ENV === 'production'
const PORT = Number(process.env.PORT) || 5173

function serialize(data) {
  return JSON.stringify(data).replace(/</g, '\\u003c')
}

async function createServer() {
  const app = express()
  let vite

  if (!isProd) {
    vite = await createViteServer({
      root: __dirname,
      server: { middlewareMode: true },
      appType: 'custom',
    })
    app.use(vite.middlewares)
  } else {
    app.use(
      express.static(path.resolve(__dirname, 'dist/client'), {
        index: false,
        maxAge: '1h',
      }),
    )
  }

  app.use(async (req, res, next) => {
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      return next()
    }

    try {
      const url = req.originalUrl
      let template
      let render

      if (!isProd) {
        template = fs.readFileSync(path.resolve(__dirname, 'index.html'), 'utf-8')
        template = await vite.transformIndexHtml(url, template)
        ;({ render } = await vite.ssrLoadModule('/src/entry-server.jsx'))
      } else {
        template = fs.readFileSync(
          path.resolve(__dirname, 'dist/client/index.html'),
          'utf-8',
        )
        ;({ render } = await import(
          pathToFileURL(path.resolve(__dirname, 'dist/server/entry-server.js')).href
        ))
      }

      const { html, data } = await render(url)
      const page = template
        .replace('<!--app-html-->', html)
        .replace(
          '<!--app-data-->',
          `<script>window.__INITIAL_DATA__=${serialize(data)}</script>`,
        )

      res.status(200).set({ 'Content-Type': 'text/html' }).end(page)
    } catch (e) {
      vite?.ssrFixStacktrace?.(e)
      console.error(e)
      res.status(500).set({ 'Content-Type': 'text/plain' }).end(String(e?.stack || e))
    }
  })

  app.listen(PORT, () => {
    console.log(`HN SSR → http://localhost:${PORT}`)
  })
}

createServer()
