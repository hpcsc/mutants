import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['tests/**/*.test.ts'],
    globalSetup: ['./globalSetup.ts'],
    testTimeout: 30000,
  },
})
