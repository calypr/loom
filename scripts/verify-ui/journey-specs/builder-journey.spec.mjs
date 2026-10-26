import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import {
  captureBuilderObservation,
  captureJourneyProvenance,
  combineAuthoringObservation,
  expect,
  observeAuthoringAttempt,
  test,
} from '../helpers/fast-fixtures.mjs';

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

const waitForProposalReady = async (page) => {
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  }, undefined, { timeout: 30_000 });
};

const waitForProposalGone = async (page) => {
  await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined, { timeout: 20_000 });
};

const applyGroup = async (page, trace, label, attempt) => {
  const proposal = page.getByTestId('construction-proposal-panel');
  const proposalAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'construction-proposals',
    owner: { kind: 'proposal', attempt },
    timeout: 20_000,
  });
  await page.getByTestId('construction-action-group-rows').click();
  const proposalStarted = await proposalAttempt.started;
  trace.actions.push({
    label: 'Preview',
    event: 'preview',
    owner: proposalStarted.owner,
    request: proposalStarted.request,
    observation: proposalStarted.requestObservation,
  });
  await waitForProposalReady(page);
  const proposalStatus = await proposal.getAttribute('data-proposal-status');
  if (label === 'GroupAgain') expect(proposalStatus).toBe('ready');
  else expect(proposalStatus, `${label} proposal status`).toBe('ready');
  const proposalExchange = await proposalAttempt.settled;
  if (label === 'GroupAgain') expect(proposalExchange.rawResponse.ok(), 'second group proposal preview').toBe(true);
  const proposalObservation = await captureBuilderObservation(page);
  trace.actions.push({
    label: 'PreviewComplete',
    event: 'preview-response',
    owner: proposalStarted.owner,
    request: proposalExchange.request,
    response: proposalExchange.response,
    settlement: proposalExchange.settlement,
    observation: combineAuthoringObservation(proposalExchange, proposalObservation),
  });
  const commandAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt },
    timeout: 20_000,
  });
  await page.getByTestId('construction-apply-proposal').click();
  const commandStarted = await commandAttempt.started;
  const commandExchange = await commandAttempt.settled;
  if (label === 'GroupAgain') expect(commandExchange.rawResponse.ok()).toBe(true);
  else expect(commandExchange.rawResponse.ok(), `${label} command persistence`).toBe(true);
  await waitForProposalGone(page);
  trace.actions.push({
    label,
    event: 'group',
    owner: commandStarted.owner,
    request: commandExchange.request,
    response: commandExchange.response,
    settlement: commandExchange.settlement,
    observation: await captureBuilderObservation(page),
  });
  return { proposalPreview: proposalExchange.payload.preview ?? proposalExchange.payload };
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
  await applyGroup(fastPage, trace.traces[0], 'Group', 'P1');
  console.log('[journey] first group applied');

  console.log('[journey] second group');
  const secondGroup = await applyGroup(fastPage, trace.traces[0], 'GroupAgain', 'P2');
  console.log('[journey] second group applied; second proposal preview captured');


  const back = fastPage.getByRole('button', { name: 'Back to table' });
  if (await back.count()) await back.click();
  await waitForPreviewReady(fastPage);
  await expect(fastPage.getByTestId('preview-table-content')).toBeVisible();
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row').first()).toBeVisible();

  const observedPreview = secondGroup.proposalPreview;
  const rowCount = Number(observedPreview.rowCount ?? observedPreview.rows?.length ?? 0);
  expect(rowCount, 'the applied table preview must contain exactly one grouped row').toBe(1);
  expect(Array.isArray(observedPreview.rows) ? observedPreview.rows.length : 0).toBe(1);
  expect(observedPreview.rows?.[0]?.row_count, 'grouped preview row value').toBe(1);
  expect(observedPreview.columns?.map((column) => column.column), 'grouped preview columns').toContain('row_count');
  const { __loom_row_id: _rowIdentity, ...observedValues } = observedPreview.rows[0] ?? {};
  expect(observedValues, 'exact grouped preview values').toEqual({ row_count: 1 });
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
