import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './playwright',
  testMatch: '**/*.spec.mjs',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 180_000,
  globalTimeout: 600_000,
  expect: { timeout: 5_000 },
  outputDir: '../.artifacts/playwright/results',
  reporter: [
    ['list'],
    ['json', { outputFile: '../.artifacts/playwright/results.json' }],
  ],
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
