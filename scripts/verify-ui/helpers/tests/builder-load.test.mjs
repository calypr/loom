import test from 'node:test';
import assert from 'node:assert/strict';
import { isLoadedBuilderSnapshot } from '../../workflows/builder-load.mjs';
import { summarizeLifecycleEvidence } from '../coverage-status.mjs';
import { scenarioCaseFor } from '../../registry.mjs';

test('Builder readiness accepts the selected empty workspace or a populated ready preview', () => {
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: true, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), true);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 1, previewStatus: 'ready',
  }, 'owned-explorer'), true);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'other-explorer', emptyWorkspaceVisible: true, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), false);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 1, previewStatus: 'loading',
  }, 'owned-explorer'), false);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), false);
});

test('Builder list and state recovery require their emitted five-second Retry timing check', () => {
  const check = 'builder in-app Retry action-to-render within budget';
  for (const caseName of ['list', 'state']) {
    const contract = scenarioCaseFor('builder-load', caseName);
    assert.ok(contract.requiredChecks.includes(check), `${caseName} requires its Retry timing check`);
    assert.equal(contract.lifecycleEvidence, undefined, `${caseName} does not claim aggregate lifecycle performance`);
    assert.equal(contract.playwrightGrep,
      caseName === 'list'
        ? 'Builder load list recovery shows the list failure and recovers through the in-app Retry'
        : 'Builder load state recovery shows the state failure and recovers through the in-app Retry',
      `${caseName} is bound to its native test title`);

    const summarized = (elapsedMs, afterMs) => summarizeLifecycleEvidence({
      dimensions: { performance: { status: elapsedMs <= 5000 ? 'passed' : 'failed' } },
      assertions: [{
        name: check,
        dimension: 'performance',
        status: elapsedMs <= 5000 ? 'passed' : 'failed',
        evidence: { elapsedMs, afterMs, budgetMs: 5000 },
      }],
    }, contract);
    const atBoundary = summarized(5000, 4999);
    assert.equal(atBoundary.renderCheckpoints.status, 'present');
    assert.equal(atBoundary.renderCheckpoints.count, 1);
    assert.equal(atBoundary.renderCheckpoints.maximumDurationMs, 5000);
    const overBudget = summarized(5001, 1);
    assert.equal(overBudget.dimensionEvidence.performance.status, 'failed');
  }
});
