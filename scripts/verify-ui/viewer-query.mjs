import { executeScenario, browserURL, runBrowserCase } from './common.mjs';
import { captureDOM, click, evaluate, goto, injectReadFaultOnce, inspectAction, readPage, waitFor, recordBrowserTiming } from './browser.mjs';
import { isActionable, recordCheck, recordUntested } from './report.mjs';
import { publishDefaultPatientExplorer } from './workflows.mjs';

const viewerRows = "document.querySelectorAll('table[aria-label$=\" results\"] tbody tr').length";
const expectedRows = "document.body.innerText.includes('dev-patient-001') && document.body.innerText.includes('dev-patient-002')";

const runOutput = (context) => runBrowserCase(context, 'viewer-query', 'output', async ({ cdp, report, faults }) => {
  let project = context.target.fixtureProject;
  let explorer = context.target.bootstrapExplorerId;
  if (!context.custom) {
    const published = await publishDefaultPatientExplorer(cdp, context, report, 'viewer');
    explorer = published.explorer;
  } else {
    await goto(cdp, browserURL(context.target, project, explorer, 'viewer'));
    await waitFor(cdp, "document.body.innerText.includes('Published') && document.querySelector('table[aria-label$=\" results\"]')", 60000);
    const initialPage = await readPage(cdp);
    recordCheck(report, 'correctness', 'custom Viewer runtime and output table load before fault injection', !initialPage.alerts.some((alert) => alert.includes('Results could not be loaded.')), {
      title: initialPage.title,
      outputTableVisible: true,
      rowCount: initialPage.rows.length,
    });
  }
  report.target.project = project;
  report.target.explorer = explorer;

  const fault = await injectReadFaultOnce(cdp, { method: 'POST', pathEndsWith: '/graphql/graph' });
  faults.push(fault);
  const freshURL = new URL(browserURL(context.target, project, explorer, 'viewer'));
  freshURL.searchParams.set('verify_run', context.runID);
  await goto(cdp, freshURL.toString());
  await fault.wait(30000);

  let page;
  let failureVisible = false;
  try {
    await waitFor(cdp, "document.body.innerText.includes('Results could not be loaded.')", 15000);
    failureVisible = true;
    page = await readPage(cdp);
  } catch {
    page = await readPage(cdp);
  }
  const retry = page.buttons.find((button) => /^(Try again|Retry)$/i.test(button.text));
  const retryAction = retry ? await inspectAction(cdp, 'button', { name: retry.text }) : null;
  const actionable = Boolean(failureVisible && retry && isActionable(retryAction));
  recordCheck(report, 'correctness', 'injected result-query error is visibly reported', failureVisible && page.alerts.some((alert) => alert.includes('Results could not be loaded.')), {
    alerts: page.alerts,
    title: page.title,
  });
  recordCheck(report, 'usability', 'result-query error exposes an actionable Retry control', actionable, { retry: retry ?? null, actionability: retryAction });
  if (!actionable) {
    await captureDOM(report, cdp, 'query-failure-no-actionable-retry');
    return;
  }

  await recordBrowserTiming(report, cdp, {
    name: 'retry result query',
    action: () => click(cdp, 'button', { name: retry.text }),
    after: (context.custom
      ? "document.querySelector('table[aria-label$=\" results\"]')"
      : expectedRows + ' && Number(' + viewerRows + ') === 2') + ' && !document.body.innerText.includes(\"Results could not be loaded.\")',
    timeout: 5000,
  });
  const initial = await evaluate(cdp, "[...document.querySelectorAll('table[aria-label$=\" results\"] tbody tr')].map((row)=>row.innerText.trim())");
  if (context.custom) {
    recordCheck(report, 'correctness', 'Retry restored the custom output table', Boolean(await evaluate(cdp, "document.querySelector('table[aria-label$=\" results\"]')")), { rowCount: initial.length });
    recordUntested(report, 'correctness', 'fixture-specific Patient rows and Gender facet behavior', 'Custom target data is read-only and is not assumed to contain Loom development Patients.');
    recordUntested(report, 'usability', 'custom target filter semantics', 'The custom Explorer schema is unknown; this run verifies query failure recovery without asserting a particular facet.');
    return;
  }
  recordCheck(report, 'correctness', 'retried Viewer results contain both independent fixture Patients', initial.length === 2 && initial.some((row) => row.includes('dev-patient-001')) && initial.some((row) => row.includes('dev-patient-002')), { rows: initial });

  const filterButton = await inspectAction(cdp, 'button', { name: 'Load values' });
  if (!isActionable(filterButton)) {
    recordUntested(report, 'usability', 'Gender facet can be opened to load values', 'The published fixture declares no runtime filters, so Viewer renders no facet control.');
    await captureDOM(report, cdp, 'gender-facet-unavailable');
    return;
  }
  recordCheck(report, 'usability', 'Gender facet can be opened to load values', true, filterButton);
  await recordBrowserTiming(report, cdp, {
    name: 'load Gender facet values',
    action: () => click(cdp, 'button', { name: 'Load values' }),
    after: "[...document.querySelectorAll('label')].some((label)=>label.innerText.toLowerCase().includes('female'))",
    timeout: 5000,
  });
  const femaleLabel = await inspectAction(cdp, 'label', { includes: 'female' });
  recordCheck(report, 'usability', 'female facet value is visible and actionable', isActionable(femaleLabel), femaleLabel ?? {});
  if (!isActionable(femaleLabel)) {
    await captureDOM(report, cdp, 'female-facet-unavailable');
    return;
  }
  await recordBrowserTiming(report, cdp, {
    name: 'apply female filter',
    action: () => click(cdp, 'label', { includes: 'female' }),
    after: "Number(" + viewerRows + ") === 1 && document.querySelector('table[aria-label$=\" results\"] tbody tr')?.innerText.includes('dev-patient-001') && !document.body.innerText.includes('dev-patient-002')",
    timeout: 5000,
  });
  const filtered = await evaluate(cdp, "[...document.querySelectorAll('table[aria-label$=\" results\"] tbody tr')].map((row)=>row.innerText.trim())");
  recordCheck(report, 'correctness', 'Gender facet returns the matching fixture Patient only', filtered.length === 1 && filtered[0].includes('dev-patient-001'), { rows: filtered });
  const currentMode = new URL(await evaluate(cdp, 'location.href')).searchParams.get('mode');
  recordCheck(report, 'correctness', 'Viewer route remains represented in the URL', currentMode === 'viewer', { mode: currentMode });
  recordUntested(report, 'persistence', 'Gender filter selection survives reload', 'Filter persistence across full page reload is not claimed by this Viewer workflow.');
});

export const runViewerQuery = async (context, caseNames) => {
  const reports = [];
  for (const caseName of caseNames) reports.push(await runOutput(context));
  return reports;
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  await executeScenario({ id: 'viewer-query', argv: process.argv.slice(2), runner: runViewerQuery });
}
