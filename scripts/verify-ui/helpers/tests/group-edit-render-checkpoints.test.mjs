import assert from 'node:assert/strict';
import test from 'node:test';
import {
  groupEditRenderBudgetMs,
  groupEditRenderCheckpointNames,
  groupEditRenderCheckpointSummary,
  recordGroupEditActionToRender,
} from '../../workflows/verify-cda-group-edit-before-related-column-browser.mjs';

const completeCheckpoints = () => groupEditRenderCheckpointNames.map((name) => ({ name, durationMs: 4999 }));

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
