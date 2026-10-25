import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
const requireUiPackage = createRequire(fileURLToPath(new URL('../../../../ui/packages/loom-ui/package.json', import.meta.url)));
const { JSDOM } = requireUiPackage('jsdom');
import {
  assertMountedRelatedSourceMatchesSaved,
  constructionHistoryDOMProjection,
  groupEditRenderBudgetMs,
  groupEditRenderCheckpointNames,
  groupEditRenderCheckpointSummary,
  isProtectedExplorerRequestPath,
  mountedRelatedSourceFromDOM,
  recordGroupEditActionToRender,
  savedColumnLabelsForIds,
} from '../../workflows/verify-cda-group-edit-before-related-column-browser.mjs';
import { browserEval } from '../cda-playwright.mjs';

const completeCheckpoints = () => groupEditRenderCheckpointNames.map((name) => ({ name, durationMs: 4999 }));

test('protected Explorer request guard matches its exact project path and descendants only', () => {
  const root = '/api/v1/projects/qa-project/explorers';
  assert.equal(isProtectedExplorerRequestPath(root + '/protected-id', root, 'protected-id'), true);
  assert.equal(isProtectedExplorerRequestPath(root + '/protected-id/authoring/v2/builder', root, 'protected-id'), true);
  assert.equal(isProtectedExplorerRequestPath(root + '/protected-id-copy', root, 'protected-id'), false);
  assert.equal(isProtectedExplorerRequestPath('/api/v1/projects/other/explorers/protected-id', root, 'protected-id'), false);
});

test('Group edit action-to-render recording enforces the five-second budget', () => {
  const report = { cases: [] };
  assert.equal(recordGroupEditActionToRender(report, 'rendered preview', 1000, {}, () => 1000 + groupEditRenderBudgetMs),
    groupEditRenderBudgetMs);
  assert.throws(() => recordGroupEditActionToRender(report, 'slow rendered preview', 1000, {},
    () => 1001 + groupEditRenderBudgetMs), /took 5001 ms/);
  assert.equal(report.cases.length, 1);
});

test('Group edit performance summary rejects missing, wrong, duplicate, and over-budget checkpoints', () => {
  assert.equal(groupEditRenderCheckpointSummary(completeCheckpoints()).status, 'passed');

  const missing = completeCheckpoints().slice(1);
  const missingSummary = groupEditRenderCheckpointSummary(missing);
  assert.equal(missingSummary.status, 'failed');
  assert.deepEqual(missingSummary.missing, [groupEditRenderCheckpointNames[0]]);

  const wrong = completeCheckpoints();
  wrong[0] = { ...wrong[0], name: 'wrong checkpoint' };
  const wrongSummary = groupEditRenderCheckpointSummary(wrong);
  assert.equal(wrongSummary.status, 'failed');
  assert.deepEqual(wrongSummary.unexpected, ['wrong checkpoint']);
  assert.deepEqual(wrongSummary.missing, [groupEditRenderCheckpointNames[0]]);

  const duplicate = completeCheckpoints();
  duplicate.push({ ...duplicate[0] });
  assert.equal(groupEditRenderCheckpointSummary(duplicate).status, 'failed');

  const slow = completeCheckpoints();
  slow[0] = { ...slow[0], durationMs: groupEditRenderBudgetMs + 1 };
  const slowSummary = groupEditRenderCheckpointSummary(slow);
  assert.equal(slowSummary.status, 'failed');
  assert.deepEqual(slowSummary.overBudget, [{ name: groupEditRenderCheckpointNames[0], durationMs: 5001 }]);
});

test('saved Group labels resolve from source columns and expansion step outputs', () => {
  const savedDocument = {
    columns: [{ columnId: 'root-source-id', label: 'Specimen ID' }],
    construction: {
      steps: [{
        id: 'saved-expansion',
        operation: { kind: 'RELATED_EXPAND' },
        outputs: [{ id: 'related-record-id', label: 'Patient FHIR resource ID' }],
      }],
    },
  };
  assert.deepEqual(savedColumnLabelsForIds(savedDocument, ['root-source-id', 'related-record-id']),
    ['Specimen ID', 'Patient FHIR resource ID']);
});

test('saved Group label resolution rejects missing and conflicting column identities', () => {
  const savedDocument = {
    columns: [{ columnId: 'root-source-id', label: 'Specimen ID' }],
    construction: {
      steps: [{
        id: 'saved-expansion',
        operation: { kind: 'RELATED_EXPAND' },
        outputs: [{ id: 'related-record-id', label: 'Patient FHIR resource ID' }],
      }],
    },
  };
  assert.throws(() => savedColumnLabelsForIds(savedDocument, ['missing-column-id']), /must resolve/);
  assert.throws(() => savedColumnLabelsForIds({
    ...savedDocument,
    construction: { steps: [...savedDocument.construction.steps, {
      id: 'conflicting-output',
      operation: { kind: 'RELATED_EXPAND' },
      outputs: [{ id: 'root-source-id', label: 'Conflicting label' }],
    }] },
  }, ['root-source-id']), /conflicting labels/);
});

