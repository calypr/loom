import test from 'node:test';
import assert from 'node:assert/strict';
import { classifyNullableSourceScalar, expectedNullableRelatedAll } from '../nullable-related-all.mjs';

test('raw source classification keeps absent gender distinct from explicit null', () => {
  const missing = classifyNullableSourceScalar({ ownProperty: false, isNull: true, value: null });
  const explicitNull = classifyNullableSourceScalar({ ownProperty: true, isNull: true, value: null });

  assert.deepEqual(missing, { state: 'missing', ownProperty: false, isNull: true });
  assert.deepEqual(explicitNull, { state: 'explicit-null', ownProperty: true, isNull: true });
  assert.notEqual(missing.state, explicitNull.state);
});

test('one linked owner projects a null ALL member while zero owners use an empty array', () => {
  assert.deepEqual(expectedNullableRelatedAll({ relatedCount: 1, sourceState: 'missing' }), [null]);
  assert.deepEqual(expectedNullableRelatedAll({ relatedCount: 1, sourceState: 'explicit-null' }), [null]);
  assert.deepEqual(expectedNullableRelatedAll({ relatedCount: 0, sourceState: 'missing' }), []);
});

test('null-witness expectation rejects populated, inconsistent, and under-specified populations', () => {
  assert.deepEqual(classifyNullableSourceScalar({ ownProperty: true, isNull: false, value: 'female' }), { state: 'populated', ownProperty: true, isNull: false });
  assert.throws(() => expectedNullableRelatedAll({ relatedCount: 1, sourceState: 'populated' }), /only a missing or explicit-null/);
  assert.throws(() => expectedNullableRelatedAll({ relatedCount: 2, sourceState: 'missing' }), /exactly one linked Patient/);
  assert.throws(() => classifyNullableSourceScalar({ ownProperty: false, isNull: false, value: 'female' }), /inconsistent/);
  assert.throws(() => classifyNullableSourceScalar({ ownProperty: true, isNull: true, value: undefined }), /JSON null/);
});
