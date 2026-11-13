import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, readFileSync, writeFileSync, symlinkSync, linkSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { loadHistoryFixtureArtifact, historyArtifactSHA256, assertCurrentHistoryFixture, assertHistoryRuntimeContinuity, writeHistoryPreparationReport, historyEvidenceTarget } from '../graph-rows-history-fixture.mjs';
import { nextStressStep, historyStressStepIDs } from '../../workflows/graph-rows-history-stress.mjs';
const recipe = '998c6aead0d3f432f3f0cf4d3fb515cbbacda9af53c0c090f457c3931b709dad';
const session = { composeProject: 'loom-dev-owned', composeFile: '/owned/compose.dev.yaml', sourceRoot: '/owned', apiUrl: 'http://127.0.0.1:8180', uiUrl: 'http://127.0.0.1:3180', fixtureDir: '/owned/testdata/graph-rows', fixtureGeneration: 'fixture-v2', populationMappingCursorSecret: 'never-copy-this' };
const identity = () => {
  const files = { 'internal/source.go': 'e'.repeat(64), 'testdata/graph-rows/Observation.ndjson': 'f'.repeat(64) };
  return { files, sourceFingerprint: historyArtifactSHA256(JSON.stringify(files)), api: { checkExit: 0, expectedSource: 'a'.repeat(64), liveSource: 'a'.repeat(64), recordedSource: 'a'.repeat(64), runningBinary: 'b'.repeat(64), recordedBinary: 'b'.repeat(64) }, recipeReferenceSHA256: recipe, liveRecipeReferenceSHA256: recipe, runtimeConfigurationSHA256: 'c'.repeat(64), liveRuntimeConfigurationSHA256: 'c'.repeat(64), runtimeComposeConfigurationSHA256: 'd'.repeat(64), liveBuildLimits: ['2', '-p=1'] };
};
const artifact = () => ({ phase: 'production-fixture-preparation', status: 'passed', target: { ...session, fixtureProject: 'loom_dev_verify_gr-history-example' }, seed: { fresh: true, reused: false, bootstrapExplorerId: 'loom-dev-bootstrap' }, before: identity(), after: identity(), savedIdentity: { version: 103, digest: `sha256:${'a'.repeat(64)}` }, outputId: 'output', idColumn: 'observation_id', sourceDocument: { kind: 'ExplorerBuilderDocument', rootResourceType: 'Observation', output: { id: 'output', title: 'Fixture' }, route: { occurrenceId: 'base', resourceType: 'Observation' }, rows: { kind: 'RECORDS', records: {} }, columns: [ { columnId: 'source-id', column: 'observation_id', label: 'Observation ID', logicalType: 'string', occurrenceId: 'base', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } }, { columnId: 'score', column: 'score', label: 'Score', logicalType: 'integer', occurrenceId: 'base', source: { kind: 'field', field: { path: 'valueInteger', projectionMode: 'VALUE' } } } ] }, historyStressFixture: { preparation: historyStressStepIDs.map((stepId, index) => ({ stepId, draftVersion: index + 4, status: 'passed', values: ['obs-one', 'obs-many-A', 'obs-many-B'].map(id => ({ id, value: 3 })) })) } });
const load = value => { const raw = Buffer.from(JSON.stringify(value)); return loadHistoryFixtureArtifact(raw, historyArtifactSHA256(raw), session); };
const state = original => {
  const document = structuredClone(original.sourceDocument); document.construction = { version: 1, steps: [] };
  let columns = document.columns.map(column => ({ id: column.columnId, name: column.column, label: column.label, type: column.logicalType }));
  for (let index = 0; index < 100; index += 1) { const step = nextStressStep(index, columns); document.construction.steps.push(step); columns = step.outputs; }
  return { kind: 'ExplorerBuilderState', lifecycleState: 'READY', catalog: { generation: 'fixture-v2' }, draftVersion: original.savedIdentity.version, draftDigest: original.savedIdentity.digest, workspace: { documents: [document] } };
};
test('loaded fixture keeps original SHA/source provenance and uses only the current owned endpoint configuration', () => {
  const original = artifact(); const loaded = load(original);
  assert.equal(loaded.target.fixtureProject, original.target.fixtureProject);
  assert.equal(loaded.target.populationMappingCursorSecret, undefined);
  const safe = historyEvidenceTarget({ ...session, password: 'do-not-export', bearerToken: 'do-not-export', arbitraryCredential: 'do-not-export' });
  for (const key of ['populationMappingCursorSecret', 'password', 'bearerToken', 'arbitraryCredential']) assert.equal(safe[key], undefined);
  assert.equal(safe.apiUrl, session.apiUrl);
  assert.equal(loaded.target.apiUrl, session.apiUrl);
  assertCurrentHistoryFixture(original, state(original));
  const changedCode = identity(); changedCode.files['internal/new.go'] = '1'.repeat(64); changedCode.sourceFingerprint = historyArtifactSHA256(JSON.stringify(changedCode.files));
  assertHistoryRuntimeContinuity(original, changedCode);
});
test('changed artifact, malformed provenance, failed preparation and unauthorized targets reject before any API action', () => {
  const original = artifact(); const raw = JSON.stringify(original);
  assert.throws(() => loadHistoryFixtureArtifact(raw, '0'.repeat(64), session), /artifact changed/);
  assert.throws(() => loadHistoryFixtureArtifact('{', historyArtifactSHA256('{'), session));
  for (const mutate of [ value => { value.status = 'failed'; }, value => { value.seed.fresh = false; }, value => { value.target.fixtureProject = 'NCPI_ACCEPTANCE'; }, value => { value.target.apiUrl = 'https://production.invalid'; }, value => { value.target.fixtureGeneration = 'other'; }, value => { value.before.sourceFingerprint = 'forged'; }, value => { value.after.api.recordedSource = 'stale'; }, value => { value.after.api.recordedBinary = 'stale'; }, value => { value.before.api.checkExit = 1; }, value => { value.after.recipeReferenceSHA256 = 'changed'; }, value => { value.historyStressFixture.preparation.pop(); }, value => { value.historyStressFixture.preparation[50].values[0].value = 99; }, value => { value.historyStressFixture.preparation[50].draftVersion = 4; }, value => { value.sourceDocument.columns[0].source.field.path = 'other'; } ]) {
    const changed = artifact(); mutate(changed); assert.throws(() => load(changed));
  }
});
test('stale drafts, generations, source bindings, operation inputs, order and every stage schema reject', () => {
  const original = artifact();
  for (const mutate of [ value => { value.draftVersion += 1; }, value => { value.draftDigest = 'stale'; }, value => { value.catalog.generation = 'stale'; }, value => { value.workspace.documents[0].columns[0].columnId = 'other'; }, value => { value.workspace.documents[0].construction.steps[50].inputs[0].stepId = historyStressStepIDs[0]; }, value => { value.workspace.documents[0].construction.steps[99].outputs.pop(); }, value => { value.workspace.documents[0].construction.steps[50].outputs[10].type = 'string'; } ]) {
    const changed = state(original); mutate(changed); assert.throws(() => assertCurrentHistoryFixture(original, changed));
  }
});
test('dataset hashes, runtime configuration and current API/build drift cannot be hidden by fixture reuse', () => {
  const original = artifact();
  for (const mutate of [ value => { value.api.expectedSource = 'stale'; }, value => { value.api.recordedBinary = 'stale'; }, value => { value.runtimeComposeConfigurationSHA256 = 'other'; }, value => { value.liveBuildLimits = ['4', '-p=1']; }, value => { value.files['testdata/graph-rows/Observation.ndjson'] = 'other'; value.sourceFingerprint = historyArtifactSHA256(JSON.stringify(value.files)); } ]) {
    const changed = identity(); mutate(changed); assert.throws(() => assertHistoryRuntimeContinuity(original, changed));
  }
});

