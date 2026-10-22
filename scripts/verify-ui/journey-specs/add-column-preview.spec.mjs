import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { expect, test } from '../helpers/fast-fixtures.mjs';

const TRACE_SCHEMA_VERSION = 1;
const AUTHORING_PREFIX = '/authoring/v2/';

const observation = (overrides = {}) => ({
  status: 'idle',
  canApply: false,
  proposalIdPresent: false,
  previewReceiptPresent: false,
  rowsPresent: false,
  receiptPresent: false,
  ...overrides,
});

const isAuthoringResponse = (response) => new URL(response.url()).pathname.includes(AUTHORING_PREFIX);
const authoringPath = (response) => new URL(response.url()).pathname.split(AUTHORING_PREFIX)[1] ?? '';

const waitForPreviewReady = async (page) => {
  await page.waitForFunction(() => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && Boolean(preview.dataset.previewReceiptId);
  }, undefined, { timeout: 45_000 });
};

const addSelectedFeature = async (page) => {
  const proposal = page.waitForResponse((response) => {
    if (response.request().method() !== 'POST' || !isAuthoringResponse(response)) return false;
    return authoringPath(response) === 'construction-choice-proposals';
  }, { timeout: 30_000 });
  const commands = page.waitForResponse((response) => {
    if (response.request().method() !== 'POST' || !isAuthoringResponse(response)) return false;
    return authoringPath(response) === 'commands';
  }, { timeout: 30_000 });
  await page.getByRole('button', { name: /^Add \d+ selected feature$/ }).click();
  const proposalResponse = await proposal;
  const commandsResponse = await commands;
  expect(proposalResponse.ok(), 'add-column proposal preview').toBe(true);
  expect(commandsResponse.ok(), 'add-column command persistence').toBe(true);
  return (await proposalResponse.json()).preview;
};

test('builder journey adds a column and previews exact values', async ({ fastPage, fastUrl }) => {
  test.setTimeout(90_000);
  fastPage.setDefaultTimeout(30_000);
  fastPage.setDefaultNavigationTimeout(20_000);

  const tracePath = process.env.LOOM_JOURNEY_TRACE;
  expect(tracePath, 'LOOM_JOURNEY_TRACE must be set by run-builder-journey.mjs').toBeTruthy();
  const sourceCommit = String(process.env.LOOM_JOURNEY_SOURCE_COMMIT ?? '').trim();
  expect(sourceCommit, 'LOOM_JOURNEY_SOURCE_COMMIT must be the tested Git commit').toMatch(/^[0-9a-f]{40}$/);

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
  await fastPage.goto(fastUrl, { waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    sourceCommit,
    traces: [{
      name: 'journey/add-column-preview',
      model: 'BuilderJourney',
      initialObservation: observation(),
      actions: [{
        label: 'SelectRows',
        event: 'select-rows',
        observation: observation({ status: 'selected' }),
      }],
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
  const proposalPreview = await addSelectedFeature(fastPage);
  const backToTable = fastPage.getByRole('button', { name: 'Back to table' });
  if (await backToTable.isVisible().catch(() => false)) await backToTable.click();
  await expect(fastPage.getByRole('columnheader', { name: addedLabel, exact: true })).toBeVisible();
  trace.traces[0].actions.push({
    label: 'AddColumn',
    event: 'add-column',
    observation: observation({ status: 'reshaping' }),
  });
  trace.traces[0].actions.push({
    label: 'Preview',
    event: 'preview',
    observation: observation({ status: 'previewing' }),
  });
  trace.traces[0].actions.push({
    label: 'PreviewComplete',
    event: 'preview-response',
    observation: observation({
      status: 'ready',
      previewReceiptPresent: true,
      rowsPresent: true,
    }),
  });

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
