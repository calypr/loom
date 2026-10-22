import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import {
  captureBuilderObservation,
  captureJourneyProvenance,
  expect,
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

test('builder journey loads the authored table and previews exact rows', async ({ fastPage, fastUrl }) => {
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

  const initialPreviewAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'preview',
    owner: { kind: 'preview', attempt: 'P1' },
    timeout: 45_000,
  });
  const rowChoices = fastPage.waitForResponse((response) => {
    if (response.request().method() !== 'GET' || !isAuthoringResponse(response)) return false;
    return authoringPath(response) === 'row-definition-choices';
  }, { timeout: 10_000 }).catch(() => null);

  console.log('[journey] navigation');
  await fastPage.goto(fastUrl, { waitUntil: 'domcontentloaded' });
  const initialPreviewExchange = await initialPreviewAttempt.settled;
  expect(initialPreviewExchange.rawResponse.ok(), 'initial table preview').toBe(true);
  const previewPayload = initialPreviewExchange.payload;
  await waitForPreviewReady(fastPage);
  const initialObservation = await captureBuilderObservation(fastPage);
  const initialPreview = {
    owner: initialPreviewExchange.owner,
    request: initialPreviewExchange.request,
    response: initialPreviewExchange.response,
    settlement: initialPreviewExchange.settlement,
  };
  await rowChoices;
  console.log('[journey] initial preview ready');

  const columns = previewPayload.columns ?? [];
  const rows = previewPayload.rows ?? [];
  const labels = columns.map((column) => column.label);
  const rowCount = Number(previewPayload.rowCount ?? rows.length);
  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    ...provenance,
    traces: [{
      name: 'journey/load-preview',
      model: 'BuilderJourney',
      initialObservation,
      initialPreview: {
        ...initialPreview,
      },
      actions: [],
      unmodeledEvents: [],
      backendCalls,
      outcome: {
        kind: 'table-preview',
        status: 'ready',
        rowCount,
        previewReceiptId: previewPayload.receiptId ?? null,
        columns: labels,
      },
    }],
  };
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);

  const isCDAMini = process.env.LOOM_CDA_MINI_FIXTURE === '1';
  const isCDA = process.env.LOOM_FAST_PROJECT === 'loom_dev_cda_fhir';
  if (isCDAMini) {
    expect(rowCount, 'CDA mini initial preview row count').toBe(6);
    expect(labels, 'CDA mini initial preview columns').toEqual(['id']);
    expect(rows, 'CDA mini initial preview rows').toHaveLength(6);
    expect(new Set(rows.map((row) => row[columns[0].column])).size, 'CDA mini preview ids are unique').toBe(6);
    await expect(fastPage.getByRole('columnheader')).toHaveCount(1);
    await expect(fastPage.getByTestId('construction-preview'))
      .toHaveAttribute('data-preview-receipt-id', previewPayload.receiptId);
    expect(await fastPage.getByTestId('preview-table-scroll').getByRole('row').count()).toBeGreaterThanOrEqual(2);
  } else if (isCDA) {
    expect(rowCount, 'CDA initial preview row count at the default limit').toBe(25);
    expect(labels, 'CDA initial preview columns').toEqual(['id']);
    expect(previewPayload.sampled, 'CDA initial preview is sampled at the default limit').toBe(true);
    expect(rows, 'CDA initial preview rows').toHaveLength(25);
    expect(new Set(rows.map((row) => row[columns[0].column])).size, 'CDA preview ids are unique').toBe(25);
    await expect(fastPage.getByRole('columnheader')).toHaveCount(1);
    await expect(fastPage.getByTestId('construction-preview'))
      .toHaveAttribute('data-preview-receipt-id', previewPayload.receiptId);
    expect(await fastPage.getByTestId('preview-table-scroll').getByRole('row').count()).toBeGreaterThanOrEqual(2);
  } else {
    expect(rowCount, 'initial preview row count').toBe(2);
    expect(labels, 'initial preview columns').toEqual([
      'id',
      'name[].family [0]',
      'name[].family [1]',
      'name__count',
      'gender',
    ]);
    const columnId = (label) => columns.find((column) => column.label === label)?.column;
    const byId = Object.fromEntries(rows.map((row) => [row[columnId('id')], row]));
    expect(byId['dev-patient-001']?.[columnId('name[].family [0]')]).toBe('Example');
    expect(byId['dev-patient-001']?.[columnId('name[].family [1]')]).toBe('Example-Smith');
    expect(byId['dev-patient-001']?.[columnId('name__count')]).toBe(2);
    expect(byId['dev-patient-001']?.[columnId('gender')]).toBe('female');
    expect(byId['dev-patient-002']?.[columnId('name[].family [0]')]).toBe('Builder');
    expect(byId['dev-patient-002']?.[columnId('name[].family [1]')] ?? null).toBeNull();
    expect(byId['dev-patient-002']?.[columnId('name__count')]).toBe(1);
    expect(byId['dev-patient-002']?.[columnId('gender')] ?? null).toBeNull();
  }
  const minBackendCalls = Number(process.env.LOOM_JOURNEY_MIN_BACKEND_CALLS ?? 10);
  expect(backendCalls.length, `journey must exercise at least ${minBackendCalls} authoring backend calls`).toBeGreaterThanOrEqual(minBackendCalls);
});
