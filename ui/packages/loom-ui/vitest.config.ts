import { defineConfig } from 'vitest/config';

export default defineConfig({
  cacheDir: process.env.LOOM_VITEST_CACHE_DIR,
  test: {
    environment: 'node',
    globals: true,
    setupFiles: ['./vitest.setup.ts'],
    testTimeout: process.env.CI ? 600_000 : 30_000,
    hookTimeout: process.env.CI ? 600_000 : 30_000,
  },
});
