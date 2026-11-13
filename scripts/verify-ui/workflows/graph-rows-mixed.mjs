import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
import { validateGraphRowsFixture } from '../helpers/graph-rows-fixture-oracle.mjs';
import { runGraphRowsRelatedRows } from './graph-rows-related-rows.mjs';

const documentFor = state => state.workspace.documents.find(document => document.output.id === 'ResearchSubject');
const sorted = rows => structuredClone(rows).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));

// Source steps carry parent step IDs; column bindings carry occurrence provenance.
// Resolve the known numeric field through those authoritative bindings, never
// by assuming projection steps duplicate a traversal's occurrenceId.
export function resolveGraphRowsObservationSource(document, network) {
  const occurrences = network.occurrences.filter(item => item.resourceType === 'Observation');
  assert.equal(occurrences.length, 1, 'The default has one exact source Observation occurrence');
  const occurrence = occurrences[0];
  const values = document.columns.flatMap(column => {
    if (!/(?:^|__)valueInteger$/.test(column.source?.imported?.outputColumn ?? '')) return [];
    const bindings = network.bindings.filter(binding => binding.status === 'EXACT' && binding.columnId === column.columnId && binding.occurrenceId === occurrence.occurrenceId);
    return bindings.map(binding => ({ column, binding }));
  });
  assert.equal(values.length, 1, 'The known Observation numeric field needs one exact occurrence/column binding');
  const { column, binding } = values[0];
  const steps = document.importedSource.steps.filter(item => item.id === binding.sourceStepId);
  assert.equal(steps.length, 1, 'The exact binding must resolve one existing source step');
  const step = steps[0];
  assert.equal(step.kind, 'CATALOG_PROJECTION');
  assert.equal(step.editable, true);
  assert.equal(step.valueMode, 'FIRST');
  assert.equal(binding.valueMode, step.valueMode);
  assert.equal(step.parentId, occurrence.sourceStepId, 'Projection scope must match the actual Observation traversal');
  const affected = network.bindings.filter(item => item.status === 'EXACT' && item.sourceStepId === step.id);
  assert(affected.every(item => item.occurrenceId === occurrence.occurrenceId && item.valueMode === step.valueMode), 'Shared source-step bindings must agree on occurrence and policy');
  return { occurrence, step, column, affectedColumnIds: affected.map(item => item.columnId) };
}

