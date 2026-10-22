import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { expect, test } from '../helpers/fast-fixtures.mjs';
import { addField, isCdaDataset } from '../helpers/cda-journey-fields.mjs';

const TRACE_SCHEMA_VERSION = 1;
const AUTHORING_PREFIX = '/authoring/v2/';
const SOURCE_LABELS = ['id', 'name[].family [0]', 'name[].family [1]', 'name__count', 'gender'];

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

const waitForProposalGone = async (page) => {
  await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined, { timeout: 20_000 });
};

test('builder journey edits and removes a group, then reloads the source', async ({ fastPage, fastUrl }) => {
  test.setTimeout(120_000);
  fastPage.setDefaultTimeout(30_000);
  fastPage.setDefaultNavigationTimeout(20_000);

  const tracePath = process.env.LOOM_JOURNEY_TRACE;
  expect(tracePath, 'LOOM_JOURNEY_TRACE must be set by run-builder-journey.mjs').toBeTruthy();
  const sourceCommit = String(process.env.LOOM_JOURNEY_SOURCE_COMMIT ?? '').trim();
  expect(sourceCommit, 'LOOM_JOURNEY_SOURCE_COMMIT must be the tested Git commit').toMatch(/^[0-9a-f]{40}$/);

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
  const cda = isCdaDataset();
  let sourceLabels = SOURCE_LABELS;
  let sourceRows = 2;
  let sourceIDs = ['dev-patient-001', 'dev-patient-002'];
  if (cda) {
    const added = await addField(fastPage, 'Select Patient.extension[].valueCode');
    sourceLabels = (added.columns ?? []).map((column) => column.label);
    sourceRows = Number(added.rowCount ?? added.rows?.length ?? 0);
    const id = (added.columns ?? []).find((column) => column.label === 'id')?.column;
    sourceIDs = (added.rows ?? []).slice(0, 2).map((row) => row[id]);
    expect(sourceRows, 'CDA source rows').toBeGreaterThan(0);
    expect(sourceLabels, 'CDA source columns').toContain('Extension Value Code');
  }

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    sourceCommit,
    traces: [{
      name: 'journey/edit-remove-reload',
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

  console.log('[journey] apply default group');
  await fastPage.getByTestId('construction-rows-settings-trigger').click();
  await fastPage.getByTestId('construction-action-group-rows').click();
  await waitForProposalReady(fastPage);
  const firstProposalId = await fastPage.getByTestId('construction-proposal-panel').getAttribute('data-proposal-id');
  const firstCommands = fastPage.waitForResponse((response) =>
    response.request().method() === 'POST' && isAuthoringResponse(response) && authoringPath(response) === 'commands',
  { timeout: 20_000 });
  await fastPage.getByTestId('construction-apply-proposal').click();
  expect((await firstCommands).ok(), 'default group persistence').toBe(true);
  await waitForProposalGone(fastPage);
  const backToTable = fastPage.getByRole('button', { name: 'Back to table' });
  if (await backToTable.isVisible().catch(() => false)) await backToTable.click();
  if (cda) {
    expect(await fastPage.getByTestId('preview-table-scroll').getByRole('row').count(), 'CDA default group rows').toBeGreaterThan(1);
  } else {
    await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(2);
  }
  trace.traces[0].actions.push({
    label: 'Group',
    event: 'group',
    observation: observation({ status: 'reshaping', proposalIdPresent: Boolean(firstProposalId) }),
  });

  console.log('[journey] edit group to id and gender');
  const history = fastPage.locator('[data-testid^="construction-history-step-"]').first();
  await expect(history).toBeVisible();
  const stepTestId = await history.getAttribute('data-testid');
  const stepId = stepTestId.replace('construction-history-step-', '');
  await history.click();
  await fastPage.getByTestId(`construction-edit-step-${stepId}`).click();
  const idGroup = fastPage.getByRole('checkbox', { name: 'Group by id', exact: true });
  const groupColumnLabel = cda ? 'Extension Value Code' : 'gender';
  const genderGroup = fastPage.getByRole('checkbox', { name: `Group by ${groupColumnLabel}`, exact: true });
  await expect(idGroup).toBeVisible();
  await expect(genderGroup).toBeVisible();
  const beforeEdit = proposalBodies.length;
  await idGroup.check();
  await genderGroup.check();
  const editedBody = () => [...proposalBodies.slice(beforeEdit)].reverse().find((body) => {
    const labels = body?.preview?.columns?.map((column) => column.label) ?? [];
    return body?.preview?.rowCount === (cda ? sourceRows : 2)
      && labels.includes('id')
      && labels.includes(groupColumnLabel)
      && labels.includes('Row count');
  });
  await expect.poll(() => Boolean(editedBody())).toBe(true);
  const editedPreview = editedBody().preview;
  await waitForProposalReady(fastPage);
  await expect(fastPage.getByTestId('construction-proposal-panel')).toContainText(
    cda ? `${sourceRows} rows and 3 columns` : '2 rows and 3 columns');
  const editedPanelId = await fastPage.getByTestId('construction-proposal-panel').getAttribute('data-proposal-id');
  trace.traces[0].actions.push(
    {
      label: 'EditGroup',
      event: 'edit-group',
      observation: observation({ status: 'reshaping', proposalIdPresent: Boolean(editedPanelId) }),
    },
    {
      label: 'Preview',
      event: 'preview',
      observation: observation({ status: 'previewing', proposalIdPresent: Boolean(editedPanelId) }),
    },
    {
      label: 'PreviewComplete',
      event: 'preview-response',
      observation: observation({
        status: 'ready',
        proposalIdPresent: Boolean(editedPanelId),
        previewReceiptPresent: true,
        rowsPresent: true,
      }),
    },
  );

  const editedColumns = editedPreview.columns ?? [];
  const editedRows = editedPreview.rows ?? [];
  const editedLabels = editedColumns.map((column) => column.label);
  const editedId = editedColumns.find((column) => column.label === 'id')?.column;
  const editedGender = editedColumns.find((column) => column.label === groupColumnLabel)?.column;
  const editedCount = editedColumns.find((column) => column.label === 'Row count')?.column;
  expect(Number(editedPreview.rowCount), 'edited group preview row count').toBe(cda ? sourceRows : 2);
  expect(editedLabels, 'edited group preview labels').toEqual(['id', groupColumnLabel, 'Row count']);
  if (cda) {
    expect(editedRows, 'CDA edited group rows').toHaveLength(sourceRows);
    expect(editedRows.every((row) => row[editedCount] === 1), 'CDA groups contain one record each').toBe(true);
  } else {
    const femaleRow = editedRows.find((row) => row[editedGender] === 'female');
    const missingRow = editedRows.find((row) => (row[editedGender] ?? null) === null);
    expect(femaleRow?.[editedId], 'female group id').toBe('dev-patient-001');
    expect(femaleRow?.[editedCount], 'female group count').toBe(1);
    expect(missingRow?.[editedId], 'missing group id').toBe('dev-patient-002');
    expect(missingRow?.[editedCount], 'missing group count').toBe(1);
  }

  const editCommands = fastPage.waitForResponse((response) =>
    response.request().method() === 'POST' && isAuthoringResponse(response) && authoringPath(response) === 'commands',
  { timeout: 20_000 });
  await fastPage.getByTestId('construction-apply-proposal').click();
  expect((await editCommands).ok(), 'group edit persistence').toBe(true);
  await waitForProposalGone(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(cda ? sourceRows + 1 : 3);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(3);
  await expect(fastPage.getByRole('columnheader', { name: /^id$/i })).toBeVisible();
  await expect(fastPage.getByRole('columnheader', { name: new RegExp(`^${groupColumnLabel}$`, 'i') })).toBeVisible();
  await expect(fastPage.getByRole('columnheader', { name: /^row count$/i })).toBeVisible();

  console.log('[journey] remove group');
  const editedHistory = fastPage.locator('[data-testid^="construction-history-step-"]').first();
  const editedStepTestId = await editedHistory.getAttribute('data-testid');
  const editedStepId = editedStepTestId.replace('construction-history-step-', '');
  await editedHistory.click();
  await fastPage.getByTestId(`construction-remove-step-${editedStepId}`).click();
  await waitForProposalReady(fastPage);
  await expect(fastPage.getByTestId('construction-proposal-panel')).toContainText(
    cda ? `${sourceRows} rows and ${sourceLabels.length} columns` : '2 rows and 5 columns');
  const removeCommands = fastPage.waitForResponse((response) =>
    response.request().method() === 'POST' && isAuthoringResponse(response) && authoringPath(response) === 'commands',
  { timeout: 20_000 });
  await fastPage.getByTestId('construction-apply-proposal').click();
  expect((await removeCommands).ok(), 'group removal persistence').toBe(true);
  await waitForProposalGone(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(cda ? sourceRows + 1 : 3);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(cda ? sourceLabels.length : 5);
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(sourceIDs[0]).first()).toBeVisible();
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(sourceIDs[1]).first()).toBeVisible();
  trace.traces[0].actions.push({
    label: 'RemoveGroup',
    event: 'remove-group',
    observation: observation({ status: 'selected' }),
  });

  console.log('[journey] reload after removal');
  await fastPage.reload({ waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(cda ? sourceRows + 1 : 3);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(cda ? sourceLabels.length : 5);
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(sourceIDs[0]).first()).toBeVisible();
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(sourceIDs[1]).first()).toBeVisible();
  trace.traces[0].unmodeledEvents.push({
    kind: 'reload',
    note: 'Reload after removing the group preserves the restored source table.',
  });

  trace.traces[0].backendCalls = backendCalls;
  trace.traces[0].outcome = {
    kind: 'table-preview',
    status: 'ready',
    rowCount: cda ? sourceRows : 2,
    columns: editedLabels,
    restoredRowCount: cda ? sourceRows : 2,
    restoredColumns: sourceLabels,
  };
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);

  expect(backendCalls.length, 'lifecycle journey must exercise ten to thirty authoring backend calls').toBeGreaterThanOrEqual(10);
  expect(backendCalls.length, 'lifecycle journey must exercise ten to thirty authoring backend calls').toBeLessThanOrEqual(45);
});
