import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const selfTestSource = readFileSync(new URL('../../../self-test-builder-traces.mjs', import.meta.url), 'utf8');
const start = selfTestSource.indexOf('const policyExpectedFailureMatches =');
const end = selfTestSource.indexOf('const runPolicyNativeJourneySelfTest =', start);

test('Policy expected-failure matching uses structured TLC outcomes and fails closed', () => {
  assert.notEqual(start, -1, 'Policy structured-result matcher exists in the self-test');
  assert.notEqual(end, -1, 'Policy structured-result test has a bounded source region');
  const matcherSource = selfTestSource.slice(start, end);
  const passed = vm.runInNewContext(matcherSource + '\npolicyStructuredFailureBoundaryTest();');
  assert.equal(passed, true);
});
