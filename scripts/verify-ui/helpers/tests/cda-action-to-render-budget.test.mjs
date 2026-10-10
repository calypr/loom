import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CDA_ACTION_TO_RENDER_BUDGET_MS,
  summarizeCdaActionToRenderTimings,
} from '../cda-action-to-render-budget.mjs';

test('action-to-render summary keeps every checkpoint and enforces the five-second boundary', () => {
  const timings = [
    { name: 'PRESERVE_PARENT preview', durationMs: 1_644 },
    { name: 'saved GROUP reload', durationMs: 5_000 },
  ];
  assert.deepEqual(summarizeCdaActionToRenderTimings(timings), {
    budgetMs: CDA_ACTION_TO_RENDER_BUDGET_MS,
    checkpointCount: 2,
    maximumDurationMs: 5_000,
    withinBudget: true,
    checkpoints: [
      { name: 'PRESERVE_PARENT preview', durationMs: 1_644 },
      { name: 'saved GROUP reload', durationMs: 5_000 },
    ],
  });
  assert.equal(summarizeCdaActionToRenderTimings([{ name: 'slow preview', durationMs: 5_001 }]).withinBudget, false);
  assert.throws(() => summarizeCdaActionToRenderTimings([]), /at least one measured checkpoint/);
  assert.throws(() => summarizeCdaActionToRenderTimings([{ name: 'missing duration' }]), /malformed/);
});
