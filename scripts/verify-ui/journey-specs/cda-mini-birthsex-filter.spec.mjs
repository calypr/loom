import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import {
  captureBuilderObservation,
  captureJourneyProvenance,
  expect,
  recordAuthoringAttempt,
  observeAuthoringAttempt,
  test,
} from '../helpers/fast-fixtures.mjs';

const TRACE_SCHEMA_VERSION = 1;
const AUTHORING_PREFIX = '/authoring/v2/';
const ADDED_LABEL = 'Extension Value Code';

const isAuthoringResponse = (response) => new URL(response.url()).pathname.includes(AUTHORING_PREFIX);
const authoringPath = (response) => new URL(response.url()).pathname.split(AUTHORING_PREFIX)[1] ?? '';

const waitForPreviewReady = async (page) => {
  await page.waitForFunction(() => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && Boolean(preview.dataset.previewReceiptId);
  }, undefined, { timeout: 45_000 });
};

const waitForProposalReady = async (page) => {
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.getAttribute('data-proposal-status') === 'ready';
  }, undefined, { timeout: 30_000 });
};

const idColumn = (columns) => columns.find((column) => column.label === 'id')?.column;
const extensionColumn = (columns) => columns.find((column) => column.label === ADDED_LABEL)?.column;

test('CDA mini journey adds the birthsex extension and filters to female patients', async ({ fastPage, fastUrl }) => {
  test.setTimeout(120_000);
  fastPage.setDefaultTimeout(30_000);
  fastPage.setDefaultNavigationTimeout(20_000);

  const tracePath = process.env.LOOM_JOURNEY_TRACE;
  expect(tracePath, 'LOOM_JOURNEY_TRACE must be set by run-builder-journey.mjs').toBeTruthy();
  const provenance = captureJourneyProvenance();
  expect(provenance.sourceCommit, 'LOOM_SOURCE_COMMIT must be the tested Git commit').toMatch(/^[0-9a-f]{40}$/);

  const backendCalls = [];
  fastPage.on('response', (response) => {
    if (!isAuthoringResponse(response)) return;
    backendCalls.push({
      method: response.request().method(),
      path: authoringPath(response),
      status: response.status(),
    });
  });

  console.log('[journey] navigation');
  const initialPreviewAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'preview',
    owner: { kind: 'preview', attempt: 'P1' },
    timeout: 45_000,
  });
  await fastPage.goto(fastUrl, { waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  const initialPreviewExchange = await initialPreviewAttempt.settled;
  const initialObservation = await captureBuilderObservation(fastPage);
  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    ...provenance,
    traces: [{
      name: 'journey/cda-mini-birthsex-filter',
      model: 'BuilderJourney',
      initialObservation,
      initialPreview: {
        owner: initialPreviewExchange.owner,
        request: initialPreviewExchange.request,
        response: initialPreviewExchange.response,
        settlement: initialPreviewExchange.settlement,
      },
      actions: [],
      unmodeledEvents: [],
    }],
  };

  const persistTrace = () => {
    trace.traces[0].backendCalls = backendCalls;
    mkdirSync(dirname(tracePath), { recursive: true });
    writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);
  };

  console.log('[journey] add birthsex extension column');
  await fastPage.getByTestId('construction-action-add-columns').click();
  await fastPage.getByRole('tab', { name: 'Other fields' }).click();
  await fastPage.getByRole('button', { name: 'Select Patient.extension[].valueCode', exact: true }).click();
  const addProposalAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'construction-choice-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
  });
  const addCommandAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt: 'P1' },
  });
  const addProposalTrace = recordAuthoringAttempt(fastPage, trace.traces[0].actions, addProposalAttempt, {
    requestLabel: 'Preview',
    requestEvent: 'preview',
    responseLabel: 'PreviewComplete',
    responseErrorLabel: 'PreviewError',
    responseEvent: 'preview-response',
  });
  await fastPage.getByRole('button', { name: /^Add \d+ selected feature$/ }).click();
  const addProposalExchange = await addProposalAttempt.settled;
  await addProposalTrace.settled;
  const addProposal = addProposalExchange.payload;
  const addCommandStarted = await addCommandAttempt.started;
  const addCommandExchange = await addCommandAttempt.settled;
  expect(addCommandExchange.rawResponse.ok(), 'birthsex column persistence').toBe(true);
  const backToTable = fastPage.getByRole('button', { name: 'Back to table' });
  if (await backToTable.isVisible().catch(() => false)) await backToTable.click();
  await waitForPreviewReady(fastPage);

  const addedPreview = addProposal.preview ?? addProposal;
  const addedColumns = addedPreview.columns ?? [];
  const addedRows = addedPreview.rows ?? [];
  const addedLabels = addedColumns.map((column) => column.label);
  expect(Number(addedPreview.rowCount), 'birthsex column row count').toBe(6);
  expect(addedLabels, 'birthsex column labels').toEqual(['id', ADDED_LABEL]);
  expect(addedRows, 'birthsex column rows').toHaveLength(6);
  const addedExtension = extensionColumn(addedColumns);
  expect(addedRows.some((row) => (row[addedExtension] ?? []).includes('F')), 'female birthsex values are present').toBe(true);
  expect(addedRows.some((row) => (row[addedExtension] ?? []).length === 0), 'one patient has no birthsex extension').toBe(true);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(2);
  trace.traces[0].actions.push({
    label: 'AddColumn',
    event: 'add-column',
    owner: addCommandStarted.owner,
    request: addCommandExchange.request,
    response: addCommandExchange.response,
    settlement: addCommandExchange.settlement,
    observation: await captureBuilderObservation(fastPage),
  });

  console.log('[journey] filter female birthsex');
  await fastPage.getByTestId('construction-action-keep-rows').click();
  const editor = fastPage.getByTestId('construction-filter-editor');
  await expect(editor).toBeVisible();
  const column = editor.getByLabel('Column', { exact: true });
  const columnValue = await column.locator('option').evaluateAll((options, label) =>
    options.find((option) => (option.textContent ?? '').includes(label))?.value, ADDED_LABEL);
  expect(columnValue, 'birthsex column must be available to filter').toBeTruthy();
  await column.selectOption(columnValue);
  const condition = editor.getByLabel('Condition', { exact: true });
  await expect(condition).toBeEnabled();
  await condition.selectOption('CONTAINS_TEXT');
  const valueInput = editor.getByRole('textbox', { name: 'Value', exact: true });
  await expect(valueInput).toBeVisible();
  const filterProposalAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'construction-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
  });
  const filterProposalTrace = recordAuthoringAttempt(fastPage, trace.traces[0].actions, filterProposalAttempt, {
    requestLabel: 'Preview',
    requestEvent: 'preview',
    responseLabel: 'PreviewComplete',
    responseErrorLabel: 'PreviewError',
    responseEvent: 'preview-response',
  });
  await valueInput.fill('F');
  const filterProposalStarted = await filterProposalTrace.started;
  const filterProposalExchange = await filterProposalAttempt.settled;
  await filterProposalTrace.settled;
  const filterPreview = filterProposalExchange.payload.preview;
  await waitForProposalReady(fastPage);

  const panel = fastPage.getByTestId('construction-proposal-panel');
  await expect(panel).toContainText('3 rows and 2 columns');
  const proposalId = await panel.getAttribute('data-proposal-id');
  expect(proposalId, 'filter proposal identity').toBeTruthy();

  const filterColumns = filterPreview.columns ?? [];
  const filterRows = filterPreview.rows ?? [];
  const filterLabels = filterColumns.map((column) => column.label);
  const filterId = idColumn(filterColumns);
  const filterExtension = extensionColumn(filterColumns);
  expect(Number(filterPreview.rowCount), 'female filter row count').toBe(3);
  expect(filterLabels, 'female filter labels').toEqual(['id', ADDED_LABEL]);
  expect(filterRows, 'female filter rows').toHaveLength(3);
  expect(new Set(filterRows.map((row) => row[filterId])), 'female patient identities')
    .toEqual(new Set(['cda-mini-patient-001', 'cda-mini-patient-003', 'cda-mini-patient-005']));
  expect(filterRows.every((row) => (row[filterExtension] ?? []).includes('F')), 'filtered rows carry F birthsex').toBe(true);

  console.log('[journey] cancel birthsex filter');
  await panel.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(panel).toHaveCount(0);
  await waitForPreviewReady(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(7);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(2);
  trace.traces[0].actions.push({
    label: 'CancelFilter',
    event: 'cancel-filter',
    owner: filterProposalStarted.owner,
    observation: await captureBuilderObservation(fastPage),
  });

  console.log('[journey] reload after cancel');
  await fastPage.reload({ waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(7);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(2);
  trace.traces[0].unmodeledEvents.push({
    kind: 'reload',
    note: 'Reload after cancelling the birthsex filter preserves the source table.',
  });

  trace.traces[0].outcome = {
    kind: 'table-preview',
    status: 'ready',
    rowCount: 3,
    previewReceiptId: filterPreview.receiptId ?? null,
    columns: filterLabels,
  };
  persistTrace();

  expect(backendCalls.length, 'journey must exercise at least fifteen authoring backend calls').toBeGreaterThanOrEqual(15);
});
