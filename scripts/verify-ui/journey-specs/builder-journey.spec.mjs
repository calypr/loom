import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import {
  captureBuilderObservation,
  captureJourneyProvenance,
  expect,
  observeAuthoringAttempt,
  test,
} from '../helpers/fast-fixtures.mjs';
import { applyJourneyGroup, assertGroupTwicePreview } from '../helpers/group-twice-journey.mjs';

const TRACE_SCHEMA_VERSION = 1;
const AUTHORING_PREFIX = '/authoring/v2/';

const isAuthoringResponse = (response) => {
  const url = new URL(response.url());
  return url.pathname.includes(AUTHORING_PREFIX);
};

const authoringPath = (response) => new URL(response.url()).pathname.split(AUTHORING_PREFIX)[1] ?? '';

const waitForPreviewReady = async (page) => {
  await page.waitForFunction(() => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && Boolean(preview.dataset.previewReceiptId);
  }, undefined, { timeout: 45_000 });
};

test('builder journey groups twice and previews the applied table', async ({ fastPage, fastUrl }) => {
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
  console.log('[journey] initial preview');
  await waitForPreviewReady(fastPage);
  const initialPreviewExchange = await initialPreviewAttempt.settled;
  const initialObservation = await captureBuilderObservation(fastPage);
  console.log('[journey] initial preview ready');

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    ...provenance,
    traces: [{
      name: 'journey/group-twice-preview',
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

  console.log('[journey] first group');
  await fastPage.getByTestId('construction-rows-settings-trigger').click();
  await applyJourneyGroup(fastPage, trace.traces[0], 'Group', 'P1');
  console.log('[journey] first group applied');

  console.log('[journey] second group');
  const secondGroup = await applyJourneyGroup(fastPage, trace.traces[0], 'GroupAgain', 'P2');
  console.log('[journey] second group applied; second proposal preview captured');


  const back = fastPage.getByRole('button', { name: 'Back to table' });
  if (await back.count()) await back.click();
  await waitForPreviewReady(fastPage);
  await expect(fastPage.getByTestId('preview-table-content')).toBeVisible();
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row').first()).toBeVisible();

  const observedPreview = secondGroup.proposalPreview;
  const rowCount = assertGroupTwicePreview(observedPreview);
  expect(backendCalls.length, 'journey must exercise at least ten authoring backend calls').toBeGreaterThanOrEqual(10);

  const tracePayload = {
    ...trace.traces[0],
    backendCalls,
    outcome: {
      kind: 'table-preview',
      status: 'ready',
      rowCount,
      previewReceiptId: observedPreview.receiptId ?? null,
    },
  };
  trace.traces[0] = tracePayload;

  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);
});
