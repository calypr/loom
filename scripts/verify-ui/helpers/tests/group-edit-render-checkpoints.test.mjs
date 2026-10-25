import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
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
  mountedGroupFromHistoryDOM,
  mountedRelatedSourceFromDOM,
  recordGroupEditActionToRender,
  savedColumnLabelsForIds,
} from '../../workflows/verify-cda-group-edit-before-related-column-browser.mjs';
import { browserEval } from '../cda-playwright.mjs';

const completeCheckpoints = () => groupEditRenderCheckpointNames.map((name) => ({ name, durationMs: 4999 }));
const productionRouteDisplayLabel = route => {
  const moduleUrl = new URL('../../../../ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/routeDisplay.ts', import.meta.url).href;
  const script = `import { relationshipLabel } from ${JSON.stringify(moduleUrl)}; const route = ${JSON.stringify(route)}; process.stdout.write(JSON.stringify(route.map(relationshipLabel).join(' → ')));`;
  const result = spawnSync(process.execPath,
    ['--experimental-strip-types', '--input-type=module', '--eval', script],
    { encoding: 'utf8', timeout: 5000 });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
};

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
  const savedRelatedSource = {
    stepId: 'related-step-1',
    inputStepId: 'group-step-1',
    source: {
      kind: 'FIELD', candidateId: 'candidate-1', nodeId: 'node-observation',
      resourceType: 'Observation', path: 'status', cardinality: 'optional_one', logicalType: 'string',
    },
    route: [
      {
        edgeId: 'edge-1', fromNodeId: 'node-1', toNodeId: 'node-2',
        fromResourceType: 'Specimen', toResourceType: 'Patient', relationship: 'subject_Patient',
        storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL',
      },
      {
        edgeId: 'edge-2', fromNodeId: 'node-2', toNodeId: 'node-3',
        fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient',
        storageDirection: 'INBOUND', matchMode: 'OPTIONAL',
      },
    ],
    outputColumnId: 'related-output-1',
    output: { id: 'related-output-1', label: 'Observation status' },
  };
  // The retained report omitted this closed child title; derive expected text from the retained route and production formatter.
  const expectedFromFormatter = productionRouteDisplayLabel(savedRelatedSource.route);
  assert.equal(expectedFromFormatter, 'Subject → Subject');
  const formattedHistoryTitle = `Add all matching values from Observation.status via ${expectedFromFormatter} as Observation status.`;
  const dom = new JSDOM(`
    <section aria-label="Row operations">
      <ol>
        <li><button data-testid="construction-history-step-group-step-1" title="Group rows by Patient FHIR resource ID; Count distinct values of Observation FHIR resource ID counts distinct Observation FHIR resource ID."><span>Group by Patient FHIR resource ID</span></button></li>
        <li>
          <details data-testid="construction-history-adjustment-group-related-step-1">
            <summary><span aria-hidden="true">▸</span>Column adjustments (1)</summary>
            <ol><li><button data-testid="construction-history-step-related-step-1" title="${formattedHistoryTitle}"><span>Related Observation</span></button></li></ol>
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
  const hadGlobalPerformance = Object.hasOwn(globalThis, 'performance');
  const previousGlobalPerformance = globalThis.performance;
  globalThis.document = document;
  globalThis.performance = dom.window.performance;
  let historyObservation;
  try {
    historyObservation = await browserEval({ evaluate: (callback, args) => callback(args) }, constructionHistoryDOMProjection);
  } finally {
    if (hadGlobalDocument) globalThis.document = previousGlobalDocument;
    else delete globalThis.document;
    if (hadGlobalPerformance) globalThis.performance = previousGlobalPerformance;
    else delete globalThis.performance;
  }
  const historySteps = historyObservation.steps;
  assert.equal(typeof historyObservation.observedAt, 'number');
  assert.equal(historyObservation.documentTimeOrigin, dom.window.performance.timeOrigin);
  const mountedGroup = mountedGroupFromHistoryDOM(historySteps);
  assert.deepEqual(mountedGroup.keyLabels, ['Patient FHIR resource ID']);
  assert.equal(mountedGroup.aggregate, 'COUNT_DISTINCT');
  assert.equal(Object.hasOwn(mountedGroup, 'keys'), false);
  const headerCells = [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')];
  const renderedRows = {
    // The retained Chromium innerText is CSS transformed; textContent remains the semantic label.
    headers: ['PATIENT FHIR RESOURCE ID', 'OBSERVATION STATUS'],
    semanticHeaders: headerCells.map(cell => cell.textContent.replace(/\s+/g, ' ').trim()),
  };
  const mounted = mountedRelatedSourceFromDOM({ historySteps, renderedRows, savedRelatedSource });

  assert.equal(historySteps[1]?.title, 'Related Observation');
  assert.equal(historySteps[1]?.adjustmentGroup?.open, false);
  assert.equal(mounted.stepId, savedRelatedSource.stepId);
  assert.equal(mounted.inputStepId, savedRelatedSource.inputStepId);
  assert.deepEqual(mounted.source, { resourceType: 'Observation', path: 'status' });
  assert.equal(mounted.sourceEvidence, 'construction-history-button-title-attribute');
  assert.equal(Object.hasOwn(mounted.source, 'candidateId'), false);
  assert.equal(mounted.historyText, formattedHistoryTitle);
  assert.equal(mounted.displayedRouteLabel, expectedFromFormatter);
  assert.equal(mounted.displayedRouteEvidence, 'construction-history-button-title-attribute');
  assert.equal(mounted.routeEvidence, 'saved-builder-projection');
  assert.deepEqual(mounted.route, savedRelatedSource.route);
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
  for (const displayedSource of ['Patient.status', 'Observation.code']) {
    const wrongSourceHistory = historySteps.map(step => step.stepId === savedRelatedSource.stepId
      ? { ...step, historyText: formattedHistoryTitle.replace('Observation.status', displayedSource) }
      : step);
    assert.equal(mountedRelatedSourceFromDOM({
      historySteps: wrongSourceHistory,
      renderedRows,
      savedRelatedSource,
    }), null, `Wrong displayed source ${displayedSource} must not be accepted as the mounted source.`);
    assert.throws(() => assertMountedRelatedSourceMatchesSaved(null, savedRelatedSource),
      /No mounted RELATED_SOURCE history row/);
  }
  dom.window.close();
});
