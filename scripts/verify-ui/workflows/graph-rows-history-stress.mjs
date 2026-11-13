import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
import { EXPLORER_AUTHORING_SEMANTICS_VERSION } from '../../../ui/packages/loom-ui/src/authoringSemanticsVersion.mjs';

const suffix = index => String(index).padStart(3, '0');
export const historyStressStepIDs = Object.freeze(Array.from({ length: 100 }, (_, index) => `stress_calc_${suffix(index)}`));
const historyStressColumnNames = Object.freeze(Array.from({ length: 100 }, (_, index) => `stress_value_${suffix(index)}`));
const expectedSourceIDs = Object.freeze(['obs-one', 'obs-many-A', 'obs-many-B']);
const sorted = values => [...values].sort();

// Values are a literal independent oracle: every authored expression is 1 + 2.
// The source identity set comes from the hand-authored GRAPH-ROWS fixture.
export function assertHistoryStressRows(preview, idColumn) {
  assert(Array.isArray(preview.rows), 'The real compiler preview must return rows');
  assert.equal(preview.sampled, false, 'The three-record fixture must return its complete result');
  assert.deepEqual(sorted(preview.rows.map(row => row[idColumn])), sorted(expectedSourceIDs));
  for (const row of preview.rows) {
    for (const name of historyStressColumnNames) {
      assert(Object.hasOwn(row, name), `Missing calculated value ${name}`);
      assert.equal(row[name], 3, `Independent 1 + 2 oracle failed for ${name}`);
    }
  }
}

export function assertHistoryStressPlan(document) {
  assert.equal(document.importedRecipe, undefined, 'The stress fixture owns an ordinary source table');
  assert.deepEqual(document.construction?.steps.map(step => step.id), historyStressStepIDs, 'Every original operation identity and its execution order must survive');
  for (const [index, step] of document.construction.steps.entries()) {
    assert.equal(step.operation.kind, 'DERIVE');
    assert.deepEqual(step.operation.derive, { constructionId: step.id, outputColumnId: `stress_column_${suffix(index)}`,
      operation: 'ADD', left: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 1 } },
      right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 2 } }, missingInputPolicy: 'ERROR' },
    'Calculation expressions and their missing-input policies must survive unchanged');
    assert.deepEqual(step.inputs, index === 0 ? [{ kind: 'SOURCE_PROJECTION' }] : [{ kind: 'STEP_OUTPUT', stepId: historyStressStepIDs[index - 1] }]);
  }
}

export function nextStressStep(index, stageColumns) {
  const id = historyStressStepIDs[index];
  const outputColumnId = `stress_column_${suffix(index)}`;
  return {
    id,
    inputs: index === 0 ? [{ kind: 'SOURCE_PROJECTION' }] : [{ kind: 'STEP_OUTPUT', stepId: historyStressStepIDs[index - 1] }],
    operation: {
      kind: 'DERIVE',
      derive: { constructionId: id, outputColumnId, operation: 'ADD',
        left: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 1 } },
        right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 2 } }, missingInputPolicy: 'ERROR' },
    },
    outputs: [...stageColumns.map(column => ({ id: column.id, name: column.name, label: column.label,
      ...(column.type === undefined ? {} : { type: column.type }) })),
    { id: outputColumnId, name: historyStressColumnNames[index], label: `Stress value ${suffix(index)}`, type: 'integer' }],
  };
}

async function jsonPost(request, url, data, timeout = 5000) {
  const response = await request.post(url, { data, timeout });
  assert.equal(response.status(), 200, `Stress fixture API rejected ${new URL(url).pathname}: ${(await response.text()).slice(0, 700)}`);
  return response.json();
}

