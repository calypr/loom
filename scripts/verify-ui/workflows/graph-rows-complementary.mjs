import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
import { editGraphRowsObservationList } from './graph-rows-mixed.mjs';
import { resolveComplementaryCatalogEdge } from '../helpers/graph-rows-complementary-oracle.mjs';
import { validateGraphRowsFixture } from '../helpers/graph-rows-fixture-oracle.mjs';

const documentFor = state => state.workspace.documents.find(document => document.output.id === 'ResearchSubject');
const identity = state => [state.draftVersion, state.draftDigest];
const sorted = values => structuredClone(values).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));

export async function runGraphRowsComplementaryConnection({ run, page, request, mode }) {
  assert(['graph-first', 'row-first'].includes(mode));
  run.report.complementaryEntryMode = mode;
  const oracle = validateGraphRowsFixture();
  await run.open();
  await page.getByRole('navigation', { name: 'Recipe presentation', exact: true }).getByRole('button', { name: 'Both editors', exact: true }).click();
  const original = await run.state();
  const originalDocument = documentFor(original);
  const originalRows = (await run.preview()).rows;
  const expression = page.getByTestId('row-recipe-expression');
  const graph = page.getByTestId('authored-traversal-editor');
  await expect(expression).toBeVisible();
  await expect(graph).toBeVisible();
  await expect(expression).toContainText('Connected records:');
  await expect(expression).toContainText('Include column values:');
  if (mode === 'graph-first') {
    await run.compareRowNotation(original, 'actual-unmodified-default');
    for (const height of [900, 1100, 900]) {
      await page.setViewportSize({ width: 1536, height });
      for (const view of ['Both editors', 'Record network', 'Define a row']) await run.captureLayout(`idle-${height}-${view.replaceAll(' ', '-')}-${run.report.layoutComparison?.length ?? 0}`, { view });
    }
    await page.getByRole('navigation', { name: 'Recipe presentation', exact: true }).getByRole('button', { name: 'Both editors', exact: true }).click();
    await graph.getByRole('navigation', { name: 'Authored occurrences', exact: true }).locator('button[data-occurrence-id="base"]').click();
    await run.captureLayout('selected-starting-occurrence', { inspector: true });
  }


  const start = async () => {
    if (mode === 'row-first') {
      const picker = expression.getByTestId('row-connection-picker');
      if (await picker.getAttribute('open') === null) await picker.locator('summary').click();
      await picker.getByRole('combobox', { name: 'Connect from occurrence', exact: true }).selectOption('base');
      const choices = await picker.getByRole('combobox', { name: 'Connection relationship', exact: true }).locator('option').evaluateAll(options => options.filter(option => /→ ResearchStudy$/.test(option.textContent)).map(option => ({ label: option.textContent, value: option.value })));
      assert.equal(choices.length, 1, 'The known root/study catalog connection must be unambiguous');
      await picker.getByRole('combobox', { name: 'Connection relationship', exact: true }).selectOption(choices[0].value);
      await picker.getByRole('button', { name: 'Preview connection', exact: true }).click();
    } else {
      const explore = graph.getByRole('button', { name: 'Explore available connections', exact: true });
      if (await explore.count()) await explore.click();
      await graph.locator('button[data-occurrence-id="base"]').click();
      await page.getByRole('checkbox', { name: 'Show full network', exact: true }).check();
      const node = page.locator('.react-flow__node').filter({ hasText: 'ResearchStudy' });
      await expect(node).toHaveCount(1);
      await node.focus(); await node.press('Enter');
      await page.getByRole('button', { name: 'Add traversal', exact: true }).click();
    }
  };
  const preview = async () => {
    const review = page.getByRole('region', { name: 'Proposed related records', exact: true });
    await review.getByRole('button', { name: 'Preview relationship', exact: true }).click();
    await expect(review.getByRole('button', { name: 'Apply relationship', exact: true })).toBeEnabled();
    const authored = graph.getByRole('button', { name: 'Show authored traversal', exact: true });
    if (await authored.count()) await authored.click();
    await expect(expression).toHaveAttribute('data-recipe-state', 'proposed');
    await expect(graph).toHaveAttribute('data-projection-kind', 'proposed');
    const term = expression.locator('[data-term-kind="connection"]').filter({ hasText: 'ResearchStudy' });
    await expect(term).toHaveCount(1);
    const occurrence = await term.getAttribute('data-occurrence-id');
    assert(occurrence);
    await expect(graph.locator(`button[data-term-kind="connection"][data-occurrence-id="${occurrence}"]`)).toContainText('ResearchStudy');
    assert.deepEqual(identity(await run.state()), identity(original), 'Preview and cross-editor inspection cannot mutate the saved owner');
    return review;
  };
  await run.action(`${mode}: prepare connection and inspect both proposed representations`, async () => { await start(); await preview(); });
  if (mode === 'graph-first') {
    await run.action('Ownership: inspection preserves the candidate and conflicting mutation is blocked', async () => {
      const candidate = page.getByTestId('construction-preview');
      const receipt = await candidate.getAttribute('data-preview-receipt-id');
      assert(receipt);
      const presentation = page.getByRole('navigation', { name: 'Recipe presentation', exact: true });
      for (const name of ['Record network', 'Define a row', 'Both editors']) {
        await presentation.getByRole('button', { name, exact: true }).click();
        await expect(presentation.getByRole('button', { name, exact: true })).toHaveAttribute('aria-pressed', 'true');
        await expect(candidate).toHaveAttribute('data-preview-receipt-id', receipt);
        await expect(candidate).toHaveAttribute('data-preview-proposal-id', /\S+/);
        await run.captureLayout(`proposed-connection-${name.replaceAll(' ', '-')}`, { view: name, inspector: true, controls: [page.getByRole('button', { name: 'Apply relationship', exact: true }), page.getByRole('region', { name: 'Proposed related records', exact: true }).getByRole('button', { name: 'Cancel', exact: true })] });
      }
      await graph.getByRole('navigation', { name: 'Authored occurrences', exact: true }).getByRole('button').filter({ hasText: 'ResearchStudy' }).click();
      await expect(candidate).toHaveAttribute('data-preview-receipt-id', receipt);
      await expect(expression.getByRole('button', { name: /^Edit source rows:/ })).toBeDisabled();
      await expect(expression.getByRole('button', { name: 'Define related-record rows', exact: true })).toBeDisabled();
      assert.deepEqual(identity(await run.state()), identity(original));
    });
    await run.action('Ownership: table switching stales the earlier owner without saving or retargeting it', async () => {
      await page.getByTestId('construction-table-DocumentReference').click();
      await expect(page.getByRole('button', { name: 'Cancel earlier recipe change', exact: true })).toBeVisible();
      await expect(page.getByRole('button', { name: 'Apply relationship', exact: true })).toHaveCount(0);
      assert.deepEqual((await run.state()).workspace, original.workspace);
      await page.getByTestId('construction-table-ResearchSubject').click();
      await expect(page.getByRole('button', { name: 'Cancel earlier recipe change', exact: true })).toBeVisible();
    });
  }
  await run.action(`${mode}: Cancel preserves complete saved recipe`, async () => {
    const staleCancel = page.getByRole('button', { name: 'Cancel earlier recipe change', exact: true });
    if (await staleCancel.count()) await staleCancel.click();
    else await page.getByRole('region', { name: 'Proposed related records', exact: true }).getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(expression).toHaveAttribute('data-recipe-state', 'saved');
    await expect(graph).toHaveAttribute('data-projection-kind', 'saved');
    assert.deepEqual((await run.state()).workspace, original.workspace);
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalRows));
  });
  await start(); await preview();
  await run.action(`${mode}: Apply same candidate`, async () => {
    await page.getByRole('region', { name: 'Proposed related records', exact: true }).getByRole('button', { name: 'Apply relationship', exact: true }).click();
    await expect(page.getByRole('region', { name: 'Proposed related records', exact: true })).toHaveCount(0);
  });
  let saved = await run.state();
  const addition = documentFor(saved).importedSource.relationships.additions.find(item => item.resourceType === 'ResearchStudy');
  assert(addition); assert.equal(addition.parentOccurrenceId, 'base'); assert.equal(addition.matchMode, 'OPTIONAL');
  assert(['study', 'study_ResearchStudy'].includes(addition.relationship));
  const idField = addition.fields.find(field => ['id', 'ResearchStudy.id'].includes(field.fieldPath));
  assert(idField); assert.equal(idField.valueMode, 'ALL');
  const column = documentFor(saved).columns.find(column => column.columnId === idField.columnId);
  assert(column);
  const root = documentFor(saved).columns.find(column => column.source?.imported?.outputColumn === 'id');
  assert(root);
  let exactRows;
  const assertRows = async (required = false, candidate = false) => {
    const value = await run.preview({ candidate });
    exactRows = sorted(value.rows.map(row => ({ identity: [row[root.column]], studyIds: row[column.column] })));
    assert.deepEqual(exactRows,
      sorted(oracle.nativeStudy.filter(row => !required || row.studyIds.length).map(({ identity, studyIds }) => ({ identity, studyIds }))));
  };
  await assertRows();
  await run.action(`${mode}: reload both authoritative projections`, async () => {
    await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click();
    await assertRows();
    await expect(expression.locator(`[data-term-kind="connection"][data-occurrence-id="${addition.occurrenceId}"]`)).toContainText('ResearchStudy');
    await expect(graph.locator(`button[data-term-kind="connection"][data-occurrence-id="${addition.occurrenceId}"]`)).toContainText('ResearchStudy');
  });
  // Continue in the opposite editor, editing this exact occurrence in place.
  const region = mode === 'graph-first' ? expression : page.getByTestId('authored-occurrence-inspector');
  if (mode === 'row-first') await graph.locator(`button[data-term-kind="connection"][data-occurrence-id="${addition.occurrenceId}"]`).click();
  const match = region.getByRole('combobox', { name: /Match policy for .*ResearchStudy.*OPTIONAL/ });
  await match.selectOption('REQUIRED');
  const review = page.getByRole('region', { name: 'Proposed related records', exact: true });
  await review.getByRole('button', { name: 'Preview relationship', exact: true }).click();
  await expect(review.getByRole('button', { name: 'Apply relationship', exact: true })).toBeEnabled();
  await assertRows(true, true);
  await review.getByRole('button', { name: 'Apply relationship', exact: true }).click();
  await expect(review).toHaveCount(0);
  await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click();
  await assertRows(true);
  const observation = await run.action(`${mode}: edit the shared Observation list policy from the preferred editor`, () =>
    editGraphRowsObservationList({ run, page, request, entry: mode, expectedIdentities: ['rs-one', 'rs-many', 'rs-shared'] }));
  saved = await run.state();
  const after = documentFor(saved);
  const finalPreview = await run.preview();
  exactRows = sorted(finalPreview.rows.map(row => ({ identity: [row[root.column]], studyIds: row[column.column], observationValues: row[observation.column.column] })));
  const expectedFinal = oracle.nativeStudy.filter(row => row.studyIds.length).map(({ identity, studyIds }) => ({ identity, studyIds,
    observationValues: oracle.preserving.find(row => row.identity[0] === identity[0]).observationValues }));
  assert.deepEqual(exactRows, sorted(expectedFinal));
  assert(after.importedSource.relationships.matchOverrides.some(item => item.occurrenceId === addition.occurrenceId && item.matchMode === 'REQUIRED') || after.importedSource.relationships.additions.find(item => item.occurrenceId === addition.occurrenceId).matchMode === 'REQUIRED');
  assert.equal(after.importedSource.relationships.additions.filter(item => item.resourceType === 'ResearchStudy').length, 1, 'Editing a term must not append a duplicate occurrence');
  assert.deepEqual(after.importedRecipe, originalDocument.importedRecipe);
  assert.deepEqual(after.construction, originalDocument.construction, 'A connection projection cannot erase unrelated operations');
  assert(!run.report.requests.flatMap(request => request.commandTypes ?? []).includes('ADD_ROUTE'));
  const originalColumnIDs = new Set(originalDocument.columns.map(item => item.columnId));
  assert.deepEqual(after.columns.filter(item => originalColumnIDs.has(item.columnId) && !observation.affectedColumnIds.includes(item.columnId)), originalDocument.columns.filter(item => !observation.affectedColumnIds.includes(item.columnId)), 'Connection and shared source edits preserve every unrelated original column binding');
  assert.deepEqual(after.columns.filter(item => originalColumnIDs.has(item.columnId)).map(item => item.columnId).sort(), [...originalColumnIDs].sort(), 'The shared Observation policy retains every original column identity');
  const originalStepIDs = new Set(originalDocument.importedSource.steps.map(item => item.id));
  assert.deepEqual(after.importedSource.steps.filter(item => originalStepIDs.has(item.id) && item.id !== observation.step.id), originalDocument.importedSource.steps.filter(item => item.id !== observation.step.id), 'Connection edits preserve unrelated source settings');
  const effectiveMatch = after.importedSource.relationships.matchOverrides?.find(item => item.occurrenceId === addition.occurrenceId)?.matchMode
    ?? after.importedSource.relationships.additions.find(item => item.occurrenceId === addition.occurrenceId).matchMode;
  const catalogEdge = resolveComplementaryCatalogEdge(original.catalog, addition.catalogEdgeId);
  assert.equal(catalogEdge.semantic.from.resourceType, originalDocument.route.resourceType);
  assert.equal(catalogEdge.semantic.to.resourceType, addition.resourceType);
  assert.equal(catalogEdge.semantic.label, addition.relationship);
  assert.equal(catalogEdge.semantic.storageDirection, addition.storageDirection ?? 'OUTBOUND');
  // Preserve actual generated identities and prove unique snapshot-bound catalog
  // correspondence before alpha-renaming. Policies, fields and order stay exact.
  run.report.complementaryIdentityNormalization = {
    occurrence: { actual: addition.occurrenceId, semanticPath: [originalDocument.route.resourceType, addition.relationship, addition.resourceType] },
    catalogEdge: catalogEdge.mapping,
    fields: addition.fields.map(field => ({ actualColumnId: field.columnId, fieldPath: field.fieldPath })),
  };
  run.report.complementaryCanonicalSemantics = {
    pin: after.importedRecipe, sourceRows: after.rows,
    originalColumns: after.columns.filter(item => originalColumnIDs.has(item.columnId)),
    originalSourceSettings: after.importedSource.steps.filter(item => originalStepIDs.has(item.id)),
    orderedConstruction: after.construction ?? null,
    connection: { parent: originalDocument.route.resourceType, catalogEdge: catalogEdge.semantic,
      relationship: addition.relationship, resourceType: addition.resourceType, storageDirection: addition.storageDirection ?? 'OUTBOUND',
      matchMode: effectiveMatch, fields: addition.fields.map(({ fieldPath, valueMode, label }) => ({ fieldPath, valueMode, label })) },
    observationPolicy: { occurrencePath: ['ResearchSubject', 'Patient', 'Observation'], sourceKey: observation.step.sourceKey, valueMode: after.importedSource.steps.find(item => item.id === observation.step.id).valueMode },
    exactRows,
  };
}
