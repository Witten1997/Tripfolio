import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig, loadEnv } from 'vite'

import { amapProxies } from './amap-proxy.ts'

// 同一份构建产物既由 Caddy 作为网页发布，也被 Capacitor 打进安卓包（webDir: dist）。
export default defineConfig(({ mode }) => ({
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
      ...amapProxies(
        loadEnv(mode, fileURLToPath(new URL('.', import.meta.url)), '').AMAP_JSCODE ?? '',
      ),
      '/api': process.env.TRIPFOLIO_DEV_API_TARGET || 'http://localhost:8080',
      '/health': process.env.TRIPFOLIO_DEV_API_TARGET || 'http://localhost:8080',
    },
  },
  build: {
    sourcemap: false,
  },
}))
