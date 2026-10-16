import test from 'node:test';
import assert from 'node:assert/strict';
import { createRelatedFieldAfterUnpivotChecks } from '../../workflows/verify-cda-related-field-after-unpivot-browser.mjs';

test('after-Unpivot lifecycle named checks record the raw case and use the domain report error list', () => {
  const calls = [];
  const cda = { check: (...args) => { calls.push(args); return args[2]; } };
  const genderAll = createRelatedFieldAfterUnpivotChecks(cda, 'gender-all');

  assert.equal(genderAll.recordLifecycleCheck('raw source matches', true, { patientID: 'patient-1' }), true);
  assert.equal(genderAll.recordGenderAllLifecycleCheck('populated gender witness', true, { gender: 'female' }), true);
  assert.equal(genderAll.recordNoUnexpectedNativeErrorsCheck({ errors: [] }), true);
  assert.deepEqual(calls, [
    ['correctness', 'raw source matches', true, { patientID: 'patient-1' }],
    ['correctness', 'populated gender witness', true, { gender: 'female' }],
    ['correctness', 'No unexpected native HTTP or network errors occurred', true, { domainErrors: [] }],
  ]);

  const nullable = createRelatedFieldAfterUnpivotChecks(cda, 'gender-null-all');
  assert.equal(nullable.recordGenderAllLifecycleCheck('populated gender witness', false), undefined);
  assert.equal(nullable.recordNoUnexpectedNativeErrorsCheck({ errors: [{ kind: 'browser-network' }] }), false);
  assert.deepEqual(calls.at(-1), [
    'correctness', 'No unexpected native HTTP or network errors occurred', false,
    { domainErrors: [{ kind: 'browser-network' }] },
  ]);
});