test('mounted RELATED_SOURCE capture reads its step under a closed Column adjustments disclosure', async () => {
  const dom = new JSDOM(`
    <section aria-label="Row operations">
      <ol>
        <li><button data-testid="construction-history-step-group-step-1" title="Counts rows"><span>Group rows</span></button></li>
        <li>
          <details data-testid="construction-history-adjustment-group-related-step-1">
            <summary><span aria-hidden="true">▸</span>Column adjustments (1)</summary>
            <ol><li><button data-testid="construction-history-step-related-step-1" title="Add all matching values from Observation.status via Specimen -[subject]-&gt; Patient -[subject]- Observation as Observation status."><span>Related Observation</span></button></li></ol>
          </details>
        </li>
      </ol>
    </section>
    <div data-testid="preview-table-scroll"><div role="table">
      <div role="row"><div role="columnheader" style="text-transform: uppercase">Patient FHIR resource ID</div><div role="columnheader" style="text-transform: uppercase">Observation status</div></div>
    </div></div>
  `);
  const { document } = dom.window;
  const disclosure = document.querySelector('details[data-testid="construction-history-adjustment-group-related-step-1"]');
  assert.equal(disclosure.open, false, 'The retained reload renders the related step inside a closed disclosure.');
  const hadGlobalDocument = Object.hasOwn(globalThis, 'document');
  const previousGlobalDocument = globalThis.document;
  globalThis.document = document;
  let historySteps;
  try {
    historySteps = await browserEval({ evaluate: (callback, args) => callback(args) }, constructionHistoryDOMProjection);
  } finally {
    if (hadGlobalDocument) globalThis.document = previousGlobalDocument;
    else delete globalThis.document;
  }
  const headerCells = [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')];
  const renderedRows = {
    // The retained Chromium innerText is CSS transformed; textContent remains the semantic label.
    headers: ['PATIENT FHIR RESOURCE ID', 'OBSERVATION STATUS'],
    semanticHeaders: headerCells.map(cell => cell.textContent.replace(/\s+/g, ' ').trim()),
  };
  const savedRelatedSource = {
    stepId: 'related-step-1',
    inputStepId: 'group-step-1',
    source: { resourceType: 'Observation', path: 'status' },
    route: [
      { fromResourceType: 'Specimen', toResourceType: 'Patient', edgeId: 'edge-1' },
      { fromResourceType: 'Patient', toResourceType: 'Observation', edgeId: 'edge-2' },
    ],
    outputColumnId: 'related-output-1',
    output: { id: 'related-output-1', label: 'Observation status' },
  };
  const mounted = mountedRelatedSourceFromDOM({ historySteps, renderedRows, savedRelatedSource });

  assert.equal(historySteps[1]?.title, 'Related Observation');
  assert.equal(historySteps[1]?.adjustmentGroup?.open, false);
  assert.equal(mounted.stepId, savedRelatedSource.stepId);
  assert.equal(mounted.inputStepId, savedRelatedSource.inputStepId);
  assert.equal(mounted.historyDisclosure?.title, '▸Column adjustments (1)');
  assert.equal(mounted.historyDisclosure?.open, false);
  assert.deepEqual(mounted.visibleColumn, {
    id: 'related-output-1',
    label: 'Observation status',
    visible: true,
    evidence: 'label-from-visible-table-header-dom-textContent; rendered-innerText-confirmed; id-joined-from-raw-builder-projection',
  });
  assertMountedRelatedSourceMatchesSaved(mounted, savedRelatedSource);
  assert.throws(() => assertMountedRelatedSourceMatchesSaved(mounted, {
    ...savedRelatedSource,
    outputColumnId: 'wrong-output-id',
  }), /actual construction output/);
  assert.throws(() => assertMountedRelatedSourceMatchesSaved(mounted, {
    ...savedRelatedSource,
    output: { ...savedRelatedSource.output, label: 'Wrong label' },
  }), /visibleColumn\.label/);
  assert.throws(() => mountedRelatedSourceFromDOM({
    historySteps,
    renderedRows: { ...renderedRows, semanticHeaders: ['Patient FHIR resource ID', 'Observation value'] },
    savedRelatedSource,
  }), /actual table-header DOM textContent/);
  dom.window.close();
});
