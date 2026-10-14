import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
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
