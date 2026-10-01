import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  base: '/admin/',
  plugins: [vue()],
  server: {
    port: 5174,
    strictPort: true,
    proxy: { '/api': process.env.TRIPFOLIO_DEV_API_TARGET || 'http://localhost:8080' },
  },
  build: { sourcemap: false },
})
