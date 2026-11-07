import assert from 'node:assert/strict';
import { test } from 'node:test';
import { compareTrials, timingSummary } from './engine-verdict.mjs';

function comparison() {
  return { caseIds: ['save-reload'], buildMs: 1, completeSelection: true,
    trials: Array.from({ length: 5 }, (_, index) => ['chromium', 'lightpanda'].map(engine => ({
      engine, index, status: 'passed', durationMs: engine === 'chromium' ? 100 : 10,
      cleanup: { browserConnected: false, lightpandaStopped: true },
      cases: [{ id: 'save-reload', status: 'passed', durationMs: engine === 'chromium' ? 90 : 9,
        errors: [], failedRequests: [], responses: [{ status: 200 }], observation: { rows: ['p1'], version: 2 } }],
    }))).flat() };
}

test('fast failures, wrong observations, missing cases, and leaked browsers never earn a speedup', () => {
  for (const corrupt of [
    trial => { trial.status = 'failed'; },
    trial => { trial.cases[0].status = 'failed'; },
    trial => { trial.cases[0].observation.rows = ['wrong']; },
    trial => { trial.cases = []; },
    trial => { trial.cases[0].errors.push('uncaught exception'); },
    trial => { trial.cases[0].failedRequests.push({ error: 'aborted' }); },
    trial => { trial.cases[0].responses[0].status = 500; },
    trial => { trial.cleanup.browserConnected = true; },
    trial => { trial.cases[0].durationMs = 0; },
  ]) {
    const input = comparison();
    corrupt(input.trials[1]);
    const result = compareTrials(input);
    assert.equal(result.verdict, 'REJECTED');
    assert.equal(result.speedup, undefined);
    assert.equal(result.adopted, false);
  }
});

test('five equivalent trials can nominate a candidate, never authorize adoption', () => {
  const result = compareTrials(comparison());
  assert.equal(result.verdict, 'CANDIDATE');
  assert.equal(result.speedup, 10);
  assert.equal(result.adopted, false);
});

test('the strict five-times gate includes build overhead', () => {
  const exact = comparison();
  for (const trial of exact.trials.filter(trial => trial.engine === 'chromium')) trial.durationMs = 50;
  assert.equal(compareTrials(exact).verdict, 'REJECTED');
  const preparation = comparison(); preparation.buildMs = 100;
  assert.equal(compareTrials(preparation).verdict, 'REJECTED');
});

test('too few samples, noisy samples, or a selected subset cannot establish adoption', () => {
  const short = comparison(); short.trials = short.trials.slice(0, 2);
  assert.equal(compareTrials(short).verdict, 'INCONCLUSIVE');
  const selected = comparison(); selected.completeSelection = false;
  assert.equal(compareTrials(selected).verdict, 'INCONCLUSIVE');
  const noisy = comparison(); noisy.trials[0].durationMs = 1000;
  assert.equal(compareTrials(noisy).verdict, 'INCONCLUSIVE');
});

test('invalid timing samples cannot become reportable measurements', () => {
  for (const values of [[], [0], [-1], [NaN], [Infinity]]) assert.throws(() => timingSummary(values));
  assert.deepEqual(timingSummary([9, 1, 3, 5]), { count: 4, medianMs: 4, minMs: 1, maxMs: 9 });
  const empty = comparison(); empty.caseIds = [];
  assert.equal(compareTrials(empty).verdict, 'INCONCLUSIVE');
  for (const buildMs of [-1, NaN, Infinity])
    assert.equal(compareTrials({ ...comparison(), buildMs }).verdict, 'INCONCLUSIVE');
});
