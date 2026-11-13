import assert from 'node:assert/strict';
import { expect } from 'playwright/test';

const sorted = rows => structuredClone(rows).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
const expectedLists = [
  { identity: ['rs-zero'], values: [] }, { identity: ['rs-one'], values: ['one-A'] },
  { identity: ['rs-many'], values: ['many-A', 'many-B'] }, { identity: ['rs-shared'], values: ['shared-A'] },
];

export async function runGraphRowsOrdinaryPolicy({ run, page }) {
  const url = new URL(run.target.uiUrl);
  url.searchParams.set('project', run.target.fixtureProject); url.searchParams.set('explorer', run.explorerId);
  await page.goto(url.href, { waitUntil: 'domcontentloaded' });
  await page.getByRole('button', { name: 'Choose ResearchSubject rows', exact: true }).click();
  await expect(page.getByTestId('construction-preview')).toHaveAttribute('data-preview-status', 'ready');
  await page.getByRole('navigation', { name: 'Recipe presentation', exact: true }).getByRole('button', { name: 'Both editors', exact: true }).click();
  const expression = page.getByTestId('row-recipe-expression');
  await expression.getByRole('button', { name: 'Include related values', exact: true }).click();
  await page.getByRole('tab', { name: 'Other fields', exact: true }).click();
  const search = page.getByRole('searchbox', { name: 'Search dataset fields by name or type', exact: true });
  await search.fill('identifier'); await search.press('Enter');
  const raw = page.getByTestId('feature-catalog-raw-fields');
  await expect(raw).toHaveCount(1);
  await expect(raw).toBeVisible();
  const fieldButton = raw.getByRole('button', { name: /^Select (?:ResearchSubject\.)?(?:ResearchSubject\.)?identifier(?:\[\])?\.value$/ });
  await expect(fieldButton).toHaveCount(1); await fieldButton.click();
  const documentFor = state => state.workspace.documents.find(document => document.rootResourceType === 'ResearchSubject');
  await page.getByRole('button', { name: 'Add 1 selected feature', exact: true }).click();
  // The root field's supported default is resolved by the existing native
  // chooser. If it needs a form choice, choose the explicit list form there.
  const options = page.getByRole('region', { name: 'Advanced route and form choices', exact: true });
  await expect.poll(async () => await options.isVisible() || documentFor(await run.state())?.columns.length === 2).toBe(true);
  if (await options.isVisible()) {
    const all = options.getByRole('radio', { name: /All (?:matching )?values/i });
    await expect(all).toHaveCount(1); await all.check();
    await options.getByRole('button', { name: 'Add 1 selected feature', exact: true }).click();
  }
  await expect.poll(async () => documentFor(await run.state())?.columns.length).toBe(2);
  const original = await run.state(); const document = documentFor(original);
  assert.equal(document.importedRecipe, undefined);
  const fields = document.columns.filter(column => column.source.kind === 'field' && /(?:^|\.)identifier(?:\[\])?\.value$/.test(column.source.field.path));
  assert.equal(fields.length, 1); const field = fields[0];
  const root = document.columns.find(column => column.columnId !== field.columnId);
  assert(root);
  const originalRows = (await run.preview()).rows;
  const open = async () => {
    await page.getByRole('navigation', { name: 'Recipe presentation', exact: true }).getByRole('button', { name: 'Both editors', exact: true }).click();
    await expression.getByRole('textbox', { name: 'Find row recipe column', exact: true }).fill(field.label);
    await expression.locator(`[data-term-kind="column"][data-column-id="${field.columnId}"]`).getByRole('button').click();
    const inspector = page.getByTestId('recipe-column-policy-inspector');
    await expect(inspector).toHaveAttribute('data-column-id', field.columnId);
    return inspector;
  };
  const assertValues = async (mode, candidate = false) => {
    const preview = await run.preview({ candidate });
    const expected = expectedLists.map(({ identity, values }) => ({ identity, values: mode === 'FIRST' ? values[0] ?? null : values }));
    assert.deepEqual(sorted(preview.rows.map(row => ({ identity: [row[root.column]], values: row[field.column] }))), sorted(expected));
  };
  const previewPolicy = async mode => {
    const inspector = await open();
    await inspector.getByRole('combobox', { name: `Value policy for ${field.label}`, exact: true }).selectOption(mode);
    await inspector.getByRole('button', { name: 'Preview column policy', exact: true }).click();
    await expect(inspector.getByRole('button', { name: 'Apply column policy', exact: true })).toBeEnabled();
    await assertValues(mode, true);
    return inspector;
  };
  const firstMode = field.source.field.projectionMode === 'ALL' ? 'FIRST' : 'ALL';
  await run.action('Ordinary policy: Preview and Cancel retain exact saved column and draft', async () => {
    const inspector = await previewPolicy(firstMode);
    await inspector.getByRole('button', { name: 'Cancel column edit', exact: true }).click();
    await expect(inspector).toHaveCount(0);
    const state = await run.state();
    assert.deepEqual([state.draftVersion, state.draftDigest], [original.draftVersion, original.draftDigest]);
    assert.deepEqual(state.workspace, original.workspace);
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalRows));
  });
  for (const mode of [firstMode, firstMode === 'ALL' ? 'FIRST' : 'ALL']) {
    await run.action(`Ordinary policy: Apply and reload ${mode} on the same column`, async () => {
      const inspector = await previewPolicy(mode);
      await inspector.getByRole('button', { name: 'Apply column policy', exact: true }).click(); await expect(inspector).toHaveCount(0);
      await page.reload({ waitUntil: 'domcontentloaded' }); await assertValues(mode);
      const after = documentFor(await run.state());
      assert.equal(after.columns.length, document.columns.length);
      const edited = after.columns.find(column => column.columnId === field.columnId);
      assert.equal(edited.source.field.projectionMode, mode);
      assert.deepEqual({ ...edited, source: { ...edited.source, field: { ...edited.source.field, projectionMode: field.source.field.projectionMode } } }, field);
      assert.deepEqual(after.columns.filter(column => column.columnId !== field.columnId), document.columns.filter(column => column.columnId !== field.columnId));
      assert.deepEqual(after.route, document.route); assert.deepEqual(after.rows, document.rows); assert.deepEqual(after.construction, document.construction);
    });
  }
  assert.equal(run.report.requests.flatMap(request => request.commandTypes ?? []).filter(type => type === 'APPLY_COLUMN_POLICY_PROPOSAL').length, 2, 'Both policy saves must apply their previewed typed proposal');
}
