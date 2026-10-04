import { executeScenario, browserURL } from './common.mjs';
import { runPlaywrightCase } from './playwright-case.mjs';
import { recordCheck } from './report.mjs';

const retryLabel = /^(?:Try again|Retry(?:\s+(?:Builder|Explorer|capabilit)[\w ]*)?|Reload capabilities)$/i;
const faultCases = {
  list: { method: 'GET', pathEndsWith: '/explorers' },
  state: { method: 'GET', pathEndsWith: '/authoring/v2/builder' },
};

const runCase = (context, caseName) => runPlaywrightCase(context, 'builder-load', caseName,
  async ({ page, browser, report, action, fault }) => {
    const target = context.target;
    const injection = await fault(faultCases[caseName]);
    await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
      waitUntil: 'domcontentloaded',
    });
    const errorAlert = page.getByRole('alert').filter({ hasText: 'Couldn’t load this dataset.' });
    await errorAlert.waitFor({ state: 'visible', timeout: 30000 });
    recordCheck(report, 'correctness', 'builder failure is exposed in an alert', true,
      { alert: await errorAlert.innerText(), failedRead: faultCases[caseName] });
    recordCheck(report, 'correctness', 'specific builder read fault was injected', injection.count() === 1,
      { method: faultCases[caseName].method, pathEndsWith: faultCases[caseName].pathEndsWith, count: injection.count() });

    const retry = page.getByRole('button', { name: retryLabel });
    const count = await retry.count();
    const actionable = count === 1 && await retry.isVisible() && await retry.isEnabled();
    recordCheck(report, 'usability', 'builder failure exposes an actionable in-app retry', actionable,
      { count, visible: count === 1 ? await retry.isVisible() : false, enabled: count === 1 ? await retry.isEnabled() : false });
    if (actionable) {
      const recoveredResponse = page.waitForResponse(response => {
        const request = response.request();
        const url = new URL(response.url());
        return request.method() === faultCases[caseName].method &&
          url.pathname.endsWith(faultCases[caseName].pathEndsWith);
      }, { timeout: 10000 });
      await action('builder in-app Retry', retry, () => retry.click(), {
        timeout: 5000,
        budget: 5000,
        after: async () => {
          await Promise.all([
            recoveredResponse,
            page.getByText(/Build your first table|Dataset graph/).first().waitFor({ state: 'visible', timeout: 10000 }),
            errorAlert.waitFor({ state: 'hidden', timeout: 10000 }),
          ]);
        },
      });
      const response = await recoveredResponse;
      recordCheck(report, 'persistence', 'builder recovered through in-app Retry', response.status() >= 200 && response.status() < 300,
        { method: response.request().method(), path: new URL(response.url()).pathname, status: response.status() });
      return;
    }

    await browser.captureFailure(new Error('Builder error did not expose a unique actionable Retry button'), {
      action: { label: 'inspect Builder Retry', locator: retry.toString(), targetLocator: retry },
      phase: 'retry-unavailable',
      failedRead: faultCases[caseName],
    });
    const response = page.waitForResponse(candidate => {
      const request = candidate.request();
      return request.method() === faultCases[caseName].method &&
        new URL(candidate.url()).pathname.endsWith(faultCases[caseName].pathEndsWith);
    }, { timeout: 10000 });
    await page.reload({ waitUntil: 'domcontentloaded' });
    const recovery = await response;
    await page.getByText(/Build your first table|Dataset graph/).first().waitFor({ state: 'visible', timeout: 10000 });
    await errorAlert.waitFor({ state: 'hidden', timeout: 10000 });
    recordCheck(report, 'persistence', 'reload separately restored the one-shot failed read',
      recovery.status() >= 200 && recovery.status() < 300,
      { method: recovery.request().method(), path: new URL(recovery.url()).pathname, status: recovery.status() });
  });

export const runBuilderLoad = async (context, caseNames) => {
  const reports = [];
  for (const caseName of caseNames) reports.push(await runCase(context, caseName));
  return reports;
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  await executeScenario({ id: 'builder-load', argv: process.argv.slice(2), runner: runBuilderLoad });
}
