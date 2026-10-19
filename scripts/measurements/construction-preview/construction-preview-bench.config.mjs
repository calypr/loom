import { defineConfig } from '@playwright/test';

const executablePath = process.env.LOOM_CONSTRUCTION_BENCH_CHROME;
const MAX_BENCH_TEST_TIMEOUT_MS = 24 * 60 * 60 * 1000;

export default defineConfig({
  testDir: '.',
  testMatch: 'construction-preview-bench.spec.mjs',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30_000,
  globalTimeout: MAX_BENCH_TEST_TIMEOUT_MS,
  expect: { timeout: 5_000 },
  outputDir: '../../../.artifacts/construction-preview-bench/playwright-results',
  reporter: [
    ['list'],
    ['json', { outputFile: '../../../.artifacts/construction-preview-bench/playwright-results.json' }],
  ],
  use: {
    browserName: 'chromium',
    ...(executablePath ? { launchOptions: { executablePath } } : { channel: 'chrome' }),
    headless: true,
    viewport: { width: 1440, height: 1000 },
    actionTimeout: 5_000,
    navigationTimeout: 5_000,
    screenshot: 'off',
    video: 'off',
    trace: 'off',
  },
});
