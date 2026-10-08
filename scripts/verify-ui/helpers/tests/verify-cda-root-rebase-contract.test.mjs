import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { scenarioCaseFor } from '../../registry.mjs';
import { buildRootRebaseOracleMetadata, rootRebaseOracleBounds } from '../../workflows/verify-cda-root-rebase.mjs';

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

test('root-rebase oracle metadata resolves the configured bounds at runtime', () => {
  const witness = { patientId: 'Patient/example', observationIDs: ['Observation/a', 'Observation/b'] };
  const metadata = buildRootRebaseOracleMetadata(witness);
  assert.equal(rootRebaseOracleBounds.patientCandidateLimit, 2000);
  assert.equal(rootRebaseOracleBounds.observationSentinelLimit, 26);
  assert.deepEqual(metadata, {
    patientId: witness.patientId,
    observationIDs: witness.observationIDs,
    observationCount: 2,
    candidatePatientLimit: 2000,
    candidatePatientSort: 'patient.id',
    perPatientObservationSentinelLimit: 26,
    sentinelMeaning: 'A 26-row result is at least 26 matches and is excluded; only exact rereads with 2–25 rows are accepted.',
    queryCaps: { maxRuntimeSeconds: 8, memoryLimitBytes: 256 * 1024 * 1024, hostTimeoutMs: 30_000 },
    exactSelectedPatientReread: true,
    expectedPatientRows: [witness.patientId],
    expectedObservationRows: witness.observationIDs,
    expectedRestoredPatientRows: [witness.patientId],
    visiblePreviewLimit: 25,
  });
});
