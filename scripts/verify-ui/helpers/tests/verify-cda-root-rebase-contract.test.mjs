import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { scenarioCaseFor } from '../../registry.mjs';
import {
  buildRootRebaseOracleMetadata,
  rootRebaseOracleBounds,
  selectRootRebaseWitness,
} from '../../workflows/verify-cda-root-rebase.mjs';

const spec = await readFile(new URL('../../specs/standalone-cda-other.spec.mjs', import.meta.url), 'utf8');

test('CDA root rebase is registered to its explicit-query native case and focused gate', () => {
  const contract = scenarioCaseFor('cda-root-rebase', 'preserve-patient-values-through-observation-and-restore');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs');
  assert.equal(contract.playwrightGrep, 'rebase Patient rows through Observation and restore the original route and values$');
  assert.deepEqual(contract.expectedIdentity, { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });
  assert.equal(contract.requiredChecks.length, 8);
  assert.equal(contract.focusedChecks.length, 1);
  assert.deepEqual(contract.focusedChecks[0].command, [
    'node-test',
    'scripts/verify-ui/helpers/tests/verify-cda-root-rebase-contract.test.mjs',
  ]);
  const describe = spec.slice(spec.indexOf("test.describe('CDA root rebase lifecycle'"), spec.indexOf("test.describe('CDA legacy collection'"));
  assert.match(describe, /cdaScenarioID:\s*'cda-root-rebase'/);
  assert.match(describe, /cdaCaseName:\s*'preserve-patient-values-through-observation-and-restore'/);
  assert.match(describe, /cdaUiRouting:\s*'explicit-query'/);
});

test('root-rebase exact rereads reject partial samples and accept only one scoped 2–25-row Patient', () => {
  const expectedScope = { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' };
  const exactObservations = count => Array.from({ length: count }, (_, index) => ({
    id: `observation-${index}`,
    ...expectedScope,
  }));
  const exactPatient = (patientId, count, overrides = {}) => ({
    patientId,
    patientKey: `Patient/key-${patientId}`,
    ...expectedScope,
    observations: exactObservations(count),
    ...overrides,
  });
  const candidates = [
    { patientId: 'sentinel', sampledObservationCount: 2 },
    { patientId: 'duplicate', sampledObservationCount: 3 },
    { patientId: 'empty', sampledObservationCount: 2 },
    { patientId: 'wrong-patient-scope', sampledObservationCount: 2 },
    { patientId: 'wrong-observation-scope', sampledObservationCount: 2 },
    { patientId: 'valid', sampledObservationCount: 2 },
    { patientId: 'not-reached', sampledObservationCount: 4 },
  ];
  const wrongObservationScope = exactPatient('wrong-observation-scope', 2);
  wrongObservationScope.observations[0].generation = 'other-generation';
  const exactRows = new Map([
    ['sentinel', [exactPatient('sentinel', 26)]],
    ['duplicate', [exactPatient('duplicate', 18), exactPatient('duplicate', 18)]],
    ['empty', [exactPatient('empty', 0)]],
    ['wrong-patient-scope', [exactPatient('wrong-patient-scope', 18, { project: 'other-project' })]],
    ['wrong-observation-scope', [wrongObservationScope]],
    ['valid', [exactPatient('valid', 18)]],
  ]);
  const rereadCalls = [];
  const selection = selectRootRebaseWitness(
    candidates,
    patientId => {
      rereadCalls.push(patientId);
      return exactRows.get(patientId) ?? [];
    },
    expectedScope,
  );

  assert.deepEqual(selection.attempts.map(attempt => attempt.outcome), [
    'observation-sentinel-exceeded',
    'patient-row-count',
    'too-few-observations',
    'patient-scope-mismatch',
    'observation-scope-mismatch',
    'accepted',
  ]);
  assert.deepEqual(rereadCalls, candidates.slice(0, 6).map(candidate => candidate.patientId));
  assert.equal(selection.witness.patientId, 'valid');
  assert.equal(selection.witness.observationIDs.length, 18);
  assert.deepEqual(selection.witness.observationIDs, exactObservations(18).map(observation => observation.id));
  assert.equal(selection.candidateLimitReached, false);

  const metadata = buildRootRebaseOracleMetadata(selection.witness, selection);
  assert.equal(metadata.observationSampleLimit, 10_000);
  assert.equal(metadata.candidatePatientLimit, 25);
  assert.deepEqual(metadata.candidateSelectionAttempts, selection.attempts);
  assert.equal(metadata.perPatientObservationSentinelLimit, 26);
  assert.equal(metadata.exactSelectedPatientReread, true);
  assert.deepEqual(metadata.expectedObservationRows, selection.witness.observationIDs);
});

test('root-rebase exact candidate rereads stop after 25 attempts', () => {
  const expectedScope = { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' };
  const candidates = Array.from({ length: 26 }, (_, index) => ({
    patientId: `patient-${index}`,
    sampledObservationCount: 2,
  }));
  const rereadCalls = [];
  const selection = selectRootRebaseWitness(candidates, patientId => {
    rereadCalls.push(patientId);
    return [];
  }, expectedScope);

  assert.equal(rootRebaseOracleBounds.candidatePatientLimit, 25);
  assert.equal(selection.attempts.length, 25);
  assert.equal(rereadCalls.length, 25);
  assert.equal(rereadCalls.includes('patient-25'), false);
  assert.equal(selection.candidateLimitReached, true);
  assert.equal(selection.witness, null);
});

test('root-rebase oracle metadata reports bounded query settings at runtime', () => {
  const witness = {
    patientId: 'patient-example',
    patientKey: 'Patient/example',
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
    observationIDs: ['observation-a', 'observation-b'],
  };
  const selection = { attempts: [{ patientId: witness.patientId, outcome: 'accepted' }], candidateLimitReached: false };
  const metadata = buildRootRebaseOracleMetadata(witness, selection);
  assert.equal(rootRebaseOracleBounds.observationSampleLimit, 10_000);
  assert.equal(rootRebaseOracleBounds.candidatePatientLimit, 25);
  assert.equal(rootRebaseOracleBounds.observationSentinelLimit, 26);
  assert.equal(rootRebaseOracleBounds.maxRuntimeSeconds, 8);
  assert.equal(rootRebaseOracleBounds.memoryLimitBytes, 256 * 1024 * 1024);
  assert.equal(metadata.observationCount, 2);
  assert.equal(metadata.candidatePatientLimit, 25);
  assert.equal(metadata.sampleSelection.includes('candidates'), true);
  assert.deepEqual(metadata.queryCaps, {
    maxRuntimeSeconds: 8,
    memoryLimitBytes: 256 * 1024 * 1024,
    hostTimeoutMs: 30_000,
  });
  assert.deepEqual(metadata.expectedPatientRows, [witness.patientId]);
  assert.deepEqual(metadata.expectedObservationRows, witness.observationIDs);
  assert.deepEqual(metadata.expectedRestoredPatientRows, [witness.patientId]);
});
