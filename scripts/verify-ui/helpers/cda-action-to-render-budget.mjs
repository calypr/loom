export const CDA_ACTION_TO_RENDER_BUDGET_MS = 5_000;

export function summarizeCdaActionToRenderTimings(timings, budgetMs = CDA_ACTION_TO_RENDER_BUDGET_MS) {
  if (!Array.isArray(timings) || timings.length === 0) {
    throw new TypeError('Action-to-render evidence must contain at least one measured checkpoint.');
  }
  if (!Number.isFinite(budgetMs) || budgetMs <= 0) {
    throw new RangeError('Action-to-render budget must be a positive finite duration.');
  }

  const checkpoints = timings.map((timing, index) => {
    if (!timing || typeof timing.name !== 'string' || !timing.name.trim()
      || !Number.isFinite(timing.durationMs) || timing.durationMs < 0) {
      throw new TypeError(`Action-to-render checkpoint ${index} is malformed.`);
    }
    return { name: timing.name, durationMs: timing.durationMs };
  });
  const maximumDurationMs = Math.max(...checkpoints.map(checkpoint => checkpoint.durationMs));
  return {
    budgetMs,
    checkpointCount: checkpoints.length,
    maximumDurationMs,
    withinBudget: checkpoints.every(checkpoint => checkpoint.durationMs <= budgetMs),
    checkpoints,
  };
}
