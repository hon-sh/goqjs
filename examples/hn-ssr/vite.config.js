import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig(({ isSsrBuild }) => ({
  plugins: [react()],
  build: {
    outDir: isSsrBuild ? 'dist/server' : 'dist/client',
    emptyOutDir: !isSsrBuild,
  },
}))
