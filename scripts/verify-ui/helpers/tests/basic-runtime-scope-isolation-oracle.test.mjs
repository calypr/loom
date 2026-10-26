import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import test from 'node:test';
import {
  assertVisibleRowsMatchRuntimeScope,
  inspectRuntimeEnvelopeIdentity,
  readBasicRuntimeScopeIsolationOracle,
} from '../basic-runtime-scope-isolation-oracle.mjs';

const sourceRoot = resolve('.');
const fixtureRoot = resolve('testdata/scope-001-runtime-scope-isolation');

test('runtime envelope uses canonical project identity and rejects legacy or foreign identities', () => {
  const project = 'loom_dev_verify_scope-run123';
  const explorer = 'scope-run123-runtime-isolation';
  assert.deepEqual(inspectRuntimeEnvelopeIdentity({
    project: 'loom_dev_verify_scope/run123', explorerId: explorer,
  }, { project, explorer }), {
    expectedProject: 'loom_dev_verify_scope/run123',
    projectMatches: true,
    explorerMatches: true,
    matches: true,
  });
  assert.equal(inspectRuntimeEnvelopeIdentity({
    project, explorerId: explorer,
  }, { project, explorer }).matches, false, 'the legacy fixture project string is not the runtime envelope identity');
  assert.equal(inspectRuntimeEnvelopeIdentity({
    project: 'loom_dev_verify_other/run123', explorerId: explorer,
  }, { project, explorer }).matches, false, 'a different canonical project must be rejected');
  assert.equal(inspectRuntimeEnvelopeIdentity({
    project: 'loom_dev_verify_scope/run123', explorerId: 'another-explorer',
  }, { project, explorer }).matches, false, 'a different Explorer must be rejected');
});

test('literal Basic scope witnesses distinguish project and generation identities', async () => {
  const oracle = await readBasicRuntimeScopeIsolationOracle({
    primaryFixtureDir: resolve(sourceRoot, 'testdata/devloop-fixture'),
    activeGenerationFixtureDir: resolve(fixtureRoot, 'active-generation'),
    foreignProjectFixtureDir: resolve(fixtureRoot, 'foreign-project'),
    primaryProject: 'loom_dev_scope_primary',
    primaryGeneration: 'scope-001-primary',
    activeGeneration: 'scope-001-active',
    foreignProject: 'loom_dev_scope_foreign',
  });
  assert.deepEqual([...oracle.primary.ids].sort(), ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual(oracle.selected.ids, ['scope-active-patient-001']);
  assert.deepEqual(oracle.foreign.ids, ['scope-foreign-patient-001']);
  assert.equal(oracle.primary.project, oracle.selected.project);
  assert.notEqual(oracle.primary.generation, oracle.selected.generation);
  assert.notEqual(oracle.foreign.project, oracle.selected.project);
  assert.equal(oracle.foreign.generation, oracle.selected.generation);
});

test('runtime rows accept only selected project-generation identities', async () => {
  const oracle = await readBasicRuntimeScopeIsolationOracle({
    primaryFixtureDir: resolve(sourceRoot, 'testdata/devloop-fixture'),
    activeGenerationFixtureDir: resolve(fixtureRoot, 'active-generation'),
    foreignProjectFixtureDir: resolve(fixtureRoot, 'foreign-project'),
    primaryProject: 'loom_dev_scope_primary',
    primaryGeneration: 'scope-001-primary',
    activeGeneration: 'scope-001-active',
    foreignProject: 'loom_dev_scope_foreign',
  });
  const selected = oracle.selected.ids;
  const excluded = [...oracle.primary.ids, ...oracle.foreign.ids];
  assert.deepEqual(assertVisibleRowsMatchRuntimeScope({
    rows: ['scope-active-patient-001 Active'], selectedIDs: selected, excludedIDs: excluded,
  }), selected);
  for (const leakedID of excluded) {
    assert.throws(() => assertVisibleRowsMatchRuntimeScope({
      rows: [leakedID], selectedIDs: selected, excludedIDs: excluded,
    }), /exactly one scoped Patient|leaked a foreign/);
  }
  assert.throws(() => assertVisibleRowsMatchRuntimeScope({
    rows: ['scope-active-patient-001 Active', 'dev-patient-001 Ada'], selectedIDs: selected, excludedIDs: excluded,
  }), /exactly the selected scope row count/);
});
