import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { registry, scenarioCaseFor } from '../../registry.mjs';
import { readRelatedExpandFieldLifecycleOracle } from '../related-expand-field-lifecycle-oracle.mjs';

const fixtureDir = fileURLToPath(new URL('../../../../testdata/basic-related-expand-field-lifecycle/', import.meta.url));

test('literal fixture oracle retains two related matches, one unmatched root, and exact public-field groups', () => {
  const oracle = readRelatedExpandFieldLifecycleOracle(fixtureDir);
  assert.equal(oracle.scope.rootResourceType, 'Observation');
  assert.deepEqual(oracle.route.map(({ fromResourceType, toResourceType, storageDirection }) =>
    [fromResourceType, toResourceType, storageDirection]), [
    ['Observation', 'Patient', 'OUTBOUND'],
    ['Patient', 'MedicationAdministration', 'INBOUND'],
  ]);
  assert.deepEqual(oracle.rows.map(row => [row.observationStatus, row.medicationAdministrationStatus]), [
    ['final', 'completed'],
    ['final', 'in-progress'],
    ['preliminary', null],
  ]);
  assert.deepEqual(oracle.rows.map(row => [row.patientId, row.patientGender, row.patientBirthDate]), [
    ['field-lifecycle-patient-matched', 'female', '1980-04-03'],
    ['field-lifecycle-patient-matched', 'female', '1980-04-03'],
    ['field-lifecycle-patient-unmatched', 'male', '1990-02-10'],
  ]);
  assert.deepEqual(oracle.groups, [
    { observationStatus: 'final', count: 2 },
    { observationStatus: 'preliminary', count: 1 },
  ]);
  assert(oracle.sourceFiles.every(file => /^[a-f0-9]{64}$/.test(file.sha256)));
});

test('literal fixture oracle rejects a changed route that invents a match for the unmatched Observation', () => {
  const temporary = mkdtempSync(join(tmpdir(), 'related-expand-fields-'));
  try {
    for (const type of ['Patient', 'Observation', 'MedicationAdministration']) {
      writeFileSync(join(temporary, `${type}.ndjson`), readFileSync(join(fixtureDir, `${type}.ndjson`)));
    }
    const path = join(temporary, 'MedicationAdministration.ndjson');
    const existing = readFileSync(path, 'utf8').trimEnd();
    writeFileSync(path, `${existing}\n${JSON.stringify({
      resourceType: 'MedicationAdministration',
      id: 'field-lifecycle-administration-unexpected',
      status: 'completed',
      subject: { reference: 'Patient/field-lifecycle-patient-unmatched' },
    })}\n`);
    assert.throws(() => readRelatedExpandFieldLifecycleOracle(temporary), /fixture IDs changed/);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
});

test('related-source lifecycle phase references stay bound to their named required checks', () => {
  const scenario = registry.find(entry => entry.id === 'basic-related-expand-field-lifecycle');
  assert(scenario, 'The related-field lifecycle scenario must remain registered.');
  const coverage = scenario.coverage.find(item =>
    item.feature === 'Basic RelatedExpand generic related-source and active-record field lifecycle with public-root Group');
  assert(coverage, 'The generic related-source lifecycle coverage row must remain registered.');
  const contract = scenarioCaseFor(scenario, 'observation-root-medication-administration');
  const expectedPhases = {
    choice: 'native Patient.gender choice is populated and exposes the compiler-proven related source',
    proposal: 'Patient.gender related-source preview preserves values across matched and unmatched rows',
    cancel: 'RELATED_SOURCE Cancel preserves exact filtered rows and the saved draft',
    apply: 'RELATED_SOURCE Apply preserves Patient.gender across matched and unmatched rows',
    savedRows: 'RELATED_SOURCE Apply preserves Patient.gender across matched and unmatched rows',
    reload: 'RELATED_SOURCE reload preserves Patient.gender across matched and unmatched rows',
    edit: 'editing the related field label preserves its source binding and exact expanded rows after reload',
    restoration: 'removing the related field restores exact expanded rows and source construction after reload',
  };
  assert.deepEqual(Object.keys(coverage.acceptance.checks).sort(), Object.keys(expectedPhases).sort());
  for (const [phase, requiredCheck] of Object.entries(expectedPhases)) {
    assert.equal(contract.requiredChecks[coverage.acceptance.checks[phase]], requiredCheck,
      `${phase} must point to its exact registered check label`);
  }
});
