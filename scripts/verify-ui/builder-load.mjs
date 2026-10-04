import { executeScenario, browserURL, runBrowserCase } from './common.mjs';
import { click, captureDOM, goto, injectReadFaultOnce, readPage, reload, waitFor, waitForCDPEvent, recordBrowserTiming } from './browser.mjs';
import { recordCheck } from './report.mjs';

const builderLoaded = "document.body.innerText.includes('Build your first table') || document.body.innerText.includes('Dataset graph')";
const builderError = "document.querySelector('[role=alert]')?.innerText.includes('Couldn’t load this dataset.')";
const retryLabel = /^(?:Try again|Retry(?:\s+(?:Builder|Explorer|capabilit)[\w ]*)?|Reload capabilities)$/i;
const faultCases = {
  list: { method: 'GET', pathEndsWith: '/explorers' },
  state: { method: 'GET', pathEndsWith: '/authoring/v2/builder' },
};
const recoveryResponse = (cdp, path) => waitForCDPEvent(cdp, 'Network.responseReceived', (event) => {
  try { return new URL(event.response.url).pathname.endsWith(path); } catch { return false; }
}, 10000);

const runCase = (context, caseName) => runBrowserCase(context, 'builder-load', caseName, async ({ cdp, report, faults }) => {
  const fault = await injectReadFaultOnce(cdp, faultCases[caseName]);
  faults.push(fault);
  await goto(cdp, browserURL(context.target, context.target.fixtureProject, context.target.bootstrapExplorerId, 'builder'));
  await fault.wait(30000);


  await waitFor(cdp, builderError, 30000);
  const page = await readPage(cdp);
  const visibleError = page.alerts.find((alert) => alert.includes('Couldn’t load this dataset.')) ?? null;
  const retry = page.buttons.find((button) => retryLabel.test(button.text));
  const actionableRetry = Boolean(retry && !retry.disabled && retry.ariaDisabled !== 'true');
  recordCheck(report, 'correctness', 'builder failure is exposed in an alert', Boolean(visibleError), { alert: visibleError });
  recordCheck(report, 'usability', 'builder failure exposes an actionable in-app retry', actionableRetry, { retry: retry ?? null });
  if (actionableRetry) {
    const recoveredRead = recoveryResponse(cdp, faultCases[caseName].pathEndsWith);
    await recordBrowserTiming(report, cdp, {
      name: 'builder in-app retry',
      action: () => click(cdp, 'button', { name: retry.text }),
      after: builderLoaded + " && !document.querySelector('[role=alert]')",
    });
    const status = (await recoveredRead).response.status;
    recordCheck(report, 'persistence', 'builder recovered through in-app Retry', status >= 200 && status < 300, { status });
  } else {
    await captureDOM(report, cdp, caseName + '-no-retry');
    const recoveredRead = recoveryResponse(cdp, faultCases[caseName].pathEndsWith);
    await reload(cdp, builderLoaded);
    const status = (await recoveredRead).response.status;
    recordCheck(report, 'persistence', 'reload separately restored the one-shot failed read', status >= 200 && status < 300, { status });
  }
});

export const runBuilderLoad = async (context, caseNames) => {
  const reports = [];
  for (const caseName of caseNames) reports.push(await runCase(context, caseName));
  return reports;
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  await executeScenario({ id: 'builder-load', argv: process.argv.slice(2), runner: runBuilderLoad });
}
