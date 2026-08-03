import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

/** Self-contained IIFE for goqjs (embeds React). */
export default defineConfig({
  plugins: [react()],
  define: {
    'process.env.NODE_ENV': JSON.stringify('production'),
  },
  build: {
    outDir: 'dist/server',
    emptyOutDir: true,
    target: 'es2020',
    lib: {
      entry: 'src/entry-goqjs.jsx',
      name: 'BenchSSR',
      formats: ['iife'],
      fileName: () => 'ssr.js',
    },
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
      },
    },
    minify: true,
  },
})
