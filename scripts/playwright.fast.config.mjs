import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './verify-ui/fast-specs',
  testMatch: '**/*.spec.mjs',
  globalSetup: './verify-ui/helpers/fast-global-setup.mjs',
  fullyParallel: true,
  workers: Math.max(1, Number(process.env.LOOM_FAST_WORKERS ?? 4) || 4),
  retries: 0,
  timeout: 20_000,
  expect: { timeout: 5_000 },
  outputDir: '../.artifacts/playwright-fast/results',
  reporter: [['list']],
  use: {
    browserName: 'chromium',
    channel: 'chrome',
    headless: true,
    viewport: { width: 1440, height: 1000 },
    actionTimeout: 5_000,
    navigationTimeout: 5_000,
    screenshot: 'off',
    video: 'off',
    trace: 'off',
  },
});
