import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ command }) => {
  const devBase = (process.env.TRIPFOLIO_ADMIN_PATH || '/wahaha').replace(/\/$/, '') + '/'
  return {
    base: command === 'serve' ? devBase : './',
    plugins: [
      vue(),
      {
        name: 'admin-development-base',
        apply: 'serve',
        transformIndexHtml: () => [
          { tag: 'base', attrs: { href: devBase }, injectTo: 'head-prepend' },
        ],
      },
    ],
    server: {
      port: 5174,
      strictPort: true,
      proxy: { '/api': process.env.TRIPFOLIO_DEV_API_TARGET || 'http://localhost:8080' },
    },
    build: { sourcemap: false },
  }
})
