import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const BASIC_VIEWER_CHARTS_FIXTURE_DIR = fileURLToPath(
  new URL('../../../testdata/devloop-fixture/', import.meta.url),
);

export const readBasicViewerChartsOracle = ({
  fixtureDir = BASIC_VIEWER_CHARTS_FIXTURE_DIR,
} = {}) => {
  const path = join(fixtureDir, 'Patient.ndjson');
  const bytes = readFileSync(path);
  const patients = bytes.toString('utf8')
    .split(/\r?\n/)
    .filter(Boolean)
    .map(line => JSON.parse(line));

  const ids = patients.map(patient => patient.id).sort();
  assert.deepEqual(ids, ['dev-patient-001', 'dev-patient-002'],
    'Viewer chart oracle must contain exactly the two independent Patient identities');
  assert(patients.every(patient => patient.resourceType === 'Patient'),
    'Viewer chart fixture rows must all be Patient resources');

  const rows = patients.map(patient => {
    if (!Object.hasOwn(patient, 'gender')) return { id: patient.id, state: 'missing' };
    if (patient.gender === null) return { id: patient.id, state: 'null', gender: null };
    return { id: patient.id, state: 'present', gender: patient.gender };
  }).sort((left, right) => left.id.localeCompare(right.id));

  assert.deepEqual(rows, [
    { id: 'dev-patient-001', state: 'present', gender: 'female' },
    { id: 'dev-patient-002', state: 'missing' },
  ], 'Viewer chart oracle must distinguish the exact female value from omitted gender');

  return {
    path,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    resourceType: 'Patient',
    rows,
    displayedCount: rows.length,
    populatedCount: rows.filter(row => row.state === 'present').length,
    missingCount: rows.filter(row => row.state === 'missing' || row.state === 'null').length,
    expectedTerms: [{ key: 'female', doc_count: 1 }],
  };
};
