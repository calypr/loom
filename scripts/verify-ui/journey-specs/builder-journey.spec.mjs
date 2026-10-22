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

const applyGroup = async (page, trace, label) => {
  const proposal = page.getByTestId('construction-proposal-panel');
  await page.getByTestId('construction-action-group-rows').click();
  await waitForProposalReady(page);
  const proposalStatus = await proposal.getAttribute('data-proposal-status');
  expect(proposalStatus, `${label} proposal status`).toBe('ready');
  const proposalId = await proposal.getAttribute('data-proposal-id');
  const commands = page.waitForResponse((response) => {
    if (response.request().method() !== 'POST' || !isAuthoringResponse(response)) return false;
    return authoringPath(response) === 'commands';
  }, { timeout: 20_000 });
  await page.getByTestId('construction-apply-proposal').click();
  const commandsResponse = await commands;
  expect(commandsResponse.ok(), `${label} command persistence`).toBe(true);
  await waitForProposalGone(page);
  trace.actions.push({
    label,
    event: 'group',
    observation: observation({
      status: 'reshaping',
      proposalIdPresent: Boolean(proposalId),
      rowsPresent: false,
    }),
  });
};

test('builder journey groups twice and previews the applied table', async ({ fastPage, fastUrl }) => {
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
  console.log('[journey] initial preview');
  await waitForPreviewReady(fastPage);
  console.log('[journey] initial preview ready');

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    sourceCommit,
    traces: [{
      name: 'journey/group-twice-preview',
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

  console.log('[journey] first group');
  await fastPage.getByTestId('construction-rows-settings-trigger').click();
  await applyGroup(fastPage, trace.traces[0], 'Group');
  console.log('[journey] first group applied');

  console.log('[journey] second group');
  const secondProposal = fastPage.waitForResponse((response) => {
    if (response.request().method() !== 'POST' || !isAuthoringResponse(response)) return false;
    return authoringPath(response) === 'construction-proposals';
  }, { timeout: 20_000 });
  const secondCommands = fastPage.waitForResponse((response) => {
    if (response.request().method() !== 'POST' || !isAuthoringResponse(response)) return false;
    return authoringPath(response) === 'commands';
  }, { timeout: 20_000 });
  await fastPage.getByTestId('construction-action-group-rows').click();
  await waitForProposalReady(fastPage);
  const secondProposalId = await fastPage.getByTestId('construction-proposal-panel').getAttribute('data-proposal-id');
  expect(await fastPage.getByTestId('construction-proposal-panel').getAttribute('data-proposal-status')).toBe('ready');
  const secondProposalResponse = await secondProposal;
  expect(secondProposalResponse.ok(), 'second group proposal preview').toBe(true);
  const previewPayload = await secondProposalResponse.json();
  await fastPage.getByTestId('construction-apply-proposal').click();
  expect((await secondCommands).ok()).toBe(true);
  await waitForProposalGone(fastPage);
  trace.traces[0].actions.push({
    label: 'GroupAgain',
    event: 'group',
    observation: observation({
      status: 'reshaping',
      proposalIdPresent: Boolean(secondProposalId),
      rowsPresent: false,
    }),
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
  console.log('[journey] second group applied; second proposal preview captured');


  const back = fastPage.getByRole('button', { name: 'Back to table' });
  if (await back.count()) await back.click();
  await waitForPreviewReady(fastPage);
  await expect(fastPage.getByTestId('preview-table-content')).toBeVisible();
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row').first()).toBeVisible();

  const observedPreview = previewPayload.preview ?? previewPayload;
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
