import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { expect, test } from '../helpers/fast-fixtures.mjs';

const TRACE_SCHEMA_VERSION = 1;
const AUTHORING_PREFIX = '/authoring/v2/';
const SOURCE_LABELS = ['id', 'name[].family [0]', 'name[].family [1]', 'name__count', 'gender'];
const CDA_MINI_LABELS = ['id'];

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

const waitForProposalReady = async (page) => {
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.getAttribute('data-proposal-status') === 'ready';
  }, undefined, { timeout: 30_000 });
};

test('builder journey filters, previews exact rows, and cancels', async ({ fastPage, fastUrl }) => {
  test.setTimeout(120_000);
  fastPage.setDefaultTimeout(30_000);
  fastPage.setDefaultNavigationTimeout(20_000);

  const tracePath = process.env.LOOM_JOURNEY_TRACE;
  expect(tracePath, 'LOOM_JOURNEY_TRACE must be set by run-builder-journey.mjs').toBeTruthy();
  const sourceCommit = String(process.env.LOOM_JOURNEY_SOURCE_COMMIT ?? '').trim();
  expect(sourceCommit, 'LOOM_JOURNEY_SOURCE_COMMIT must be the tested Git commit').toMatch(/^[0-9a-f]{40}$/);

  const isCDAMini = process.env.LOOM_CDA_MINI_FIXTURE === '1';
  const sourceLabels = isCDAMini ? CDA_MINI_LABELS : SOURCE_LABELS;

  const backendCalls = [];
  const proposalBodies = [];
  fastPage.on('response', async (response) => {
    if (!isAuthoringResponse(response)) return;
    const path = authoringPath(response);
    backendCalls.push({ method: response.request().method(), path, status: response.status() });
    if (response.request().method() === 'POST' && path === 'construction-proposals' && response.ok()) {
      proposalBodies.push(await response.json().catch(() => null));
    }
  });

  console.log('[journey] navigation');
  await fastPage.goto(fastUrl, { waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    sourceCommit,
    traces: [{
      name: 'journey/filter-preview',
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

  const persistTrace = () => {
    trace.traces[0].backendCalls = backendCalls;
    mkdirSync(dirname(tracePath), { recursive: true });
    writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);
  };

  const runFilter = async ({ value, expectedId }) => {
    console.log(`[journey] filter ${value}`);
    await fastPage.getByTestId('construction-action-keep-rows').click();
    const editor = fastPage.getByTestId('construction-filter-editor');
    await expect(editor).toBeVisible();
    const column = editor.getByLabel('Column', { exact: true });
    const filterColumnLabel = isCDAMini ? 'id' : 'gender';
    const filterColumnValue = await column.locator('option').evaluateAll((options, label) =>
      options.find((option) => (option.textContent ?? '').trim().toLowerCase().startsWith(label))?.value, filterColumnLabel);
    expect(filterColumnValue, isCDAMini ? 'fixture id column must be available to filter' : 'fixture gender column must be available to filter').toBeTruthy();
    await column.selectOption(filterColumnValue);
    const condition = editor.getByLabel('Condition', { exact: true });
    await expect(condition).toBeEnabled();
    await condition.selectOption('EQUALS');
    const valueInput = editor.getByRole('textbox', { name: 'Value', exact: true });
    await expect(valueInput).toBeVisible();
    const before = proposalBodies.length;
    await valueInput.fill(value);
    const matchingPreview = () => [...proposalBodies.slice(before)].reverse()
      .find((body) => body?.preview?.rows?.some((row) => Object.values(row).includes(expectedId)))?.preview;
    await expect.poll(() => Boolean(matchingPreview())).toBe(true);
    await waitForProposalReady(fastPage);

    const panel = fastPage.getByTestId('construction-proposal-panel');
    await expect(panel).toContainText(isCDAMini ? '1 row and 1 column' : '1 row and 5 columns');
    const proposalId = await panel.getAttribute('data-proposal-id');
    const preview = matchingPreview();
    trace.traces[0].actions.push(
      {
        label: 'Filter',
        event: 'filter',
        observation: observation({ status: 'reshaping', proposalIdPresent: Boolean(proposalId) }),
      },
      {
        label: 'Preview',
        event: 'preview',
        observation: observation({ status: 'previewing', proposalIdPresent: Boolean(proposalId) }),
      },
      {
        label: 'PreviewComplete',
        event: 'preview-response',
        observation: observation({
          status: 'ready',
          proposalIdPresent: Boolean(proposalId),
          previewReceiptPresent: true,
          rowsPresent: true,
        }),
      },
    );

    const columns = preview.columns ?? [];
    const rows = preview.rows ?? [];
    const labels = columns.map((column) => column.label);
    const idColumn = columns.find((column) => column.label === 'id')?.column;
    const genderColumn = columns.find((column) => column.label === 'gender')?.column;
    const countColumn = columns.find((column) => column.label === 'name__count')?.column;
    trace.traces[0].outcome = {
      kind: 'table-preview',
      status: 'ready',
      rowCount: Number(preview.rowCount ?? rows.length),
      previewReceiptId: preview.receiptId ?? null,
      columns: labels,
    };
    persistTrace();

    expect(Number(preview.rowCount), `${value} filtered preview row count`).toBe(1);
    expect(labels, `${value} filtered preview labels`).toEqual(sourceLabels);
    expect(rows, `${value} filtered preview rows`).toHaveLength(1);
    if (isCDAMini) {
      expect(rows[0]?.[idColumn]).toBe(expectedId);
    } else {
      expect(rows[0]?.[genderColumn]).toBe(value);
      expect(rows[0]?.[countColumn]).toBe(2);
    }

    console.log(`[journey] cancel ${value} filter`);
    const cancel = panel.getByRole('button', { name: 'Cancel', exact: true });
    await expect(cancel).toBeVisible();
    await cancel.click();
    await expect(panel).toHaveCount(0);
    await waitForPreviewReady(fastPage);
    await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(isCDAMini ? 7 : 3);
    await expect(fastPage.getByTestId('preview-table-scroll').getByText(isCDAMini ? 'cda-mini-patient-001' : 'dev-patient-001').first()).toBeVisible();
    await expect(fastPage.getByTestId('preview-table-scroll').getByText(isCDAMini ? 'cda-mini-patient-002' : 'dev-patient-002').first()).toBeVisible();
    await expect(fastPage.getByRole('columnheader')).toHaveCount(isCDAMini ? 1 : 5);

    trace.traces[0].actions.push({
      label: 'CancelFilter',
      event: 'cancel-filter',
      observation: observation({ status: 'selected' }),
    });
    persistTrace();
  };

  await runFilter(isCDAMini
    ? { value: 'cda-mini-patient-001', expectedId: 'cda-mini-patient-001' }
    : { value: 'female', expectedId: 'dev-patient-001' });

  console.log('[journey] reload after cancel');
  await fastPage.reload({ waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(isCDAMini ? 7 : 3);
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(isCDAMini ? 'cda-mini-patient-001' : 'dev-patient-001').first()).toBeVisible();
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(isCDAMini ? 'cda-mini-patient-002' : 'dev-patient-002').first()).toBeVisible();
  await expect(fastPage.getByRole('columnheader')).toHaveCount(isCDAMini ? 1 : 5);
  trace.traces[0].unmodeledEvents.push({
    kind: 'reload',
    note: 'Reload after cancelling the filter preserves the source table.',
  });
  persistTrace();

  expect(backendCalls.length, 'journey must exercise ten to twenty authoring backend calls').toBeGreaterThanOrEqual(10);
  expect(backendCalls.length, 'journey must exercise ten to twenty authoring backend calls').toBeLessThanOrEqual(20);
});
