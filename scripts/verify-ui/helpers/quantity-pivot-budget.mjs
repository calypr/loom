export const DEFAULT_ACTION_TO_RENDER_BUDGET_MS = 5_000;
export const FULL_POPULATION_PIVOT_ACTION_TO_RENDER_BUDGET_MS = 10_000;

const fullPopulationCases = new Set([
  'full-population-discovery',
  'full-population-lifecycle',
  'related-text-only-full-population-lifecycle',
]);

export function actionToRenderBudgetMs({ scenarioID, caseName }) {
  return scenarioID === 'root-quantity-pivot' && fullPopulationCases.has(caseName)
    ? FULL_POPULATION_PIVOT_ACTION_TO_RENDER_BUDGET_MS
    : DEFAULT_ACTION_TO_RENDER_BUDGET_MS;
}

export function actionToRenderBudgetMsForReport(report) {
  return actionToRenderBudgetMs({ scenarioID: report?.scenario, caseName: report?.case });
}

export async function waitForPivotObservable({ page, fallbackWait, predicate, argument, timeoutMs, budgetMs }) {
  const boundedTimeoutMs = Math.min(timeoutMs, budgetMs);
  if (budgetMs > DEFAULT_ACTION_TO_RENDER_BUDGET_MS) {
    if (typeof predicate !== 'function') throw new TypeError('Browser waits require an inspection function.');
    if (typeof argument === 'string') throw new TypeError('Browser wait arguments must be structured data, not source code.');
    // The scoped exception uses the same Playwright wait primitive without the shared helper's five-second cap.
    await page.waitForFunction(predicate, argument ?? {}, { timeout: boundedTimeoutMs });
    return;
  }
  return fallbackWait(predicate, argument ?? {}, boundedTimeoutMs);
}

export function recordPivotActionToRender({ cases, name, startedAt, finishedAt = Date.now(), budgetMs }) {
  const durationMs = finishedAt - startedAt;
  if (durationMs > budgetMs) throw new Error(`${name} took ${durationMs}ms (budget ${budgetMs}ms)`);
  const result = { name, durationMs, budgetMs };
  cases.push(result);
  return result;
}