// Both entry-point cases use the same exact source-step API while exercising
// different visible controls. This edits the explicitly labelled shared policy;
// it does not invent a single-column override inside an immutable pin.
export async function editGraphRowsObservationList({ run, page, request, entry, expectedIdentities }) {
  const saved = await run.state();
  const document = documentFor(saved);
  const response = await request.post(`${run.apiPath}/authoring-network`, { timeout: 30_000, data: {
    snapshotToken: saved.catalog.snapshotToken, expectedDraftVersion: saved.draftVersion,
    expectedDraftDigest: saved.draftDigest, outputId: 'ResearchSubject',
  } });
  assert.equal(response.status(), 200);
  const { network } = await response.json();
  const { occurrence, step, column, affectedColumnIds } = resolveGraphRowsObservationSource(document, network);
  let initiatingControl;
  if (entry === 'graph-first') {
    const graph = page.getByTestId('authored-traversal-editor');
    await graph.getByRole('navigation', { name: 'Authored occurrences', exact: true }).locator(`button[data-occurrence-id="${occurrence.occurrenceId}"]`).click();
    initiatingControl = page.getByTestId('authored-occurrence-inspector').getByRole('button', { name: `Edit shared source policy for ${column.label}`, exact: true });
  } else {
    const expression = page.getByTestId('row-recipe-expression');
    await expression.getByRole('textbox', { name: 'Find row recipe column', exact: true }).fill(column.label);
    initiatingControl = expression.locator(`[data-term-kind="column"][data-column-id="${column.columnId}"]`).getByRole('button');
  }
  await initiatingControl.focus(); await initiatingControl.press('Enter');
  await expect(page.getByTestId('imported-source-step-editor')).toBeVisible();
  if (entry === 'graph-first') await run.captureLayout('open-row-source-policy-inspector', { inspector: true });
  await page.getByTestId('imported-source-value-mode').selectOption('ALL');
  const review = page.getByRole('region', { name: 'Proposed source step', exact: true });
  await review.getByRole('button', { name: 'Preview source step', exact: true }).click();
  await expect(review.getByTestId('imported-source-step-apply')).toBeEnabled();
  const root = document.columns.find(item => item.source?.imported?.outputColumn === 'id');
  assert(root);
  const assertLists = async candidate => {
    const preview = await run.preview({ candidate });
    const expected = validateGraphRowsFixture().preserving.filter(row => expectedIdentities.includes(row.identity[0])).map(({ identity, observationValues }) => ({ identity, observationValues }));
    assert.deepEqual(sorted(preview.rows.map(row => ({ identity: [row[root.column]], observationValues: row[column.column] }))), sorted(expected));
  };
  await assertLists(true);
  if (entry === 'graph-first') {
    for (const height of [900, 1100, 900]) {
      await page.setViewportSize({ width: 1536, height });
      await run.captureLayout(`proposed-Observation-list-${height}-${run.report.layoutComparison?.length ?? 0}`, { inspector: true,
        controls: [review.getByTestId('imported-source-step-apply'), review.getByRole('button', { name: 'Cancel', exact: true })] });
    }
  }
  await review.getByTestId('imported-source-step-apply').click(); await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
  await expect(initiatingControl).toBeFocused();
  await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click();
  await assertLists(false);
  const after = documentFor(await run.state());
  assert.equal(after.importedSource.steps.find(item => item.id === step.id).valueMode, 'ALL');
  assert.deepEqual(after.importedSource.steps.filter(item => item.id !== step.id), document.importedSource.steps.filter(item => item.id !== step.id));
  assert.deepEqual(after.columns.filter(item => !affectedColumnIds.includes(item.columnId)), document.columns.filter(item => !affectedColumnIds.includes(item.columnId)), 'A shared source-step edit preserves unrelated column bindings');
  assert.deepEqual(after.construction, document.construction);
  return { column, step, occurrence, affectedColumnIds };
}

