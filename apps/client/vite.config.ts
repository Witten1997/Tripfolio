import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// 同一份构建产物既由 Caddy 作为网页发布，也被 Capacitor 打进安卓包（webDir: dist）。
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    // 开发时与 Go API 同源，避免 CORS；Capacitor 打包时通过 VITE_API_BASE_URL 指向正式地址
    proxy: {
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
    },
  },
  build: {
    sourcemap: false,
  },
})
