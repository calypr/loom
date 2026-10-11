import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readViewerSharedFilterOracle } from '../viewer-shared-filter-oracle.mjs';
import {
  repositoryConfigExplorerId,
  repositoryConfigResponseMatches,
  runtimeBindingsMatch,
} from '../../workflows/viewer-shared-filter.mjs';

const fixtureDir = new URL('../../../../testdata/devloop-fixture/', import.meta.url).pathname;

test('shared filter oracle binds both outputs to the exact independent Patient identities', () => {
  const oracle = readViewerSharedFilterOracle({ fixtureDir });
  assert.equal(oracle.sha256.length, 64);
  assert.deepEqual(oracle.allIDs, ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual(oracle.secondIDs, ['dev-patient-002']);
  assert.equal(oracle.selectedID, 'dev-patient-001');
});

test('repository config response requires the activated default Explorer receipt identities', () => {
  const expected = {
    project: 'loom_dev_verify_test/project-a',
    generation: 'cda-fhir-v1',
  };
  const body = {
    ...expected,
    explorerId: 'default',
    receiptId: 'receipt-1',
    revisionId: 'revision-1',
    executionId: 'execution-1',
    activated: true,
  };

  assert.equal(repositoryConfigExplorerId, 'default');
  assert.equal(repositoryConfigResponseMatches({ ok: true, body, ...expected }), true);
  assert.equal(repositoryConfigResponseMatches({
    ok: true, body: { ...body, explorerId: 'loom-dev-bootstrap' }, ...expected,
  }), false);
  assert.equal(repositoryConfigResponseMatches({ ok: true, body: { ...body, activated: false }, ...expected }), false);
  assert.equal(repositoryConfigResponseMatches({ ok: true, body: { ...body, receiptId: '' }, ...expected }), false);
  assert.equal(repositoryConfigResponseMatches({ ok: true, body: { ...body, revisionId: undefined }, ...expected }), false);
  assert.equal(repositoryConfigResponseMatches({ ok: true, body: { ...body, executionId: undefined }, ...expected }), false);
  assert.equal(repositoryConfigResponseMatches({ ok: true, body: { ...body, project: 'wrong-project' }, ...expected }), false);
  assert.equal(repositoryConfigResponseMatches({ ok: false, body, ...expected }), false);
});

test('runtime shared bindings compare exact ordered pairs without depending on JSON object key order', () => {
  const expected = [
    { outputId: 'patients_all', column: 'patient_id' },
    { outputId: 'patient_002_only', column: 'patient_id' },
  ];
  const actualFromRuntime = [
    { column: 'patient_id', outputId: 'patients_all' },
    { column: 'patient_id', outputId: 'patient_002_only' },
  ];

  assert.equal(runtimeBindingsMatch(actualFromRuntime, expected), true);
  assert.equal(runtimeBindingsMatch([...actualFromRuntime].reverse(), expected), false);
  assert.equal(runtimeBindingsMatch([actualFromRuntime[0], actualFromRuntime[0]], expected), false);
  assert.equal(runtimeBindingsMatch([...actualFromRuntime, actualFromRuntime[0]], expected), false);
  assert.equal(runtimeBindingsMatch([{ ...actualFromRuntime[0], column: 'gender' }, actualFromRuntime[1]], expected), false);
  assert.equal(runtimeBindingsMatch(null, expected), false);
});
