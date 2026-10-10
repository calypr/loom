import assert from 'node:assert/strict';
import test from 'node:test';
import {
  fixtureUnavailableOutcome,
  fixtureUnavailableSkipReason,
} from '../cda-fixture-outcomes.mjs';

const reason = 'No zero-Observation Patient witness was found by the completed bounded query.';
const oracle = {
  searchBounds: { project: 'fixture-project', generation: 'fixture-generation', limit: 2000 },
  missingWitnessCategories: [{ category: 'zero-observation', limit: 2000 }],
};

test('the zero-observation producer outcome retains its exact existing oracle', () => {
  const result = {
    status: 'unverified', oracle,
    fixtureUnavailable: fixtureUnavailableOutcome(reason, oracle),
    errors: [], failures: [], invalidations: [], cases: [], assertions: [],
  };
  assert.equal(fixtureUnavailableSkipReason(result), reason);
  assert.equal(result.fixtureUnavailable.oracle, oracle);
});

test('a zero-observation outcome cannot conceal failed or started native work', () => {
  const oracleFailure = {
    status: 'failed', oracle,
    error: 'Arango returned malformed JSON',
  };
  assert.throws(() => fixtureUnavailableSkipReason(oracleFailure), /cannot hide an error/);

  const startedLifecycle = {
    status: 'unverified', oracle,
    fixtureUnavailable: fixtureUnavailableOutcome(reason, oracle),
    errors: [], failures: [], invalidations: [], cases: [{ name: 'ONE preview' }], assertions: [],
  };
  assert.throws(() => fixtureUnavailableSkipReason(startedLifecycle), /must not hide a partial lifecycle/);
});
