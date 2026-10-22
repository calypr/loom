import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import {
  addFieldWithCommandTrace,
  captureBuilderObservation,
  captureJourneyProvenance,
  combineAuthoringObservation,
  expect,
  observeAuthoringAttempt,
  test,
} from '../helpers/fast-fixtures.mjs';
import { addField, isCdaDataset } from '../helpers/cda-journey-fields.mjs';

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
  console.log('[journey] initial preview ready');
  const cda = isCdaDataset();
  const setupActions = [];
  let cdaRowCount = 0;
  let cdaLabels = [];
  if (cda) {
    await addFieldWithCommandTrace(fastPage, 'Select Observation.subject.reference', setupActions, addField);
    await addFieldWithCommandTrace(fastPage, 'Select Observation.valueQuantity.unit', setupActions, addField);
    const added = await addFieldWithCommandTrace(fastPage, 'Select Observation.valueQuantity.value', setupActions, addField);
    cdaRowCount = Number(added.rowCount ?? added.rows?.length ?? 0);
    cdaLabels = (added.columns ?? []).map((column) => column.label);
    expect(cdaRowCount, 'CDA pivot source rows').toBeGreaterThan(0);
    expect(cdaLabels, 'CDA pivot source columns').toEqual(expect.arrayContaining(['Subject Reference', 'Value Quantity Unit', 'Value Quantity Value']));
  }

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    ...provenance,
    traces: [{
      name: 'journey/group-pivot-preview',
      model: 'BuilderJourney',
      initialObservation,
      initialPreview: {
        owner: initialPreviewExchange.owner,
        request: initialPreviewExchange.request,
        response: initialPreviewExchange.response,
        settlement: initialPreviewExchange.settlement,
      },
      actions: setupActions,
      unmodeledEvents: [],
    }],
  };

  console.log('[journey] open pivot form');
  const proposalAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'construction-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
    timeout: cda ? 120_000 : 30_000,
  });
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
  const proposalStarted = await proposalAttempt.started;
  trace.traces[0].actions.push({
    label: 'Preview',
    event: 'preview',
    owner: proposalStarted.owner,
    request: proposalStarted.request,
    observation: proposalStarted.requestObservation,
  });

  await waitForProposalSettled(fastPage, cda ? 120_000 : 30_000);
  const proposalExchange = await proposalAttempt.settled;
  const proposalResponse = proposalExchange.rawResponse;
  expect(proposalResponse, 'pivot must produce a construction-proposals response').toBeTruthy();
  const proposalStatus = proposalExchange.response.status;
  const proposalBody = proposalExchange.payload;
  const proposalPreview = proposalBody?.preview ?? proposalBody;
  const proposalObservation = await captureBuilderObservation(fastPage);
  const typedPreviewError = Boolean(proposalExchange.response.errorCode)
    || proposalExchange.response.proposalPreviewStatus === 'NEEDS_REPAIR';
  const observedPreviewError = ['error', 'needs-repair'].includes(proposalObservation.status) || typedPreviewError;
  const observedPreviewReady = proposalObservation.status === 'ready'
    && proposalExchange.response.proposalPreviewStatus !== 'NEEDS_REPAIR'
    && !proposalExchange.response.errorCode;
  if (proposalStatus < 400 && !observedPreviewError && !observedPreviewReady) {
    throw new Error(`Cannot classify pivot preview settlement from observed panel status ${proposalObservation.status}`);
  }

  const previewStatus = proposalStatus >= 400 || observedPreviewError ? 'error' : 'ready';
  if (previewStatus === 'error') {
    const errorClass = proposalBody?.error?.code
      ?? proposalBody?.diagnostics?.[0]?.code
      ?? (proposalStatus >= 400 ? `HTTP_${proposalStatus}` : null);
    const requestId = proposalBody?.error?.requestId
      ?? proposalBody?.diagnostics?.[0]?.requestId
      ?? proposalExchange.response.requestId;
    trace.traces[0].actions.push({
      label: 'PreviewError',
      event: 'preview-response',
      owner: proposalStarted.owner,
      request: proposalExchange.request,
      response: proposalExchange.response,
      settlement: proposalExchange.settlement,
      observation: combineAuthoringObservation(proposalExchange, proposalObservation),
    });
    trace.traces[0].outcome = {
      kind: 'pivot-preview',
      status: 'error',
      httpStatus: proposalStatus,
      errorClass,
      requestId,
    };
  } else {
    trace.traces[0].actions.push({
      label: 'PreviewComplete',
      event: 'preview-response',
      owner: proposalStarted.owner,
      request: proposalExchange.request,
      response: proposalExchange.response,
      settlement: proposalExchange.settlement,
      observation: combineAuthoringObservation(proposalExchange, proposalObservation),
    });
    const rowCount = Number(proposalPreview?.rowCount ?? proposalPreview?.rows?.length ?? 0);
    trace.traces[0].outcome = {
      kind: 'pivot-preview',
      status: 'ready',
      rowCount,
      previewReceiptId: proposalPreview?.receiptId ?? null,
    };
  }
  trace.traces[0].backendCalls = backendCalls;
  writeTrace(tracePath, trace);

  if (proposalStatus >= 400) {
    expect(proposalStatus, 'pivot preview must return HTTP 200').toBe(200);
    return;
  }

  const rowCount = Number(proposalPreview?.rowCount ?? proposalPreview?.rows?.length ?? 0);
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
