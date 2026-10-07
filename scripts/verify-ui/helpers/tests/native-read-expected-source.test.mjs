import assert from 'node:assert/strict';
import test from 'node:test';
import { nativeReadRequestMatchesExpectedScope } from '../native-request-ownership.mjs';

const project = 'loom_dev_source_scope_test';
const explorer = 'source-scope-explorer';
const origin = 'http://127.0.0.1:30008';
const path = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/construction-choices`;
const commonExpected = () => ({
  project,
  explorer,
  origin,
  outputId: 'output-1',
  snapshotToken: 'snapshot-1',
});

const requestEntry = (source) => ({
  origin,
  path,
  method: 'POST',
  scopeProject: project,
  scopeExplorer: explorer,
  request: {
    outputId: 'output-1',
    snapshotToken: 'snapshot-1',
    source,
  },
});

test('FIELD choices match an independently captured candidate identity', () => {
  const expected = {
    ...commonExpected(),
    source: { kind: 'FIELD', candidateId: 'candidate-field-1' },
  };
  assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry({
    kind: 'FIELD', candidateId: 'candidate-field-1',
  }), expected), true);

  assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry({
    kind: 'FIELD', candidateId: 'candidate-field-2',
  }), expected), false, 'a different actual candidate must not match the captured choice');
  assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry({
    kind: 'SEMANTIC', contextToken: 'context-1', buildId: 'build-1', conceptId: 'concept-1', bindingId: 'binding-1',
  }), expected), false, 'a semantic source is not the captured FIELD source');
  assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry({
    kind: 'FIELD', candidateId: 'candidate-field-1',
  }), { ...commonExpected() }), false, 'missing expected source identity must fail');
  assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry({
    kind: 'FIELD', candidateId: 'candidate-field-1',
  }), { ...expected, source: { kind: 'FIELD', candidateId: 'candidate-field-other' } }), false,
  'wrong independently captured candidate identity must fail');
});

test('SEMANTIC choices match every member of the independently captured source tuple', () => {
  const actualSource = {
    kind: 'SEMANTIC',
    contextToken: 'context-1',
    buildId: 'build-1',
    conceptId: 'concept-1',
    bindingId: 'binding-1',
  };
  const expected = {
    ...commonExpected(),
    source: {
      kind: 'SEMANTIC',
      contextToken: 'context-1',
      buildId: 'build-1',
      conceptId: 'concept-1',
      bindingId: 'binding-1',
    },
  };
  assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry(actualSource), expected), true);
  assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry(actualSource), { ...commonExpected() }), false,
    'missing expected source tuple must fail');

  for (const key of ['kind', 'contextToken', 'buildId', 'conceptId', 'bindingId']) {
    const wrongExpected = {
      ...expected,
      source: { ...expected.source, [key]: `wrong-${key}` },
    };
    assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry(actualSource), wrongExpected), false,
      `wrong expected ${key} must fail`);
  }

  for (const key of ['contextToken', 'buildId', 'conceptId', 'bindingId']) {
    const wrongActual = { ...actualSource, [key]: `wrong-${key}` };
    assert.equal(nativeReadRequestMatchesExpectedScope(requestEntry(wrongActual), expected), false,
      `wrong actual ${key} must fail`);
  }
});
