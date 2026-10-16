import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

export const readViewerSharedFilterOracle = target => {
  const path = join(target.fixtureDir, 'Patient.ndjson');
  const bytes = readFileSync(path);
  const patients = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const rows = patients.map(patient => ({ id: patient.id, gender: patient.gender ?? '' }))
    .sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(rows, [
    { id: 'dev-patient-001', gender: 'female' },
    { id: 'dev-patient-002', gender: '' },
  ], 'shared-filter case requires the exact two independent Patient fixture rows');
  return {
    path,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    rows,
    allIDs: rows.map(row => row.id),
    secondIDs: ['dev-patient-002'],
    selectedID: 'dev-patient-001',
  };
};
