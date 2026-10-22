import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './verify-ui/journey-specs',
  testMatch: '**/*.spec.mjs',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 20_000 },
  outputDir: '../.artifacts/playwright-journey/results',
  reporter: [['list']],
  use: {
    browserName: 'chromium',
    channel: 'chrome',
    headless: true,
    viewport: { width: 1440, height: 1000 },
    actionTimeout: 15_000,
    navigationTimeout: 20_000,
    screenshot: 'off',
    video: 'off',
    trace: 'off',
  },
});
