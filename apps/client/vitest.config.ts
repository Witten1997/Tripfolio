import { fileURLToPath, URL } from 'node:url'

import { configDefaults, defineConfig, mergeConfig } from 'vitest/config'

import viteConfig from './vite.config.ts'

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      include: ['src/**/*.spec.ts'],
      exclude: [...configDefaults.exclude, 'android/**'],
      root: fileURLToPath(new URL('./', import.meta.url)),
    },
  }),
)
