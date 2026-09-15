import { fileURLToPath, URL } from 'node:url'

import { configDefaults, defineConfig, mergeConfig } from 'vitest/config'

import viteConfig from './vite.config.ts'

export default defineConfig((env) =>
  mergeConfig(viteConfig(env), {
    test: {
      environment: 'jsdom',
      include: ['src/**/*.spec.ts', '*.spec.ts'],
      exclude: [...configDefaults.exclude, 'android/**'],
      root: fileURLToPath(new URL('./', import.meta.url)),
      css: true,
    },
  }),
)
