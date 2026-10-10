import assert from 'node:assert/strict';
import test from 'node:test';
import { createReport } from '../report.mjs';
import {
  actionToRenderBudgetMs,
  actionToRenderBudgetMsForReport,
  DEFAULT_ACTION_TO_RENDER_BUDGET_MS,
  FULL_POPULATION_PIVOT_ACTION_TO_RENDER_BUDGET_MS,
  recordPivotActionToRender,
  waitForPivotObservable,
} from '../quantity-pivot-budget.mjs';

test('only registered full-population Pivot cases use the ten-second action-to-render budget', () => {
  for (const caseName of ['full-population-discovery', 'full-population-lifecycle', 'related-text-only-full-population-lifecycle']) {
    assert.equal(actionToRenderBudgetMs({ scenarioID: 'root-quantity-pivot', caseName }), 10_000, caseName);
  }

  for (const identity of [
    { scenarioID: 'root-quantity-pivot', caseName: 'fixture-lifecycle' },
    { scenarioID: 'builder-combine-draft', caseName: 'append' },
    { scenarioID: 'root-quantity-pivot', caseName: 'unknown-case' },
  ]) {
    assert.equal(actionToRenderBudgetMs(identity), 5_000, JSON.stringify(identity));
  }

  assert.equal(DEFAULT_ACTION_TO_RENDER_BUDGET_MS, 5_000);
  assert.equal(FULL_POPULATION_PIVOT_ACTION_TO_RENDER_BUDGET_MS, 10_000);
});

test('related full-population Pivot waits and render gates use ten seconds while fallback waits stay capped at five', async () => {
  const cdaReport = createReport({
    scenario: 'root-quantity-pivot',
    caseName: 'related-text-only-full-population-lifecycle',
    target: {},
    evidenceDirectory: '/tmp/pivot-budget-test',
  });
  assert.equal(cdaReport.case, 'related-text-only-full-population-lifecycle');
  assert.equal(Object.hasOwn(cdaReport, 'caseName'), false);
  const budgetMs = actionToRenderBudgetMsForReport(cdaReport);
  assert.equal(budgetMs, 10_000);
  const pageCalls = [];
  const fallbackCalls = [];
  const page = { waitForFunction: async (predicate, args, options) => pageCalls.push({ predicate, args, options }) };
  const fallbackWait = async (...args) => fallbackCalls.push(args);
  const predicate = () => true;
  const argument = { outputId: 'output-1' };

  await waitForPivotObservable({ page, fallbackWait, predicate, argument, timeoutMs: 15_000, budgetMs });
  assert.equal(pageCalls.length, 1);
  assert.deepEqual(pageCalls[0].args, argument);
  assert.deepEqual(pageCalls[0].options, { timeout: 10_000 });
  assert.equal(fallbackCalls.length, 0);
  const waitFailure = new Error('native wait failure');
  await assert.rejects(waitForPivotObservable({
    page: { waitForFunction: async () => { throw waitFailure; } },
    fallbackWait,
    predicate,
    argument,
    timeoutMs: 15_000,
    budgetMs,
  }), error => error === waitFailure);

  const cases = [];
  const timing = recordPivotActionToRender({ cases, name: 'related-Pivot-to-render', startedAt: 100, finishedAt: 9_600, budgetMs });
  assert.deepEqual(timing, { name: 'related-Pivot-to-render', durationMs: 9_500, budgetMs: 10_000 });
  assert.deepEqual(cases, [timing]);
  assert.throws(() => recordPivotActionToRender({ cases, name: 'too-slow', startedAt: 0, finishedAt: 10_001, budgetMs }), /budget 10000ms/);

  const fixtureBudgetMs = actionToRenderBudgetMs({ scenarioID: 'root-quantity-pivot', caseName: 'fixture-lifecycle' });
  await waitForPivotObservable({ page, fallbackWait, predicate, argument, timeoutMs: 15_000, budgetMs: fixtureBudgetMs });
  assert.equal(pageCalls.length, 1);
  assert.equal(fallbackCalls.length, 1);
  assert.equal(fallbackCalls[0][2], 5_000);
  assert.throws(() => recordPivotActionToRender({ cases, name: 'fixture-too-slow', startedAt: 0, finishedAt: 5_001,
    budgetMs: fixtureBudgetMs }), /budget 5000ms/);
});
