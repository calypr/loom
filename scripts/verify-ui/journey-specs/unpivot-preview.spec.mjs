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

const waitForProposalSettled = async (page) => {
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  }, undefined, { timeout: 30_000 });
};

const writeTrace = (tracePath, trace) => {
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);
};

test('builder journey stacks columns into rows and previews exact values', async ({ fastPage, fastUrl }) => {
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
  const cda = isCdaDataset();
  const setupActions = [];
  let cdaRowCount = 0;
  if (cda) {
    await addFieldWithCommandTrace(fastPage, 'Select Observation.status', setupActions, addField);
    const added = await addFieldWithCommandTrace(fastPage, 'Select Observation.valueString', setupActions, addField);
    cdaRowCount = Number(added.rowCount ?? added.rows?.length ?? 0);
    expect(cdaRowCount, 'CDA unpivot source rows').toBeGreaterThan(0);
  }

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    ...provenance,
    traces: [{
      name: 'journey/unpivot-preview',
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

  console.log('[journey] configure unpivot');
  const proposalAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'construction-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
    timeout: 30_000,
  });
  await fastPage.getByTestId('construction-rows-settings-trigger').click();
  await fastPage.getByTestId('construction-row-intent-reshape').click();
  await fastPage.getByTestId('construction-action-unpivot-rows').click();
  const editor = fastPage.getByTestId('construction-reshape-unpivot');
  await expect(editor).toBeVisible();
  if (cda) {
    await editor.getByRole('checkbox', { name: 'Unpivot Status', exact: true }).check();
    await editor.getByRole('checkbox', { name: 'Unpivot Value String', exact: true }).check();
  } else {
    await editor.getByRole('checkbox', { name: 'Unpivot name[].family [0]', exact: true }).check();
    await editor.getByRole('checkbox', { name: 'Unpivot name[].family [1]', exact: true }).check();
  }
  const proposalStarted = await proposalAttempt.started;
  trace.traces[0].actions.push({
    label: 'Preview',
    event: 'preview',
    owner: proposalStarted.owner,
    request: proposalStarted.request,
    observation: proposalStarted.requestObservation,
  });
  await waitForProposalSettled(fastPage);

  const proposalExchange = await proposalAttempt.settled;
  const proposalResponse = proposalExchange.rawResponse;
  expect(proposalResponse, 'unpivot must produce a construction-proposals response').toBeTruthy();
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
    throw new Error(`Cannot classify unpivot preview settlement from observed panel status ${proposalObservation.status}`);
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
    trace.traces[0].backendCalls = backendCalls;
    trace.traces[0].outcome = {
      kind: 'unpivot-preview',
      status: 'error',
      httpStatus: proposalStatus,
      errorClass,
      requestId,
    };
  } else {
    const rowCount = Number(proposalPreview?.rowCount ?? proposalPreview?.rows?.length ?? 0);
    trace.traces[0].actions.push({
      label: 'PreviewComplete',
      event: 'preview-response',
      owner: proposalStarted.owner,
      request: proposalExchange.request,
      response: proposalExchange.response,
      settlement: proposalExchange.settlement,
      observation: combineAuthoringObservation(proposalExchange, proposalObservation),
    });
    trace.traces[0].outcome = {
      kind: 'unpivot-preview',
      status: 'ready',
      rowCount,
      previewReceiptId: proposalPreview?.receiptId ?? null,
      columns: proposalPreview?.columns?.map((column) => column.label) ?? [],
    };
  }
  trace.traces[0].backendCalls = backendCalls;
  writeTrace(tracePath, trace);

  if (proposalStatus >= 400) {
    expect(proposalStatus, 'unpivot preview must return HTTP 200').toBe(200);
    return;
  }

  const rowCount = Number(proposalPreview?.rowCount ?? proposalPreview?.rows?.length ?? 0);
  if (cda) {
    expect(rowCount, 'CDA unpivot preview row count').toBe(2 * cdaRowCount);
    expect(proposalPreview?.columns?.map((column) => column.label), 'CDA unpivot preview columns')
      .toEqual(expect.arrayContaining(['key', 'value']));
  } else {
    expect(rowCount, 'unpivot preview row count').toBe(4);
    expect(proposalPreview?.columns?.map((column) => column.label), 'unpivot preview columns')
      .toEqual(expect.arrayContaining(['id', 'key', 'value']));
    const ids = (proposalPreview?.rows ?? []).map((row) =>
      Object.values(row).find((value) => String(value).startsWith('dev-patient-')));
    expect(ids, 'unpivot rows cover both source patients twice').toEqual(
      expect.arrayContaining(['dev-patient-001', 'dev-patient-001', 'dev-patient-002', 'dev-patient-002']),
    );
  }
  expect(backendCalls.length, 'journey must exercise ten to twenty authoring backend calls').toBeGreaterThanOrEqual(10);
});
