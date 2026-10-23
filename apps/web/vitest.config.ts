import { defineConfig } from 'vitest/config'

// Unit tests for framework-independent modules (e.g. the Markdown renderer). Nuxt pages are covered by
// typecheck, build and the compose end-to-end scripts.
export default defineConfig({
  test: {
    include: ['app/**/*.test.ts'],
    environment: 'node',
  },
})
