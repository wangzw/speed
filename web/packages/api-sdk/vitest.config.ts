import { defineConfig } from 'vitest/config'
import { sharedTestOptions } from '../../vitest.shared.ts'

export default defineConfig({
  test: sharedTestOptions({
    environment: 'jsdom',
    coverage: {
      exclude: ['src/index.ts'],
    },
  }),
})
