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

const isAuthoringResponse = (response) => new URL(response.url()).pathname.includes(AUTHORING_PREFIX);
const authoringPath = (response) => new URL(response.url()).pathname.split(AUTHORING_PREFIX)[1] ?? '';

const waitForPreviewReady = async (page) => {
  await page.waitForFunction(() => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && Boolean(preview.dataset.previewReceiptId);
  }, undefined, { timeout: 45_000 });
};

const addSelectedFeature = async (page, actions) => {
  const proposalAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'construction-choice-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
  });
  const commandAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt: 'P1' },
  });
  const proposalTrace = recordAuthoringAttempt(page, actions, proposalAttempt, {
    requestLabel: 'Preview',
    requestEvent: 'preview',
    responseLabel: 'PreviewComplete',
    responseErrorLabel: 'PreviewError',
    responseEvent: 'preview-response',
  });
  await page.getByRole('button', { name: /^Add \d+ selected feature$/ }).click();
  const proposalExchange = await proposalAttempt.settled;
  await proposalTrace.settled;
  expect(proposalExchange.rawResponse.ok(), 'add-column proposal preview').toBe(true);
  const commandStarted = await commandAttempt.started;
  const commandExchange = await commandAttempt.settled;
  expect(commandExchange.rawResponse.ok(), 'add-column command persistence').toBe(true);
  return {
    preview: proposalExchange.payload.preview,
    command: {
      ...commandStarted,
      response: commandExchange.response,
      settlement: commandExchange.settlement,
    },
  };
};

test('builder journey adds a column and previews exact values', async ({ fastPage, fastUrl }) => {
  test.setTimeout(90_000);
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
      name: 'journey/add-column-preview',
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

  const isCDAMini = process.env.LOOM_CDA_MINI_FIXTURE === '1';
  const isCDA = process.env.LOOM_FAST_PROJECT === 'loom_dev_cda_fhir';
  const fieldButton = isCDA || isCDAMini ? 'Select Patient.identifier[].value' : 'Select Patient.birthDate';
  const addedLabel = isCDA || isCDAMini ? 'Identifier Value' : 'Birth Date';
  console.log(`[journey] add ${addedLabel}`);
  await fastPage.getByTestId('construction-action-add-columns').click();
  await fastPage.getByRole('tab', { name: 'Other fields' }).click();
  await fastPage.getByRole('button', { name: fieldButton, exact: true }).click();
  const addition = await addSelectedFeature(fastPage, trace.traces[0].actions);
  const backToTable = fastPage.getByRole('button', { name: 'Back to table' });
  if (await backToTable.isVisible().catch(() => false)) await backToTable.click();
  await expect(fastPage.getByRole('columnheader', { name: addedLabel, exact: true })).toBeVisible();
  trace.traces[0].actions.push({
    label: 'AddColumn',
    event: 'add-column',
    owner: addition.command.owner,
    request: addition.command.request,
    response: addition.command.response,
    settlement: addition.command.settlement,
    observation: await captureBuilderObservation(fastPage),
  });

  const proposalPreview = addition.preview;
  const columns = proposalPreview.columns ?? [];
  const rows = proposalPreview.rows ?? [];
  const rowCount = Number(proposalPreview.rowCount ?? rows.length);
  const labels = columns.map((column) => column.label);
  trace.traces[0].backendCalls = backendCalls;
  trace.traces[0].outcome = {
    kind: 'table-preview',
    status: 'ready',
    rowCount,
    previewReceiptId: proposalPreview.receiptId ?? null,
    columns: labels,
  };
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);

  const idColumn = columns.find((column) => column.label === 'id')?.column;
  const birthDateColumn = columns.find((column) => column.label === 'Birth Date')?.column;
  const addedColumn = columns.find((column) => column.label === addedLabel)?.column;
  if (isCDAMini) {
    expect(rowCount, 'CDA mini add-column preview row count').toBe(6);
    expect(labels, 'CDA mini add-column preview labels').toEqual(['id', 'Identifier Value']);
    expect(columns, 'CDA mini add-column preview columns').toHaveLength(2);
    expect(rows, 'CDA mini add-column preview rows').toHaveLength(6);
    expect(new Set(rows.map((row) => row[idColumn])).size, 'CDA mini add-column preview ids are unique').toBe(6);
    expect(rows.some((row) => (row[addedColumn] ?? null) !== null), 'CDA mini identifier values are present').toBe(true);
    await expect(fastPage.getByRole('columnheader')).toHaveCount(2);
  } else if (isCDA) {
    expect(rowCount, 'CDA add-column preview row count at the default limit').toBe(25);
    expect(labels, 'CDA add-column preview labels').toEqual(['id', 'Identifier Value']);
    expect(columns, 'CDA add-column preview columns').toHaveLength(2);
    expect(rows, 'CDA add-column preview rows').toHaveLength(25);
    expect(new Set(rows.map((row) => row[idColumn])).size, 'CDA add-column preview ids are unique').toBe(25);
    expect(rows.some((row) => (row[addedColumn] ?? null) !== null), 'CDA identifier values are present').toBe(true);
    await expect(fastPage.getByRole('columnheader')).toHaveCount(2);
  } else {
    expect(rowCount, 'add-column preview row count').toBe(2);
    expect(labels, 'add-column preview labels').toEqual([
      'id',
      'name[].family [0]',
      'name[].family [1]',
      'name__count',
      'gender',
      'Birth Date',
    ]);
    const genderColumn = columns.find((column) => column.label === 'gender')?.column;
    const byId = Object.fromEntries(rows.map((row) => [row[idColumn], row]));
    expect(byId['dev-patient-001']?.[birthDateColumn]).toBe('1980-01-02');
    expect(byId['dev-patient-002']?.[birthDateColumn]).toBe('1985-03-04');
    expect(byId['dev-patient-001']?.[genderColumn]).toBe('female');
    expect(byId['dev-patient-002']?.[genderColumn] ?? null).toBeNull();
  }
  expect(backendCalls.length, 'journey must exercise ten to twenty authoring backend calls').toBeGreaterThanOrEqual(10);
  expect(backendCalls.length, 'journey must exercise ten to twenty authoring backend calls').toBeLessThanOrEqual(20);
});
