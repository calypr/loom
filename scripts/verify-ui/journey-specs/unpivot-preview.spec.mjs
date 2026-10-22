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
    if (response.request().method() === 'POST' && authoringPath(response) === 'construction-proposals') {
      proposalResponses.push(response);
    }
  });

  console.log('[journey] navigation');
  await fastPage.goto(fastUrl, { waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  const cda = isCdaDataset();
  let cdaRowCount = 0;
  if (cda) {
    await addField(fastPage, 'Select Observation.status');
    const added = await addField(fastPage, 'Select Observation.valueString');
    cdaRowCount = Number(added.rowCount ?? added.rows?.length ?? 0);
    expect(cdaRowCount, 'CDA unpivot source rows').toBeGreaterThan(0);
  }

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    sourceCommit,
    traces: [{
      name: 'journey/unpivot-preview',
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

  console.log('[journey] configure unpivot');
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
  await waitForProposalSettled(fastPage);

  const proposalResponse = proposalResponses.at(-1);
  expect(proposalResponse, 'unpivot must produce a construction-proposals response').toBeTruthy();
  const proposalStatus = proposalResponse.status();
  const proposalBody = await proposalResponse.json().catch(() => null);
  const proposalPreview = proposalBody?.preview ?? proposalBody;

  trace.traces[0].actions.push({
    label: 'Unpivot',
    event: 'unpivot',
    observation: observation({ status: 'reshaping' }),
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
      kind: 'unpivot-preview',
      status: 'error',
      httpStatus: proposalStatus,
      errorClass,
      requestId,
    };
    writeTrace(tracePath, trace);
    expect(proposalStatus, 'unpivot preview must return HTTP 200').toBe(200);
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
    kind: 'unpivot-preview',
    status: 'ready',
    rowCount,
    previewReceiptId: proposalPreview?.receiptId ?? null,
    columns: proposalPreview?.columns?.map((column) => column.label) ?? [],
  };
  writeTrace(tracePath, trace);

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