export async function seedThroughProductionProposals(run, request, state, outputId, { fixtureRequestTimeoutMs = 5000 } = {}) {
  assert([5000, 30000].includes(fixtureRequestTimeoutMs));
  if (fixtureRequestTimeoutMs !== 5000) {
    assert.equal(run.report.phase, 'production-fixture-preparation', 'A larger API bootstrap budget cannot be used by a native lifecycle');
    assert.equal(run.report.requestBudgetMs, fixtureRequestTimeoutMs);
  }
  const started = performance.now();
  const capabilities = await jsonPost(request, `${run.apiPath}/construction-capabilities`, {
    snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
    expectedDraftDigest: state.draftDigest, outputId, stageId: 'source_projection',
  }, fixtureRequestTimeoutMs);
  assert.equal(capabilities.outputId, outputId);
  assert.equal(capabilities.draftVersion, state.draftVersion);
  assert.equal(capabilities.draftDigest, state.draftDigest);
  assert.equal(capabilities.snapshotToken, state.catalog.snapshotToken);
  assert.equal(capabilities.selectedStage.id, 'source_projection');
  assert(capabilities.selectedStage.capabilities.some(capability => capability.kind === 'DERIVE' && capability.supported), 'The source must support actual bounded calculations');
  assert.equal(capabilities.baseConstruction.steps.length, 0, 'The new stress source must have no existing transformations');
  let construction = capabilities.baseConstruction;
  let columns = capabilities.selectedStage.columns;
  let owner = state;
  const preparation = [];
  run.report.historyStressFixture = { elapsedMs: null, preparation, expectedOperationIDs: historyStressStepIDs,
    expectedSourceIDs: expectedSourceIDs, expectedCalculatedValue: 3 };
  // DERIVE retains every prior input by contract. Every intermediate source
  // identity and calculated value is checked against the independent oracle;
  // the final native preview also checks all100 retained calculated columns.
  // The production contract intentionally accepts one appended calculation per
  // proposal. Seed sequentially; never inject a workspace or bypass its CAS.
  for (let index = 0; index < historyStressStepIDs.length; index += 1) {
    const stepStarted = performance.now();
    const step = nextStressStep(index, columns);
    try {
    const proposal = await jsonPost(request, `${run.apiPath}/construction-proposals`, {
      snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: owner.draftVersion,
      expectedDraftDigest: owner.draftDigest, outputId, changedStepId: step.id,
      candidateConstruction: { ...construction, steps: [...construction.steps, step] }, limit: 100,
    }, fixtureRequestTimeoutMs);
    assert.equal(proposal.previewStatus, 'READY', 'Every setup step requires a successful real execution preview');
    assert(proposal.proposalId);
    assert.equal(proposal.outputId, outputId);
    assert.equal(proposal.draftVersion, owner.draftVersion);
    assert.equal(proposal.draftDigest, owner.draftDigest);
    assert.equal(proposal.snapshotToken, state.catalog.snapshotToken);
    assert(Array.isArray(proposal.preview?.rows), 'The proposal must include its actual receipt-bound preview');
    assert.equal(proposal.preview.rows.length, 3, 'Calculation setup must preserve all three source rows');
    assert.deepEqual(sorted(proposal.preview.rows.map(row => row[run.idColumn])), sorted(expectedSourceIDs));
    for (const row of proposal.preview.rows) assert.equal(row[historyStressColumnNames[index]], 3);
    const applied = await jsonPost(request, `${run.apiPath}/commands`, {
      commandId: `graph-rows-history-seed-${suffix(index)}`,
      semanticsVersion: EXPLORER_AUTHORING_SEMANTICS_VERSION,
      snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: owner.draftVersion,
      expectedDraftDigest: owner.draftDigest,
      commands: [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: proposal.proposalId }],
    }, fixtureRequestTimeoutMs);
    assert(applied.draftVersion > owner.draftVersion, 'Every fixture seed must advance its real CAS revision');
    assert.equal(applied.workspace.documents.find(document => document.output.id === outputId).construction.steps.length, index + 1);
    owner = { ...owner, ...applied };
    construction = owner.workspace.documents.find(document => document.output.id === outputId).construction;
    const stage = proposal.stages.find(stage => stage.id === step.id);
    assert(stage, 'The compiler must expose the exact appended output stage');
    columns = stage.columns;
    preparation.push({ stepId: step.id, draftVersion: owner.draftVersion, elapsedMs: performance.now() - stepStarted, status: 'passed', values: proposal.preview.rows.map(row => ({ id: row[run.idColumn], value: row[historyStressColumnNames[index]] })) });
    } catch (error) {
      const failure = { phase: 'fixture-setup', stepId: step.id, elapsedMs: performance.now() - stepStarted, status: 'failed', error: error.message };
      preparation.push(failure);
      run.report.firstFailure ??= failure;
      throw error;
    }
  }
  run.report.historyStressFixture.elapsedMs = performance.now() - started;
  return owner;
}

async function assertDesktopLayout(page, run, label) {
  const visible = await page.evaluate(() => {
    const rect = selector => {
      const element = document.querySelector(selector);
      if (!element) return null;
      const { x, y, width, height, bottom, right } = element.getBoundingClientRect();
      return { x, y, width, height, bottom, right };
    };
    return { viewport: { width: innerWidth, height: innerHeight }, scroll: { width: document.documentElement.scrollWidth,
      height: document.documentElement.scrollHeight }, network: rect('[aria-label="Record network"]'),
    preview: rect('[data-testid="construction-preview"]'), table: rect('[data-testid="preview-table-scroll"]') };
  });
  assert(visible.network?.width > 150 && visible.network?.height > 80, 'A useful authored network must remain visible');
  assert(visible.preview?.width > 300 && visible.preview?.height > 120, 'A useful actual result preview must remain visible');
  assert(visible.table?.height > 80, 'At least a usable result-table viewport must remain');
  assert(visible.preview.y >= 0 && visible.preview.bottom <= visible.viewport.height + 1, 'The result must remain inside the desktop viewport');
  assert(visible.network.y >= 0 && visible.network.bottom <= visible.viewport.height + 1, 'The network must remain inside the desktop viewport');
  assert(visible.scroll.width <= visible.viewport.width + 1, 'No page-level horizontal overflow');
  assert(visible.scroll.height <= visible.viewport.height + 1, 'Routine history inspection must not require page-level scrolling');
  run.report.historyStressLayout ??= [];
  run.report.historyStressLayout.push({ label, ...visible });
}

