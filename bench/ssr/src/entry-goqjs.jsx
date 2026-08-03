import { render } from './render.jsx'

// Polyfills live in main.go (Eval before this IIFE) — Vite hoists imports,
// so MessageChannel must exist before React's module body runs.

globalThis.__bench_render = render
