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
    return ['ready', 'error', 'needs-repair'].includes(panel?.getAttribute('data-proposal-status'));
  }, undefined, { timeout: 30_000 });
};

test('builder journey rejects a pivot error without changing the draft', async ({ fastPage, fastUrl }) => {
  test.setTimeout(90_000);
  fastPage.setDefaultTimeout(30_000);
  fastPage.setDefaultNavigationTimeout(20_000);

  const tracePath = process.env.LOOM_JOURNEY_TRACE;
  expect(tracePath, 'LOOM_JOURNEY_TRACE must be set by run-builder-journey.mjs').toBeTruthy();
  const sourceCommit = String(process.env.LOOM_JOURNEY_SOURCE_COMMIT ?? '').trim();
  expect(sourceCommit, 'LOOM_JOURNEY_SOURCE_COMMIT must be the tested Git commit').toMatch(/^[0-9a-f]{40}$/);

  const backendCalls = [];
  const proposalResponses = [];
  const commandCalls = [];
  fastPage.on('response', (response) => {
    if (!isAuthoringResponse(response)) return;
    const path = authoringPath(response);
    backendCalls.push({ method: response.request().method(), path, status: response.status() });
    if (response.request().method() === 'POST' && path === 'construction-proposals') proposalResponses.push(response);
    if (response.request().method() === 'POST' && path === 'commands') commandCalls.push(response);
  });

  console.log('[journey] navigation');
  await fastPage.goto(fastUrl, { waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  const cda = isCdaDataset();
  let cdaRowCount = 0;
  if (cda) {
    await addField(fastPage, 'Select Observation.subject.reference');
    await addField(fastPage, 'Select Observation.valueQuantity.unit');
    const added = await addField(fastPage, 'Select Observation.valueQuantity.value');
    cdaRowCount = Number(added.rowCount ?? added.rows?.length ?? 0);
    expect(cdaRowCount, 'CDA pivot error source rows').toBeGreaterThan(0);
  }

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    sourceCommit,
    traces: [{
      name: 'journey/pivot-error',
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

  console.log('[journey] configure failing pivot');
  if (cda) {
    trace.traces[0].actions.push(
      { label: 'AddColumn', event: 'add-column', observation: observation({ status: 'reshaping' }) },
      { label: 'AddColumnAgain', event: 'add-column', observation: observation({ status: 'reshaping' }) },
    );
  }

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
  await waitForProposalSettled(fastPage);

  const proposalResponse = proposalResponses.at(-1);
  expect(proposalResponse, 'pivot must produce a construction-proposals response').toBeTruthy();
  const proposalStatus = proposalResponse.status();
  const proposalBody = await proposalResponse.json().catch(() => null);
  const errorClass = proposalBody?.error?.code
    ?? proposalBody?.diagnostics?.[0]?.code
    ?? `HTTP_${proposalStatus}`;
  const requestId = proposalBody?.error?.requestId
    ?? proposalBody?.diagnostics?.[0]?.requestId
    ?? null;

  trace.traces[0].actions.push(
    {
      label: 'PivotDirect',
      event: 'pivot',
      observation: observation({ status: 'pivot-ready' }),
    },
    {
      label: 'Preview',
      event: 'preview',
      observation: observation({ status: 'previewing' }),
    },
    {
      label: 'PreviewError',
      event: 'preview-response',
      observation: observation({ status: 'error' }),
    },
  );
  trace.traces[0].backendCalls = backendCalls;
  trace.traces[0].outcome = {
    kind: 'pivot-preview',
    status: 'error',
    httpStatus: proposalStatus,
    errorClass,
    requestId,
    commandCount: commandCalls.length,
  };
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);

  expect(commandCalls, 'no draft command may run after a rejected pivot').toHaveLength(0);
  await expect(fastPage.locator('[data-testid^="construction-history-step-"]')).toHaveCount(0);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(cda ? cdaRowCount + 1 : 3);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(cda ? 4 : 5);
  expect(backendCalls.length, 'journey must exercise ten to twenty authoring backend calls').toBeGreaterThanOrEqual(10);
  expect(backendCalls.length, 'journey must exercise ten to twenty authoring backend calls').toBeLessThanOrEqual(20);
  expect(proposalStatus, 'pivot error must be a typed client error, not a server error').toBeGreaterThanOrEqual(400);
  expect(proposalStatus, 'pivot error must be a typed client error, not a server error').toBeLessThan(500);
  expect(errorClass, 'pivot error code').toBeTruthy();
  expect(errorClass, 'pivot error must not be an internal server error').not.toBe('INTERNAL_ERROR');
});
