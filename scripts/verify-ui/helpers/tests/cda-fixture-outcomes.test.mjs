import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  fixtureUnavailableOutcome,
  fixtureUnavailableSkipReason,
} from '../cda-fixture-outcomes.mjs';

const reason = 'The completed bounded raw scan found no required fixture witness.';
const oracle = { completedBoundedScan: { selected: [] } };

function unavailableResult() {
  return {
    status: 'unverified',
    oracle,
    fixtureUnavailable: fixtureUnavailableOutcome(reason, oracle),
    failures: [], invalidations: [], errors: [], assertions: [], cases: [],
  };
}

test('an explicit fixture-unavailable result carries its existing oracle and may skip', () => {
  const result = unavailableResult();
  assert.equal(fixtureUnavailableSkipReason(result), reason);
  assert.equal(result.fixtureUnavailable.oracle, result.oracle);
});

test('query failures and non-passed statuses cannot turn into fixture skips', () => {
  const queryFailure = unavailableResult();
  queryFailure.status = 'failed';
  queryFailure.error = 'Arango query exited nonzero';
  assert.throws(() => fixtureUnavailableSkipReason(queryFailure), /cannot hide an error/);

  const queryFailureWithoutOutcome = { status: 'unverified', error: 'invalid raw query response' };
  assert.throws(() => fixtureUnavailableSkipReason(queryFailureWithoutOutcome), /cannot hide an error/);

  for (const status of ['failed', 'invalidated', 'untested', 'skipped', 'unknown']) {
    assert.throws(() => fixtureUnavailableSkipReason({ status }), /must fail the native case/);
  }
});

test('recorded failure, invalidation, or partial lifecycle cannot be skipped', () => {
  const failed = unavailableResult();
  failed.errors.push({ message: 'raw query transport failed' });
  assert.throws(() => fixtureUnavailableSkipReason(failed), /cannot hide errors/);

  const error = unavailableResult();
  error.error = 'raw query transport failed';
  assert.throws(() => fixtureUnavailableSkipReason(error), /cannot hide an error/);

  const invalidated = unavailableResult();
  invalidated.sourceFreeze = { invalidatesRun: true };
  assert.throws(() => fixtureUnavailableSkipReason(invalidated), /cannot hide source or API invalidation/);

  const partial = unavailableResult();
  partial.assertions.push({ name: 'native Apply', status: 'passed' });
  assert.throws(() => fixtureUnavailableSkipReason(partial), /must not hide a partial lifecycle/);
});

test('optional witness gaps stay attached to a completed lifecycle without a skip', () => {
  const result = {
    status: 'passed',
    relatedFieldLifecycle: 'passed',
    oracle: { missingWitnessCategories: [{ category: 'zero' }] },
  };
  assert.equal(fixtureUnavailableSkipReason(result), undefined);
  assert.equal(result.oracle.missingWitnessCategories[0].category, 'zero');
});

test('all standalone row fixture skips use the explicit outcome boundary', () => {
  const spec = readFileSync(new URL('../../specs/standalone-cda-rows.spec.mjs', import.meta.url), 'utf8');
  assert.doesNotMatch(spec, /if \(result\.status !== 'passed'\) test\.skip\(true,/);
  assert.equal([...spec.matchAll(/fixtureUnavailableSkipReason\(result\)/g)].length, 7);
  assert.match(spec, /missingComponentGroupSkipReason\(result\)/);
});
