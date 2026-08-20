import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build output is embedded into the Go binary (web/embed.go), so deploying
// stays "copy one file". Assets are hashed; index.html is not, and the server
// serves it without caching so a new build is picked up immediately.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // No sourcemaps in the shipped binary — this runs on a shop PC, not a
    // machine anyone debugs from.
    sourcemap: false,
  },
  server: {
    port: 5173,
    // In development the SPA runs on Vite and the API on the Go server.
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: false },
    },
  },
})
