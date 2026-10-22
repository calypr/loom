import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { expect, test } from '../helpers/fast-fixtures.mjs';
import { addField, isCdaDataset } from '../helpers/cda-journey-fields.mjs';

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

const waitForProposalSettled = async (page, timeout = 30_000) => {
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  }, undefined, { timeout });
};

const writeTrace = (tracePath, trace) => {
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);
};

test('builder journey pivots categories into columns and previews the result', async ({ fastPage, fastUrl }) => {
  test.setTimeout(90_000);
  fastPage.setDefaultTimeout(30_000);
  fastPage.setDefaultNavigationTimeout(20_000);

  const tracePath = process.env.LOOM_JOURNEY_TRACE;
  expect(tracePath, 'LOOM_JOURNEY_TRACE must be set by run-builder-journey.mjs').toBeTruthy();
  const sourceCommit = String(process.env.LOOM_JOURNEY_SOURCE_COMMIT ?? '').trim();
  expect(sourceCommit, 'LOOM_JOURNEY_SOURCE_COMMIT must be the tested Git commit').toMatch(/^[0-9a-f]{40}$/);

  const backendCalls = [];
  const proposalResponses = [];
  fastPage.on('response', (response) => {
    if (!isAuthoringResponse(response)) return;
    backendCalls.push({
      method: response.request().method(),
      path: authoringPath(response),
      status: response.status(),
    });
    if (authoringPath(response) === 'construction-proposals') proposalResponses.push(response);
  });

  console.log('[journey] navigation');
  await fastPage.goto(fastUrl, { waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  console.log('[journey] initial preview ready');
  const cda = isCdaDataset();
  let cdaRowCount = 0;
  let cdaLabels = [];
  if (cda) {
    await addField(fastPage, 'Select Observation.subject.reference');
    await addField(fastPage, 'Select Observation.valueQuantity.unit');
    const added = await addField(fastPage, 'Select Observation.valueQuantity.value');
    cdaRowCount = Number(added.rowCount ?? added.rows?.length ?? 0);
    cdaLabels = (added.columns ?? []).map((column) => column.label);
    expect(cdaRowCount, 'CDA pivot source rows').toBeGreaterThan(0);
    expect(cdaLabels, 'CDA pivot source columns').toEqual(expect.arrayContaining(['Subject Reference', 'Value Quantity Unit', 'Value Quantity Value']));
  }

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    sourceCommit,
    traces: [{
      name: 'journey/group-pivot-preview',
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

  if (cda) {
    trace.traces[0].actions.push(
      { label: 'AddColumn', event: 'add-column', observation: observation({ status: 'reshaping' }) },
      { label: 'AddColumnAgain', event: 'add-column', observation: observation({ status: 'reshaping' }) },
    );
  }

  console.log('[journey] open pivot form');
  await fastPage.getByTestId('construction-rows-settings-trigger').click();
  await fastPage.getByTestId('construction-row-intent-reshape').click();
  await fastPage.getByTestId('construction-action-table-pivot-rows').click();

  const dialog = fastPage.getByRole('dialog');
  if (cda) {
    await dialog.getByLabel('Pivot group Subject Reference').check();
    await dialog.getByLabel('Pivot category field').selectOption({ label: 'Observation · Value Quantity Unit' });
    await dialog.getByLabel('Pivot values field').selectOption({ label: 'Observation · Value Quantity Value (decimal)' });
  } else {
    await dialog.getByLabel('Pivot group id').check();
    await dialog.getByLabel('Pivot category field').selectOption({ label: 'gender' });
    await dialog.getByLabel('Pivot values field').selectOption({ label: 'name__count (integer)' });
  }
  console.log('[journey] pivot configured');

  await waitForProposalSettled(fastPage, cda ? 120_000 : 30_000);
  const proposalResponse = proposalResponses.at(-1);
  expect(proposalResponse, 'pivot must produce a construction-proposals response').toBeTruthy();
  const proposalStatus = proposalResponse.status();
  const proposalBody = await proposalResponse.json().catch(() => null);
  const proposalPreview = proposalBody?.preview ?? proposalBody;

  trace.traces[0].actions.push({
    label: 'PivotDirect',
    event: 'pivot',
    observation: observation({ status: 'pivot-ready' }),
  });
  trace.traces[0].actions.push({
    label: 'Preview',
    event: 'preview',
    observation: observation({ status: 'previewing' }),
  });

  if (proposalStatus >= 400) {
    const errorClass = proposalBody?.error?.code
      ?? proposalBody?.diagnostics?.[0]?.code
      ?? `HTTP_${proposalStatus}`;
    const requestId = proposalBody?.error?.requestId
      ?? proposalBody?.diagnostics?.[0]?.requestId
      ?? null;
    trace.traces[0].actions.push({
      label: 'PreviewError',
      event: 'preview-response',
      observation: observation({ status: 'error' }),
    });
    trace.traces[0].backendCalls = backendCalls;
    trace.traces[0].outcome = {
      kind: 'pivot-preview',
      status: 'error',
      httpStatus: proposalStatus,
      errorClass,
      requestId,
    };
    writeTrace(tracePath, trace);
    expect(proposalStatus, 'pivot preview must return HTTP 200').toBe(200);
    return;
  }

  const rowCount = Number(proposalPreview?.rowCount ?? proposalPreview?.rows?.length ?? 0);
  trace.traces[0].actions.push({
    label: 'PreviewComplete',
    event: 'preview-response',
    observation: observation({
      status: 'ready',
      previewReceiptPresent: true,
      rowsPresent: true,
    }),
  });
  trace.traces[0].backendCalls = backendCalls;
  trace.traces[0].outcome = {
    kind: 'pivot-preview',
    status: 'ready',
    rowCount,
    previewReceiptId: proposalPreview?.receiptId ?? null,
  };
  writeTrace(tracePath, trace);

  if (cda) {
    expect(rowCount, 'CDA pivot preview row count').toBe(cdaRowCount);
    expect(proposalPreview?.columns?.length ?? 0, 'CDA pivot preview has category columns').toBeGreaterThan(1);
  } else {
    expect(rowCount, 'pivot preview row count').toBe(2);
    expect(proposalPreview?.columns?.map((column) => column.column), 'pivot preview columns')
      .toEqual(expect.arrayContaining(['id', 'female']));
    const rowsById = Object.fromEntries((proposalPreview?.rows ?? []).map((row) => [row.id, row]));
    expect(rowsById['dev-patient-001']?.female, 'female category value').toBe(2);
    expect(rowsById['dev-patient-002']?.female ?? null, 'missing female category value').toBeNull();
  }
  expect(backendCalls.length, 'journey must exercise at least ten authoring backend calls').toBeGreaterThanOrEqual(10);
});
