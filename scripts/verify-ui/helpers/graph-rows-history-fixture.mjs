import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { writeFileSync } from 'node:fs';
import { assertGraphRowsNativeIdentity } from './graph-rows-native-identity.mjs';
import { assertHistoryStressPlan, historyStressStepIDs } from '../workflows/graph-rows-history-stress.mjs';

const recipeSHA256 = '998c6aead0d3f432f3f0cf4d3fb515cbbacda9af53c0c090f457c3931b709dad';
const expectedIDs = ['obs-many-A', 'obs-many-B', 'obs-one'];
export const historyArtifactSHA256 = raw => createHash('sha256').update(raw).digest('hex');

export function historyEvidenceTarget(target) {
  const fields = ['kind', 'composeProject', 'composeFile', 'sourceRoot', 'fixtureDir', 'fixtureLoadTimeout', 'fixtureProject', 'fixtureGeneration', 'host', 'apiPort', 'uiPort', 'apiUrl', 'uiUrl', 'artifacts'];
  return Object.fromEntries(fields.filter(field => target[field] !== undefined).map(field => [field, target[field]]));
}

export function writeHistoryPreparationReport(path, report) {
  writeFileSync(path, JSON.stringify(report, null, 2), { flag: 'wx', mode: 0o600 });
}

const assertRecordedIdentity = identity => {
  assert(identity?.files && Object.keys(identity.files).length > 0, 'Original preparation requires its source manifest');
  assert.equal(identity.sourceFingerprint, historyArtifactSHA256(JSON.stringify(identity.files)), 'Original source fingerprint does not match its manifest');
  assert.equal(identity.api?.checkExit, 0, 'Original API identity was not current');
  assert.match(identity.api.liveSource, /^[a-f0-9]{64}$/);
  assert.equal(identity.api.expectedSource, identity.api.liveSource);
  assert.equal(identity.api.recordedSource, identity.api.liveSource);
  assert.equal(identity.api.recordedBinary, identity.api.runningBinary);
  assert.match(identity.api.runningBinary, /^[a-f0-9]{64}$/);
  assert.equal(identity.recipeReferenceSHA256, recipeSHA256);
  assert.equal(identity.liveRecipeReferenceSHA256, recipeSHA256);
  assert.equal(identity.liveRuntimeConfigurationSHA256, identity.runtimeConfigurationSHA256);
};