test('report output is exclusive: existing, symlink-directory and hardlink aliases preserve original bytes', () => {
  const directory = mkdtempSync(join(tmpdir(), 'graph-history-output-'));
  try {
    const original = join(directory, 'history-preparation.json');
    writeFileSync(original, 'immutable-original');
    const aliasDirectory = join(directory, 'alias');
    symlinkSync(directory, aliasDirectory, 'dir');
    const hardlink = join(directory, 'hardlink.json');
    linkSync(original, hardlink);
    const symlink = join(directory, 'symlink.json');
    symlinkSync(original, symlink);
    for (const output of [original, join(aliasDirectory, 'history-preparation.json'), hardlink, symlink]) {
      assert.throws(() => writeHistoryPreparationReport(output, { status: 'passed' }), error => error.code === 'EEXIST');
      assert.equal(readFileSync(original, 'utf8'), 'immutable-original');
    }
    const fresh = join(directory, 'fresh.json');
    writeHistoryPreparationReport(fresh, { status: 'passed' });
    assert.deepEqual(JSON.parse(readFileSync(fresh)), { status: 'passed' });
    assert.throws(() => writeHistoryPreparationReport(fresh, { status: 'failed' }), error => error.code === 'EEXIST');
    assert.deepEqual(JSON.parse(readFileSync(fresh)), { status: 'passed' });
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test('registered runner rejects malformed or non-history reuse flags before setup', () => {
  const runner = fileURLToPath(new URL('../../../run-graph-rows-native.mjs', import.meta.url));
  for (const [args, message] of [
    [['--case', 'history-stress', '--reuse-history-fixture'], /requires a path/],
    [['--case', 'history-stress', '--history-fixture-sha256'], /requires a hash/],
    [['--case', 'history-stress', '--reuse-history-fixture', 'fixture.json'], /requires both/],
    [['--case', 'ordinary', '--reuse-history-fixture', 'fixture.json', '--history-fixture-sha256', 'a'.repeat(64)], /only for history-stress/],
  ]) {
    const result = spawnSync(process.execPath, [runner, ...args, '--checks-only'], { encoding: 'utf8', timeout: 10000 });
    assert.equal(result.error, undefined);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, message);
  }
});
