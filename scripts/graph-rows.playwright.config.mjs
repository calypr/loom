import { defineConfig } from 'playwright/test';
import { resolve } from 'node:path';

if (!process.env.LOOM_GRAPH_ROWS_EVIDENCE) throw new Error('Set LOOM_GRAPH_ROWS_EVIDENCE to a fresh evidence directory outside watched source.');
export default defineConfig({
  testDir: './verify-ui/specs',
  testMatch: 'graph-rows.spec.mjs',
  timeout: 180_000,
  expect: { timeout: 30_000 },
  workers: 1,
  retries: 0,
  fullyParallel: false,
  outputDir: resolve(process.env.LOOM_GRAPH_ROWS_EVIDENCE, 'artifacts'),
  reporter: [['list'], ['json', { outputFile: resolve(process.env.LOOM_GRAPH_ROWS_EVIDENCE, 'results.json') }]],
  use: {
    browserName: 'chromium',
    headless: true,
    viewport: { width: 1536, height: 900 },
    actionTimeout: 30_000,
    navigationTimeout: 30_000,
    launchOptions: { executablePath: process.env.LOOM_E2E_CHROME || '/usr/bin/chromium', args: ['--no-sandbox', '--disable-dev-shm-usage'] },
    trace: 'off',
    screenshot: 'only-on-failure',
    video: 'off',
  },
});