export function loadHistoryFixtureArtifact(raw, expectedSHA256, session) {
  assert.match(expectedSHA256 ?? '', /^[a-f0-9]{64}$/, 'Supply the original preparation SHA-256 explicitly');
  assert.equal(historyArtifactSHA256(raw), expectedSHA256, 'Original preparation artifact changed');
  const original = JSON.parse(raw);
  assert.equal(original.phase, 'production-fixture-preparation', 'Reuse only an original production preparation record');
  assert.equal(original.status, 'passed', 'Original production preparation did not pass');
  assert.equal(original.seed?.fresh, true);
  assert.equal(original.seed?.reused, false);
  assert.equal(original.seed?.bootstrapExplorerId, 'loom-dev-bootstrap');
  const source = original.target;
  for (const key of ['composeProject', 'composeFile', 'sourceRoot', 'apiUrl', 'uiUrl', 'fixtureDir', 'fixtureGeneration']) {
    assert.equal(source?.[key], session[key], `Original fixture belongs to another owned target: ${key}`);
  }
  assert.match(source.fixtureProject, /^loom_dev_verify_gr-history-[a-z0-9]+$/, 'Reuse requires this workflow\'s isolated QA project');
  assertRecordedIdentity(original.before);
  assertRecordedIdentity(original.after);
  assertGraphRowsNativeIdentity(original.before, original.after);
  assert(Number.isSafeInteger(original.savedIdentity?.version) && original.savedIdentity.version > 0);
  assert.match(original.savedIdentity?.digest ?? '', /^sha256:[a-f0-9]{64}$/);
  const sourceDocument = original.sourceDocument;
  assert.equal(sourceDocument?.rootResourceType, 'Observation');
  assert.equal(sourceDocument?.importedRecipe, undefined);
  assert.equal(sourceDocument?.output?.id, original.outputId);
  assert.equal(sourceDocument?.rows?.kind, 'RECORDS');
  assert.equal(sourceDocument?.route?.occurrenceId, 'base');
  assert.equal(sourceDocument?.route?.resourceType, 'Observation');
  assert.equal(sourceDocument.columns?.length, 2);
  assert.deepEqual(sourceDocument.columns.map(column => column.source?.field?.path).sort(), ['id', 'valueInteger']);
  for (const column of sourceDocument.columns) {
    assert.equal(column.occurrenceId, 'base');
    assert.equal(column.source.kind, 'field');
    assert.equal(column.source.field.projectionMode, 'VALUE');
  }
  const idColumns = sourceDocument.columns.filter(column => column.source.field.path === 'id');
  assert.equal(idColumns.length, 1);
  assert.equal(original.idColumn, idColumns[0].column);
  const steps = original.historyStressFixture?.preparation;
  assert.equal(steps?.length, 100, 'Original preparation did not prove all100 stages');
  assert.deepEqual(steps.map(step => step.stepId), historyStressStepIDs);
  let version = 0;
  for (const step of steps) {
    assert.equal(step.status, 'passed');
    assert(step.draftVersion > version, 'Original fixture CAS revisions must increase');
    version = step.draftVersion;
    assert.deepEqual(step.values.map(value => value.id).sort(), expectedIDs);
    for (const value of step.values) assert.equal(value.value, 3);
  }
  assert.equal(version, original.savedIdentity.version);
  // Use the current explicitly owned environment, never arbitrary endpoints or
  // command configuration from an input report. Secrets stay in memory only.
  const target = historyEvidenceTarget({ ...session, fixtureProject: source.fixtureProject });
  return { original, originalSHA256: expectedSHA256, target, seed: { ...original.seed } };
}

export function assertHistoryRuntimeContinuity(original, current) {
  assertRecordedIdentity(current);
  for (const key of ['recipeReferenceSHA256', 'runtimeConfigurationSHA256', 'runtimeComposeConfigurationSHA256']) {
    assert.equal(current[key], original.after[key], `Loaded fixture runtime configuration changed: ${key}`);
  }
  assert.deepEqual(current.liveBuildLimits, original.after.liveBuildLimits);
  const fixtureFiles = files => Object.fromEntries(Object.entries(files).filter(([path]) => path.startsWith('testdata/graph-rows/')));
  assert.deepEqual(fixtureFiles(current.files), fixtureFiles(original.after.files), 'Loaded dataset fixture files changed');
}

export function assertCurrentHistoryFixture(original, state) {
  assert.equal(state.kind, 'ExplorerBuilderState');
  assert.equal(state.lifecycleState, 'READY');
  assert.equal(state.catalog?.generation, original.target.fixtureGeneration, 'Loaded fixture generation changed');
  assert.equal(state.draftVersion, original.savedIdentity.version, 'Loaded fixture draft version changed');
  assert.equal(state.draftDigest, original.savedIdentity.digest, 'Loaded fixture draft digest changed');
  assert.equal(state.workspace?.documents?.length, 1);
  const document = state.workspace.documents[0];
  assert.equal(document.output.id, original.outputId);
  assertHistoryStressPlan(document);
  const sourceOnly = value => { const { construction, ...source } = value; return source; };
  assert.deepEqual(sourceOnly(document), sourceOnly(original.sourceDocument), 'Loaded fixture source or field bindings changed');
  for (const [index, step] of document.construction.steps.entries()) {
    const expected = [...original.sourceDocument.columns.map(column => ({ id: column.columnId, name: column.column, type: column.logicalType })),
      ...historyStressStepIDs.slice(0, index + 1).map((_, columnIndex) => ({ id: `stress_column_${String(columnIndex).padStart(3, '0')}`, name: `stress_value_${String(columnIndex).padStart(3, '0')}`, type: 'integer' }))];
    assert.deepEqual(step.outputs.map(({ id, name, type }) => ({ id, name, type })), expected, 'Loaded fixture stage schema or execution order changed');
  }
  return document;
}
