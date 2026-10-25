import assert from 'node:assert/strict';
import test from 'node:test';
import {
  groupEditRenderBudgetMs,
  groupEditRenderCheckpointNames,
  groupEditRenderCheckpointSummary,
  isProtectedExplorerRequestPath,
  recordGroupEditActionToRender,
  savedColumnLabelsForIds,
} from '../../workflows/verify-cda-group-edit-before-related-column-browser.mjs';

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
