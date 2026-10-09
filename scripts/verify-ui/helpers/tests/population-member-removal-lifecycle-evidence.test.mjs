import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildPopulationMemberRemovalLifecycleEvidence,
  recordPopulationMemberRemovalLifecycleCheckpoint,
} from '../../workflows/population-member-removal-workflow.mjs';

const checkpointNames = [
  'remove-click-to-exact-candidate-rows',
  'cancel-click-to-exact-baseline-rows',
  'reopen-click-to-exact-candidate-rows',
  'apply-click-to-exact-candidate-rows',
  'reload-to-exact-candidate-rows',
  'undo-click-to-exact-baseline-rows',
  'restore-reload-to-exact-baseline-rows',
];

const input = () => ({
  checkpoints: checkpointNames.map((name, index) => ({
    name,
    durationMs: [100, 200, 300, 400, 1800, 437, 2410][index],
  })),
  actionDurationsMs: [261, 222, 266, 287, 5000],
});

test('builds performance evidence from the exact seven required rendered transitions', () => {
  const evidence = buildPopulationMemberRemovalLifecycleEvidence(input());

  assert.deepEqual(evidence, {
    actionCount: 5,
    maxActionMs: 5000,
    measuredTransitionCount: 7,
    checkpoints: [
      { name: 'remove-click-to-exact-candidate-rows', durationMs: 100, budgetMs: 5000, withinBudget: true },
      { name: 'cancel-click-to-exact-baseline-rows', durationMs: 200, budgetMs: 5000, withinBudget: true },
      { name: 'reopen-click-to-exact-candidate-rows', durationMs: 300, budgetMs: 5000, withinBudget: true },
      { name: 'apply-click-to-exact-candidate-rows', durationMs: 400, budgetMs: 5000, withinBudget: true },
      { name: 'reload-to-exact-candidate-rows', durationMs: 1800, budgetMs: 5000, withinBudget: true },
      { name: 'undo-click-to-exact-baseline-rows', durationMs: 437, budgetMs: 5000, withinBudget: true },
      { name: 'restore-reload-to-exact-baseline-rows', durationMs: 2410, budgetMs: 5000, withinBudget: true },
    ],
  });
});

test('accepts an action and rendered transition at the exact five-second boundary', () => {
  const value = input();
  value.actionDurationsMs[0] = 5000;
  value.checkpoints[0].durationMs = 5000;

  const evidence = buildPopulationMemberRemovalLifecycleEvidence(value);

  assert.equal(evidence.maxActionMs, 5000);
  assert.deepEqual(evidence.checkpoints[0], {
    name: 'remove-click-to-exact-candidate-rows', durationMs: 5000, budgetMs: 5000, withinBudget: true,
  });
});

test('retains an over-budget checkpoint together with its failed budget result', () => {
  const checkpoints = [{ name: 'remove-click-to-exact-candidate-rows', durationMs: 120 }];

  const recorded = recordPopulationMemberRemovalLifecycleCheckpoint(
    checkpoints, 'cancel-click-to-exact-baseline-rows', 5001,
  );

  assert.deepEqual(recorded.checkpoints, [
    { name: 'remove-click-to-exact-candidate-rows', durationMs: 120 },
    { name: 'cancel-click-to-exact-baseline-rows', durationMs: 5001 },
  ]);
  assert.equal(recorded.withinBudget, false);
  assert.deepEqual(checkpoints, [{ name: 'remove-click-to-exact-candidate-rows', durationMs: 120 }]);
});

test('rejects missing transition evidence', () => {
  const value = input();
  value.checkpoints.pop();

  assert.throws(() => buildPopulationMemberRemovalLifecycleEvidence(value));
});

test('rejects absent action timings and malformed transition timing', () => {
  const missingActionTiming = input();
  missingActionTiming.actionDurationsMs = [];
  assert.throws(() => buildPopulationMemberRemovalLifecycleEvidence(missingActionTiming));

  const malformedTransitionTiming = input();
  malformedTransitionTiming.checkpoints[0] = {
    ...malformedTransitionTiming.checkpoints[0], durationMs: Number.NaN,
  };
  assert.throws(() => buildPopulationMemberRemovalLifecycleEvidence(malformedTransitionTiming));
});

test('rejects a rendered transition over the five-second budget', () => {
  const value = input();
  value.checkpoints[4] = { ...value.checkpoints[4], durationMs: 5001 };

  assert.throws(() => buildPopulationMemberRemovalLifecycleEvidence(value));
});

test('rejects a native action over the five-second budget', () => {
  const value = input();
  value.actionDurationsMs.push(5001);

  assert.throws(() => buildPopulationMemberRemovalLifecycleEvidence(value));
});
