import { expect, test } from '@playwright/test';
import {
  createNativeBenchSession,
  estimateBenchTestTimeoutMs,
  loadScenario,
  runBenchmark,
} from './construction_preview_bench.mjs';

test('construction preview benchmark @construction-preview-bench', async ({ browser }) => {
  const rawOptions = process.env.LOOM_CONSTRUCTION_BENCH_OPTIONS;
  if (!rawOptions) throw new Error('construction preview benchmark options were not provided by the CLI');
  const options = JSON.parse(rawOptions);
  const authorization = process.env.LOOM_CONSTRUCTION_BENCH_AUTHORIZATION ?? '';
  const scenario = await loadScenario(options);
  options.nativeTestTimeoutMs = estimateBenchTestTimeoutMs(options, scenario);
  test.setTimeout(options.nativeTestTimeoutMs);

  await runBenchmark(options, authorization, scenario, async ({ browserOptions, authorization: sessionAuthorization, concurrency }) => {
    const sessions = [];
    try {
      for (let index = 0; index < concurrency; index += 1) {
        const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
        sessions.push(await createNativeBenchSession({
          browser,
          context,
          options: browserOptions,
          authorization: sessionAuthorization,
          expect,
        }));
      }
      return sessions;
    } catch (error) {
      await Promise.allSettled(sessions.map(session => session.close()));
      throw error;
    }
  });
});