export async function runGraphRowsMixed({ run, page, request, earlierTerm = false }) {
  await run.open();
  const baseline = await run.state();
  const original = documentFor(baseline);
  const originalRows = (await run.preview()).rows;
  const response = await request.post(`${run.apiPath}/authoring-network`, { timeout: 30_000, data: {
    snapshotToken: baseline.catalog.snapshotToken, expectedDraftVersion: baseline.draftVersion,
    expectedDraftDigest: baseline.draftDigest, outputId: 'ResearchSubject',
  } });
  assert.equal(response.status(), 200);
  const { network } = await response.json();
  const { step, column: valueColumn } = resolveGraphRowsObservationSource(original, network);
  const root = original.columns.find(column => column.source?.imported?.outputColumn === 'id');
  assert(root);
  const oracle = validateGraphRowsFixture();
  const assertLists = async (candidate = false) => {
    const preview = await run.preview({ candidate });
    assert.deepEqual(sorted(preview.rows.map(row => ({ identity: [row[root.column]], observationValues: row[valueColumn.column] }))),
      sorted(oracle.preserving.map(({ identity, observationValues }) => ({ identity, observationValues }))));
  };
  const editSourceMode = async mode => {
    const expression = page.getByTestId('row-recipe-expression');
    await expression.getByRole('textbox', { name: 'Find row recipe column', exact: true }).fill(valueColumn.label);
    await expression.locator(`[data-term-kind="column"][data-column-id="${valueColumn.columnId}"]`).getByRole('button').click();
    await expect(page.getByTestId('imported-source-step-editor')).toBeVisible();
    await page.getByTestId('imported-source-value-mode').selectOption(mode);
    const review = page.getByRole('region', { name: 'Proposed source step', exact: true });
    await review.getByRole('button', { name: 'Preview source step', exact: true }).click();
    await expect(review.getByTestId('imported-source-step-apply')).toBeEnabled();
    return review;
  };
  await run.action('Mixed: edit the existing Observation policy to a list in Define a row', async () => {
    await editSourceMode('ALL');
    await assertLists(true);
    await page.getByTestId('imported-source-step-apply').click();
    await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
    await assertLists();
  });
  await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click();
  await assertLists();
  const saved = documentFor(await run.state());
  assert.equal(saved.importedSource.steps.filter(item => item.id === step.id).length, 1);
  assert.equal(saved.importedSource.steps.find(item => item.id === step.id).valueMode, 'ALL');
  assert(saved.columns.some(column => column.columnId === valueColumn.columnId), 'The earlier value edit retains its source column identity');
  assert.deepEqual(saved.construction, original.construction, 'Editing an existing source term cannot append a construction operation');
  await expect(page.getByTestId('row-recipe-expression').locator(`[data-term-kind="column"][data-column-id="${valueColumn.columnId}"]`)).toContainText('all values as a list');
  const explicitScalar = async ({ rootID, status, expansion }) => {
    const beforeScalar = await run.state();
    await page.getByTestId('row-recipe-expression').getByRole('button', { name: 'Include related values', exact: true }).click();
    const fieldsTab = page.getByRole('tab', { name: 'Other fields', exact: true });
    if (await fieldsTab.count()) await fieldsTab.click();
    const exact = page.locator('[data-testid="construction-add-columns-source-option"][data-source-kind="EXACT_RELATED"]');
    await expect(exact).toHaveCount(1);
    await expect(exact).toContainText('Observation');
    await exact.click();
    const fieldEditor = page.getByTestId('construction-related-field-editor');
    await expect(fieldEditor).toBeVisible();
    await fieldEditor.getByRole('textbox', { name: 'Find a field', exact: true }).fill('valueInteger');
    const field = fieldEditor.getByRole('group', { name: 'Fields from this related record', exact: true }).getByRole('button').filter({ hasText: /(?:Observation\.)?valueInteger · integer/ });
    await expect(field).toHaveCount(1);
    await field.click();
    await fieldEditor.getByRole('textbox', { name: 'Column name', exact: true }).fill('per_match_observation_value');
    await fieldEditor.getByRole('textbox', { name: 'Column label', exact: true }).fill('Per-match Observation value');
    const proposal = page.getByTestId('construction-proposal-panel');
    const assertScalar = async (candidate = false) => {
      const preview = await run.preview({ candidate });
      const expected = [...oracle.expandedListsAndExplicitScalars, oracle.expandedListsKeepMissingExtra]
        .map(({ identity, subjectStatus, observationValues, perMatchObservationValue }) => ({ identity, subjectStatus, observationValues, perMatchObservationValue }));
      assert.deepEqual(sorted(preview.rows.map(row => ({ identity: [row[rootID], row.expanded_observation_id], subjectStatus: row[status],
        observationValues: row[valueColumn.column], perMatchObservationValue: row.per_match_observation_value }))), sorted(expected),
      'Explicit scalar selection must retain the original list and give each input/related-record pair its own scalar');
    };
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await assertScalar(true);
    await run.action('Mixed: Apply a separately selected per-match scalar', async () => {
      await page.getByTestId('construction-apply-proposal').click(); await expect(proposal).toHaveCount(0); await assertScalar();
    });
    const scalarState = await run.state();
    const added = documentFor(scalarState).construction.steps.filter(item => !documentFor(beforeScalar).construction.steps.some(before => before.id === item.id));
    assert.equal(added.length, 1); const scalar = added[0];
    assert.equal(scalar.operation.kind, 'RELATED_FIELD');
    assert.equal(scalar.operation.relatedField.source.resourceType, 'Observation');
    assert(/(?:^|\.)valueInteger$/.test(scalar.operation.relatedField.source.path));
    assert.deepEqual(scalar.inputs, [{ kind: 'STEP_OUTPUT', stepId: expansion.id }]);
    assert.notEqual(scalar.operation.relatedField.outputColumnId, valueColumn.columnId);
    const scalarOutput = scalar.outputs.find(output => output.id === scalar.operation.relatedField.outputColumnId);
    assert.equal(scalarOutput.name, 'per_match_observation_value'); assert.equal(scalarOutput.type, 'integer');
    assert.equal(documentFor(scalarState).importedSource.steps.find(item => item.id === step.id).valueMode, 'ALL');
    await run.action('Mixed: reload the distinct list and per-match scalar columns', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); await assertScalar();
      assert.deepEqual(documentFor(await run.state()).construction.steps.find(item => item.id === scalar.id), scalar);
    });
    if (earlierTerm) {
      const baselineWithDownstream = await run.state();
      const before = documentFor(baselineWithDownstream);
      const assertPreservedDownstream = async (mode) => {
        const current = documentFor(await run.state());
        assert.deepEqual(current.construction, before.construction, 'Earlier value policy editing preserves every downstream operation, order, input and anchor');
        assert.deepEqual(current.importedRecipe, before.importedRecipe);
        assert.deepEqual(current.columns.map(item => item.columnId), before.columns.map(item => item.columnId));
        assert.deepEqual(current.importedSource.steps.filter(item => item.id !== step.id), before.importedSource.steps.filter(item => item.id !== step.id));
        assert.equal(current.importedSource.steps.find(item => item.id === step.id).valueMode, mode);
        await assertScalar();
      };
      await run.action('Earlier term: Preview and Cancel with saved expansion and scalar downstream', async () => {
        const review = await editSourceMode('DISTINCT'); await assertScalar(true);
        await review.getByRole('button', { name: 'Cancel', exact: true }).click();
        await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
        const state = await run.state();
        assert.deepEqual([state.draftVersion, state.draftDigest], [baselineWithDownstream.draftVersion, baselineWithDownstream.draftDigest]);
        await assertPreservedDownstream('ALL');
      });
      for (const mode of ['DISTINCT', 'ALL']) {
        await run.action(`Earlier term: apply ${mode} to the original source policy without duplicate steps`, async () => {
          await editSourceMode(mode); await assertScalar(true);
          await page.getByTestId('imported-source-step-apply').click(); await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
          await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click();
          await assertPreservedDownstream(mode);
        });
      }
      return;
    }
    await page.getByRole('navigation', { name: 'Recipe presentation', exact: true }).getByRole('button', { name: 'Both editors', exact: true }).click();
    await page.getByTestId('row-recipe-expression').locator(`[data-term-kind="operation"][data-operation-id="${scalar.id}"]`).getByRole('button', { name: /^Edit operation [0-9]+:/ }).click();
    await page.getByTestId(`construction-remove-step-${scalar.id}`).click();
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await run.action('Mixed: remove only the explicit scalar before editing the earlier expansion', async () => {
      await page.getByTestId('construction-apply-proposal').click(); await expect(proposal).toHaveCount(0);
      const state = documentFor(await run.state());
      assert(!state.construction.steps.some(item => item.id === scalar.id));
      assert.deepEqual(state.construction.steps.find(item => item.id === expansion.id), expansion);
      assert.equal(state.importedSource.steps.find(item => item.id === step.id).valueMode, 'ALL');
    });
  };
  await runGraphRowsRelatedRows({ run, page, existingList: { column: valueColumn, sourceStepId: step.id }, onExpanded: explicitScalar, stopAfterExpanded: earlierTerm });
  if (earlierTerm) return;
  await run.action('Mixed: restore the existing policy through its original expression term', async () => {
    await editSourceMode('FIRST');
    await page.getByTestId('imported-source-step-apply').click();
    await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
    await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click();
    const restored = documentFor(await run.state());
    assert.equal(restored.importedSource.steps.find(item => item.id === step.id).valueMode, step.valueMode);
    assert.deepEqual(restored.importedRecipe, original.importedRecipe);
    // Removing the last operation may retain the public empty version-1 plan.
    assert.deepEqual(
      restored.construction === undefined ? { version: 1, steps: [] } : restored.construction,
      original.construction === undefined ? { version: 1, steps: [] } : original.construction,
    );
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalRows));
  });
}
