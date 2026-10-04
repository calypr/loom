import { executeScenario, runBrowserCase } from './common.mjs';
import { click, evaluate, fill, injectReadFaultOnce, recordBrowserTiming, reload, waitFor, waitForCDPEvent } from './browser.mjs';
import { recordCheck } from './report.mjs';
import { finishFirstTableAddColumnsObserver, installFirstTableAddColumnsObserver, waitForAddColumnsAction } from '../loom-dev.mjs';
import { addPatientTableRoot, configurePatientColumns, createBlankExplorer, previewPatientRows } from './workflows.mjs';

const ready = "document.body.innerText.includes('DATASET WORKSPACE')";
const tableCount = "document.querySelectorAll('[data-testid^=\"construction-table-\"]').length";
const successfulResponse = (cdp, path) => waitForCDPEvent(cdp, 'Network.responseReceived', (event) => new URL(event.response.url).pathname.endsWith(path) && event.response.status >= 200 && event.response.status < 300, 30000);

const prepare = async (cdp, context, report, label) => {
  const created = await createBlankExplorer(cdp, context.target, context.runID, label, report);
  report.target.explorer = created.explorer;
  await addPatientTableRoot(cdp, report);
  await configurePatientColumns(cdp, report);
  return created;
};

const recompile = (context) => runBrowserCase(context, 'builder-controls', 'recompile', async ({ cdp, report, faults }) => {
  const created = await createBlankExplorer(cdp, context.target, context.runID, 'recompile', report);
  report.target.explorer = created.explorer;
  const fault = await injectReadFaultOnce(cdp, {
    method: 'POST', pathEndsWith: '/reconcile',
    rejection: { status: 422, body: { code: 'VERIFY_COMPILE_REJECTED', message: 'Controlled compilation rejection for the Recompile regression.', diagnostics: [{ severity: 'error', code: 'VERIFY_COMPILE_REJECTED', message: 'Controlled compilation rejection.' }] } },
  });
  faults.push(fault);
  await fill(cdp, '#first-table-name', 'Patients');
  await recordBrowserTiming(report, cdp, {
    name: 'failed compilation exposes Recompile',
    action: () => click(cdp, 'button', { name: 'Choose Patient rows' }),
    after: "[...document.querySelectorAll('button')].some((button)=>button.innerText.trim()==='Recompile') && document.querySelector('[role=alert]')",
    timeout: 30000,
  });
  await fault.wait(5000);
  const requested = waitForCDPEvent(cdp, 'Network.requestWillBeSent', (event) => new URL(event.request.url).pathname.endsWith('/reconcile'), 5000).then(() => true, () => false);
  const compiled = successfulResponse(cdp, '/reconcile');
  await click(cdp, 'button', { name: 'Recompile' });
  const invoked = await requested;
  recordCheck(report, 'usability', 'Recompile invokes the backend compiler completed', invoked);
  if (!invoked) return;
  recordCheck(report, 'correctness', 'Recompile returned a successful compilation response', (await compiled).response.status === 200);
  await previewPatientRows(cdp, report);
});

const firstTable = (context) => runBrowserCase(context, 'builder-controls', 'first-table', async ({ cdp, report }) => {
  const created = await createBlankExplorer(cdp, context.target, context.runID, 'first-table', report);
  report.target.explorer = created.explorer;
  await fill(cdp, '#first-table-name', 'Patients');
  const patientTableReady = `document.body.innerText.includes('DATASET WORKSPACE') && document.body.innerText.includes('Patients') && Boolean(document.querySelector('[data-testid="construction-workspace"]')) && Boolean(document.querySelector('button[aria-label^="Select Patient ID"]'))`;
  await waitFor(cdp, "Boolean(document.querySelector('button[aria-label=\"Choose Patient rows\"]:not(:disabled)'))", 30000);
  await installFirstTableAddColumnsObserver(cdp, patientTableReady);
  let availabilityEvents;
  let observerInstalled = true;
  try {
    await recordBrowserTiming(report, cdp, {
      name: 'create verified-ID Patient first table with accepted preview',
      action: () => click(cdp, 'button', { name: 'Choose Patient rows' }),
      settle: async () => {
        await waitForAddColumnsAction(cdp, patientTableReady);
        availabilityEvents = await finishFirstTableAddColumnsObserver(cdp);
        observerInstalled = false;
      },
      after: `(${patientTableReady}) && Boolean(document.querySelector('[data-testid="construction-action-add-columns"]:not(:disabled)'))`,
      timeout: 30000,
      budget: 5000,
    });
  } finally {
    if (observerInstalled) {
      try {
        availabilityEvents = await finishFirstTableAddColumnsObserver(cdp);
      } catch (error) {
        report.errors.push({ kind: 'observer-cleanup-error', message: error instanceof Error ? error.message : String(error) });
      }
      observerInstalled = false;
    }
    if (availabilityEvents) report.target.firstTableAddColumnsAvailability = availabilityEvents;
  }

  const prematureEvents = availabilityEvents.filter((event) => !event.acceptedCurrentPreview);
  recordCheck(report, 'correctness', 'Add columns stays disabled until a current-draft preview is accepted', prematureEvents.length === 0, {
    enabledEventsBeforePreview: prematureEvents.length,
    events: prematureEvents,
  });
  const acceptedMutation = availabilityEvents.find((event) => event.source === 'mutation' && event.acceptedCurrentPreview);
  recordCheck(report, 'correctness', 'Add columns becomes enabled after the accepted current-draft preview', Boolean(acceptedMutation), {
    acceptedPreviewEvent: acceptedMutation ?? null,
    eventCount: availabilityEvents.length,
  });
  const patientIDControl = await evaluate(cdp, `document.querySelector('button[aria-label^="Select Patient ID"]')?.getAttribute('aria-label') || ''`);
  recordCheck(report, 'correctness', 'first Patient table uses its verified ID field', patientIDControl.startsWith('Select Patient ID'), {
    controlLabel: patientIDControl,
  });
  await previewPatientRows(cdp, report);

  await recordBrowserTiming(report, cdp, {
    name: 'open Add columns from verified-ID first table',
    action: () => click(cdp, 'button', { includes: 'Add columns:' }),
    after: "Boolean(document.querySelector('[aria-label=\"Add columns editor\"]'))",
    timeout: 5000,
    budget: 5000,
  });
  recordCheck(report, 'usability', 'Add columns opens from the verified-ID first table',
    await evaluate(cdp, `Boolean(document.querySelector('[aria-label="Add columns editor"]'))`));

  await recordBrowserTiming(report, cdp, {
    name: 'close Add columns on verified-ID first table',
    action: () => click(cdp, 'button', { name: 'Close operation editor' }),
    after: "!document.querySelector('[aria-label=\"Add columns editor\"]') && Boolean(document.querySelector('[data-testid=\"construction-action-add-columns\"]:not(:disabled)'))",
    timeout: 5000,
    budget: 5000,
  });
  recordCheck(report, 'usability', 'Add columns closes from the verified-ID first table',
    !await evaluate(cdp, `Boolean(document.querySelector('[aria-label="Add columns editor"]'))`));
});

