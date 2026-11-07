import { isDeepStrictEqual } from 'node:util';

export function timingSummary(values) {
  if (!values.length || values.some(value => !Number.isFinite(value) || value <= 0))
    throw new Error('Timing samples must be finite and positive');
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return { count: sorted.length, medianMs: sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2, minMs: sorted[0], maxMs: sorted.at(-1) };
}

export function chooseConcurrency(trials) {
  const valid = trials.filter(trial => trial.status === 'passed' && Number.isFinite(trial.steadyMs) && trial.steadyMs > 0);
  if (!valid.length) throw new Error('No correct tuning configuration');
  const fastest = Math.min(...valid.map(trial => trial.steadyMs));
  // Prefer fewer processes when exploratory work times are within five percent.
  // Separate confirmation trials validate the selected setting.
  return valid.filter(trial => trial.steadyMs <= fastest * 1.05).sort((a, b) => a.concurrency - b.concurrency)[0].concurrency;
}

export function compareTrials({ trials, caseIds, buildMs, completeSelection }) {
  const result = { verdict: 'INCONCLUSIVE', adopted: false, reason: 'Requires at least five valid trials per engine with equivalent observations.', caseResults: [] };
  if (!caseIds.length || !Number.isFinite(buildMs) || buildMs < 0) return result;
  const sides = Object.fromEntries(['chromium', 'lightpanda'].map(engine => [engine, trials.filter(trial => trial.engine === engine)]));
  const allSides = Object.values(sides);
  if (allSides.some(side => !side.length)) return result;
  const validCase = record => record?.status === 'passed' && record.observation !== undefined &&
    !record.error && !record.cleanupError && record.errors?.length === 0 && record.failedRequests?.length === 0 &&
    Array.isArray(record.responses) && record.responses.every(response => response.status >= 200 && response.status < 400) &&
    Number.isFinite(record.durationMs) && record.durationMs > 0;
  for (const id of caseIds) {
    const records = allSides.flatMap(side => side.map(trial => trial.cases?.find(record => record.id === id)));
    const equivalent = records.every(validCase) && records.every(record => isDeepStrictEqual(record.observation, records[0].observation));
    if (!equivalent) { result.caseResults.push({ id, equivalent: false, speedup: null }); continue; }
    const timings = Object.fromEntries(Object.entries(sides).map(([engine, side]) =>
      [engine, timingSummary(side.map(trial => trial.cases.find(record => record.id === id).durationMs))]));
    result.caseResults.push({ id, equivalent: true, ...timings, speedup: timings.chromium.medianMs / timings.lightpanda.medianMs });
  }
  const compatible = trials.every(trial => trial.status === 'passed' && !trial.error && !trial.cleanupError &&
    !trial.cleanup?.errors?.length &&
    trial.cleanup?.browserConnected === false && trial.cleanup?.lightpandaStopped === true &&
    isDeepStrictEqual(trial.cases?.map(record => record.id), caseIds)) && result.caseResults.every(record => record.equivalent);
  if (!compatible) return { ...result, verdict: 'REJECTED', reason: 'A required journey failed, cleanup failed, or observations differed. Failed durations are not performance results.' };
  if (allSides.some(side => side.length < 5) || sides.chromium.length !== sides.lightpanda.length) return result;
  const chromium = timingSummary(sides.chromium.map(trial => trial.durationMs));
  const lightpanda = timingSummary(sides.lightpanda.map(trial => trial.durationMs));
  const speedup = chromium.medianMs / lightpanda.medianMs;
  const withBuildSpeedup = (chromium.medianMs + buildMs) / (lightpanda.medianMs + buildMs);
  Object.assign(result, { chromium, lightpanda, speedup, withBuildSpeedup });
  if (speedup <= 5 || withBuildSpeedup <= 5)
    return { ...result, verdict: 'REJECTED', reason: 'The equivalent selected workload did not exceed 5x both with and without build preparation.' };
  if (chromium.medianMs - lightpanda.medianMs <= Math.max(chromium.maxMs - chromium.minMs, lightpanda.maxMs - lightpanda.minMs))
    return { ...result, reason: 'The run-to-run spread exceeds the measured difference.' };
  if (!completeSelection) return { ...result, reason: 'A selected subset cannot establish whole-suite adoption.' };
  return { ...result, verdict: 'CANDIDATE', reason: 'Timing gate passed. Limiter evidence and a representative deployed journey are still required before adoption.' };
}