// Call from a registered Playwright test with setup(..., {ordinary:true}); the
// wrapper owns try/catch/finally and run.finish(), as for every GRAPH-ROWS case.
export async function runGraphRowsHistoryStress({ run, page, request }) {
  const url = new URL(run.target.uiUrl);
  url.searchParams.set('project', run.target.fixtureProject);
  url.searchParams.set('explorer', run.explorerId);
  const prepared = run.historyPrepared;
  assert(prepared, 'The runner must prepare the 100-step production fixture before the native action clock starts');
  const outputId = prepared.outputId;
  const original = prepared.sourceDocument;
  const idColumn = prepared.idColumn;
  const saved = await run.state();
  const document = saved.workspace.documents.find(document => document.output.id === outputId);
  assertHistoryStressPlan(document);
  assert.deepEqual({ version: saved.draftVersion, digest: saved.draftDigest }, prepared.savedIdentity);
  const identity = prepared.savedIdentity;
  await run.action('Reload actual 100-operation plan', async () => {
    await page.goto(url.href, { waitUntil: 'domcontentloaded' });
    await page.getByTestId(`construction-table-${outputId}`).click();
    await expect(page.getByTestId('construction-preview')).toHaveAttribute('data-preview-status', 'ready');
    assertHistoryStressRows(await run.preview(), idColumn);
  });
  const group = page.getByTestId(`construction-history-adjustment-group-${historyStressStepIDs[0]}`);
  await expect(group).toHaveCount(1);
  await expect(group.locator('summary').first()).toContainText('Column adjustments (100)');
  await expect(group.locator('details').first()).not.toHaveAttribute('open');
  await expect(page.getByTestId(`construction-history-step-${historyStressStepIDs[99]}`)).not.toBeVisible();
  await assertDesktopLayout(page, run, 'collapsed-100-at-900');
  await run.action('Keyboard opens the 100-operation group', async () => {
    const summary = group.locator('summary').first();
    await summary.focus();
    await summary.press('Enter');
    await expect(group.locator('details').first()).toHaveAttribute('open');
  });
  assert.deepEqual(await group.locator('[data-testid^="construction-history-step-"]').evaluateAll(buttons => buttons.map(button => button.dataset.testid.slice('construction-history-step-'.length))), historyStressStepIDs, 'Expanding the group must expose every operation in original order');
  for (const index of [0, 99]) {
    const step = page.getByTestId(`construction-history-step-${historyStressStepIDs[index]}`);
    await run.action(`Keyboard selects original operation ${index + 1}`, async () => {
      await step.scrollIntoViewIfNeeded();
      await step.focus();
      await step.press('Enter');
      await expect(step).toHaveAttribute('aria-pressed', 'true');
      await expect(page.getByTestId('construction-calculate-editor')).toHaveCount(1);
      await expect(page.getByRole('textbox', { name: 'Output column name', exact: true })).toHaveValue(historyStressColumnNames[index]);
      await expect(page.getByTestId('construction-operation-editor')).toHaveCount(1);
    });
    await run.action(`Close operation ${index + 1} without a save`, async () => {
      await page.getByTestId('construction-close-operation-editor').click();
      await expect(page.getByTestId('construction-operation-editor')).toHaveCount(0);
    });
  }
  await run.action('Collapse inspected 100-operation group', async () => {
    await group.locator('summary').first().click();
    await expect(group.locator('details').first()).not.toHaveAttribute('open');
  });
  for (const height of [1100, 900]) {
    await page.setViewportSize({ width: 1536, height });
    await assertDesktopLayout(page, run, `collapsed-100-at-${height}`);
  }
  const after = await run.state();
  assert.deepEqual({ version: after.draftVersion, digest: after.draftDigest }, identity, 'Inspecting, collapsing, keyboard selection and resizing must never write the saved plan');
  const afterDocument = after.workspace.documents.find(document => document.output.id === outputId);
  assertHistoryStressPlan(afterDocument);
  assert.deepEqual(afterDocument.columns, original.columns, 'Calculated steps must not rewrite source field bindings');
  assert.deepEqual(afterDocument.route, original.route);
  assert.deepEqual(afterDocument.rows, original.rows);
  assertHistoryStressRows(await run.preview(), idColumn);
}
