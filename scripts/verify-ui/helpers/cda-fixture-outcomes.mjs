import assert from 'node:assert/strict';

export function fixtureUnavailableOutcome(reason, oracle) {
  assert.equal(typeof reason, 'string');
  assert(reason.length > 0, 'Fixture unavailability needs a reason');
  assert(oracle && typeof oracle === 'object', 'Fixture unavailability must carry the existing raw oracle');
  return { kind: 'fixture-unavailable', reason, oracle };
}

export function fixtureUnavailableSkipReason(result) {
  assert(result && typeof result === 'object', 'CDA row workflow result is required');
  assert.equal(result.error, undefined, 'Fixture unavailability cannot hide an error');
  if (result.status === 'passed') return undefined;
  assert.equal(result.status, 'unverified', `CDA row workflow ${String(result.status)} must fail the native case`);

  for (const key of ['failures', 'invalidations', 'errors']) {
    if (result[key] !== undefined) assert.deepEqual(result[key], [], `Fixture unavailability cannot hide ${key}`);
  }
  assert.notEqual(result.productFailure, true, 'Fixture unavailability cannot hide a product failure');
  for (const freeze of [result.apiBuildFreeze, result.sourceFreeze, result.sourceFingerprint]) {
    assert.notEqual(freeze?.invalidatesRun, true, 'Fixture unavailability cannot hide source or API invalidation');
  }
  assert.equal(result.relatedFieldLifecycle, undefined, 'Fixture unavailability must be established before native lifecycle work');
  assert.equal(result.cases?.length ?? 0, 0, 'Fixture unavailability must not hide a partial lifecycle');
  assert.equal(result.assertions?.length ?? 0, 0, 'Fixture unavailability must not hide a partial lifecycle');

  const outcome = result.fixtureUnavailable;
  assert.equal(outcome?.kind, 'fixture-unavailable', 'Only the producer-marked fixture outcome may skip');
  assert.equal(typeof outcome.reason, 'string');
  assert(outcome.reason.length > 0, 'Fixture unavailability needs a reason');
  assert.equal(outcome.oracle, result.oracle, 'Fixture outcome must carry the exact raw-oracle evidence');
  return outcome.reason;
}
