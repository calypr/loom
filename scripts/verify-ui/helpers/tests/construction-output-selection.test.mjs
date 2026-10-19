import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { chromium } from 'playwright';
import {
  bindUniqueConstructionOutputTitle,
  constructionOutputSelectionEvidence,
  constructionWorkspaceSelectionSnapshot,
} from '../builder-combine-draft-helpers.mjs';

const workflow = readFileSync(new URL('../../workflows/builder-combine-draft.mjs', import.meta.url), 'utf8');

const documents = [
  { output: { id: 'combine-target', title: 'Observation ID source' } },
  { output: { id: 'report-source', title: 'Report source' } },
];

const editorSnapshot = (overrides = {}) => ({
  navigationPresent: false,
  navSelectedOutputId: null,
  editorPresent: true,
  editorVisible: true,
  editorOutputId: 'combine-target',
  editorHeadingTexts: ['Editing Observation ID source'],
  visibleEditorHeadingTexts: ['Editing Observation ID source'],
  ...overrides,
});

test('Combine editor ownership rejects wrong output IDs, duplicate titles, and a missing heading', () => {
  const binding = bindUniqueConstructionOutputTitle(documents, 'combine-target');
  assert.equal(binding.ok, true);
  assert.equal(binding.title, 'Observation ID source');
  const accepted = constructionOutputSelectionEvidence(binding, editorSnapshot());
  assert.equal(accepted.ok, true);
  assert.equal(accepted.selectionSource, 'combine-editor-heading-and-output-id');
  assert.equal(accepted.selectedOutputId, 'combine-target');

  const wrongOutput = constructionOutputSelectionEvidence(binding, editorSnapshot({ editorOutputId: 'other-output' }));
  assert.equal(wrongOutput.ok, false);
  assert.equal(wrongOutput.selectedOutputId, null);

  const duplicateTitleBinding = bindUniqueConstructionOutputTitle([
    ...documents,
    { output: { id: 'duplicate-title', title: 'Observation ID source' } },
  ], 'combine-target');
  assert.equal(duplicateTitleBinding.checks.titleUniqueInWorkspace, false);
  assert.equal(constructionOutputSelectionEvidence(duplicateTitleBinding, editorSnapshot()).ok, false);

  const missingHeading = constructionOutputSelectionEvidence(binding, editorSnapshot({ editorHeadingTexts: [], visibleEditorHeadingTexts: [] }));
  assert.equal(missingHeading.ok, false);
  assert.equal(missingHeading.selectedOutputId, null);

  const visibleNavigationMismatch = constructionOutputSelectionEvidence(binding, editorSnapshot({
    navigationPresent: true,
    navSelectedOutputId: 'other-output',
  }));
  assert.equal(visibleNavigationMismatch.ok, false, 'an existing navigation must still select the exact target');
});

test('Combine editor selector snapshot matches the hidden Tables nav and Editing title DOM in native Playwright', async (t) => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  await page.setContent(`
    <main data-testid="construction-workspace">
      <section>
        <header><h1>Editing Observation ID source</h1></header>
        <section data-testid="construction-operation-editor" data-operation-family="COMBINE" data-output-id="combine-target">
          <div data-testid="construction-combine-editor"></div>
        </section>
      </section>
    </main>
  `);

  const navSelection = page.locator('nav[aria-label="Tables"] button[aria-current="page"][data-testid^="construction-table-"]');
  const combineEditor = page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"][data-output-id]');
  const editorHeading = page.locator('[data-testid="construction-workspace"] h1');
  assert.equal(await navSelection.count(), 0, 'ConstructionWorkspace hides the Tables nav while an editor is open');
  assert.equal(await combineEditor.count(), 1);
  assert.equal(await combineEditor.getAttribute('data-output-id'), 'combine-target');
  assert.equal(await editorHeading.count(), 1);
  assert.equal((await editorHeading.textContent()).trim(), 'Editing Observation ID source');

  const snapshot = await page.evaluate(constructionWorkspaceSelectionSnapshot);
  const evidence = constructionOutputSelectionEvidence(bindUniqueConstructionOutputTitle(documents, 'combine-target'), snapshot);
  assert.equal(snapshot.navigationPresent, false);
  assert.equal(snapshot.navSelectedOutputId, null, 'a missing nav does not itself identify the selected output');
  assert.equal(snapshot.editorVisible, true);
  assert.deepEqual(snapshot.visibleEditorHeadingTexts, ['Editing Observation ID source']);
  assert.equal(evidence.ok, true);
  assert.equal(evidence.selectedOutputId, 'combine-target', 'the editor output ID and unique visible heading jointly prove ownership');
});

test('Combine preview diagnostics retain editor state and do not infer selection from a missing nav', () => {
  assert.match(workflow, /constructionWorkspaceSelectionSnapshot/);
  assert.match(workflow, /selectedOutputId: selection\.navigationPresent \? selection\.navSelectedOutputId : null/);
  assert.match(workflow, /outputId: selection\.editorOutputId/);
  assert.match(workflow, /headingTexts: selection\.editorHeadingTexts/);
  assert.match(workflow, /visibleHeadingTexts: selection\.visibleEditorHeadingTexts/);
  assert.match(workflow, /bindUniqueConstructionOutputTitle\(base\.workspace\?\.documents, target\.outputId\)/);
  assert.match(workflow, /domSelectedOutputMatchesTarget: !requireSelectedPreview \|\| selectionEvidence\.ok/);
});
