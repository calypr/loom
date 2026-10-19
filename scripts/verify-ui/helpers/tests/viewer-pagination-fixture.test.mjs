import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const fixtureDir = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../testdata/viewer-pagination');

test('Viewer pagination fixture pins six Patient IDs across a five-row page boundary', () => {
  const expectedIDs = [
    'page-patient-001',
    'page-patient-002',
    'page-patient-003',
    'page-patient-004',
    'page-patient-005',
    'page-patient-006',
  ];
  const records = readFileSync(resolve(fixtureDir, 'Patient.ndjson'), 'utf8')
    .trim()
    .split('\n')
    .map((line) => JSON.parse(line));

  assert.ok(records.every((record) => record.resourceType === 'Patient'));
  const ids = records.map((record) => record.id);
  assert.deepEqual(ids, expectedIDs);

  const minimumPageSize = 5;
  const firstPage = ids.slice(0, minimumPageSize);
  const secondPage = ids.slice(minimumPageSize, minimumPageSize * 2);
  assert.deepEqual(firstPage, expectedIDs.slice(0, minimumPageSize));
  assert.deepEqual(secondPage, ['page-patient-006']);
  assert.deepEqual([...firstPage, ...secondPage], expectedIDs);
});