const tables = (context) => runBrowserCase(context, 'builder-controls', 'tables', async ({ cdp, report }) => {
  await prepare(cdp, context, report, 'controls');
  await recordBrowserTiming(report, cdp, {
    name: 'duplicate configured table',
    action: () => click(cdp, 'button', { name: 'Duplicate table' }),
    after: "[...document.querySelectorAll('[data-testid^=\"construction-table-\"]')].some((button)=>button.innerText.includes('Patients copy')) && Number(" + tableCount + ")===2",
  });
  const renamed = successfulResponse(cdp, '/commands');
  await recordBrowserTiming(report, cdp, {
    name: 'rename duplicated table',
    action: () => {
      cdp.nextDialogResponse = { accept: true, promptText: 'Renamed Patients' };
      return click(cdp, 'button', { name: 'Rename Patients copy' });
    },
    after: "[...document.querySelectorAll('[data-testid^=\"construction-table-\"]')].some((button)=>button.innerText.includes('Renamed Patients'))",
    settle: () => renamed,
  });
  await reload(cdp, ready + ' && ' + tableCount + '===2');
  recordCheck(report, 'persistence', 'duplicated and renamed tables survive reload', await evaluate(cdp, "[...document.querySelectorAll('[data-testid^=\"construction-table-\"]')].some((button)=>button.innerText.includes('Renamed Patients'))"));
  await click(cdp, 'button', { includes: 'Renamed Patients' });
  await recordBrowserTiming(report, cdp, {
    name: 'delete duplicated table',
    action: () => click(cdp, 'button', { name: 'Delete table' }),
    after: tableCount + "===1 && ![...document.querySelectorAll('[data-testid^=\"construction-table-\"]')].some((button)=>button.innerText.includes('Renamed Patients'))",
  });
  await reload(cdp, ready + ' && ' + tableCount + '===1');
  recordCheck(report, 'persistence', 'deleted table stays absent after reload', await evaluate(cdp, "![...document.querySelectorAll('[data-testid^=\"construction-table-\"]')].some((button)=>button.innerText.includes('Renamed Patients'))"));
  await previewPatientRows(cdp, report);
  await click(cdp, 'summary', { name: 'New explorer' });
  const title = 'Copy ' + context.runID;
  await fill(cdp, '#new-explorer-name', title);
  await click(cdp, 'label', { includes: 'Start with a copy of the current explorer' });
  await recordBrowserTiming(report, cdp, {
    name: 'copy configured Explorer',
    action: () => click(cdp, 'button', { name: 'Create copy' }),
    after: "document.querySelector('select[aria-label=\"Explorer\"] option:checked')?.textContent.trim()===" + JSON.stringify(title) + " && document.querySelector('button[aria-label^=\"Select Patient ID\"]')",
  });
  report.target.sourceExplorer = report.target.explorer;
  report.target.explorer = await evaluate(cdp, "document.querySelector('select[aria-label=\"Explorer\"]')?.value");
  await previewPatientRows(cdp, report);
  await reload(cdp, ready + " && document.querySelector('button[aria-label^=\"Select Patient ID\"]')");
  recordCheck(report, 'persistence', 'copied Explorer retains configured fields after reload', await evaluate(cdp, "document.querySelector('select[aria-label=\"Explorer\"] option:checked')?.textContent.trim()===" + JSON.stringify(title)));
  await recordBrowserTiming(report, cdp, {
    name: 'delete the last configured table',
    action: () => click(cdp, 'button', { name: 'Delete table' }),
    after: "document.body.innerText.includes('Build your first table') && " + tableCount + '===0',
  });
  await reload(cdp, "document.body.innerText.includes('Build your first table')");
  recordCheck(report, 'persistence', 'deleting the last table persists an empty workspace', await evaluate(cdp, tableCount + '===0'));
  await addPatientTableRoot(cdp, report);
  await configurePatientColumns(cdp, report);
  await previewPatientRows(cdp, report);
});

export const runBuilderControls = async (context, cases) => {
  const reports = [];
  for (const name of cases) reports.push(await (name === 'recompile' ? recompile(context) : name === 'first-table' ? firstTable(context) : tables(context)));
  return reports;
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  await executeScenario({ id: 'builder-controls', argv: process.argv.slice(2), runner: runBuilderControls, mutating: true });
}
