/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The build lands in web/dist, which the Go binary embeds. In development,
// `npm run dev` proxies the API and sign-in routes to a Keyhole
// server on :8080, so cookies stay same-origin.
const backend = process.env.KEYHOLE_DEV_BACKEND ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': backend,
      '/auth': backend,
    },
  },
  // The unit tests cover the analytics allowlist and the sign-in marker;
  // both use browser storage and DOM APIs.
  test: { environment: 'jsdom' },
})
