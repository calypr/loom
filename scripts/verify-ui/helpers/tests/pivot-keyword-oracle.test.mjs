import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { buildPivotKeywordOracle, PIVOT_KEYWORD_NAMES, DEVLOOP_OBSERVATION_PATH } from '../pivot-keyword-fixture-oracle.mjs';

test('real fixture yields the audited single-group pivot oracle', () => {
  const oracle = buildPivotKeywordOracle();
  assert.equal(oracle.groupRowCount, 1);
  assert.deepEqual(oracle.statuses, [null]);
  assert.deepEqual({ ...oracle.categorySums }, {
    'Patient/dev-patient-001': 352.5,
    'Patient/dev-patient-002': 68,
    __missing__: 0,
  });
  assert.deepEqual([...PIVOT_KEYWORD_NAMES], ['null', 'RETURN', 'Filter']);
});

test('keyword names survive the authoring physical-column pattern', () => {
  const physicalColumnPattern = /^[A-Za-z_]\w*$/;
  for (const name of PIVOT_KEYWORD_NAMES) {
    assert.ok(physicalColumnPattern.test(name), `${name} must satisfy the server identity gate`);
  }
});

test('drifted fixture bytes fail loudly instead of passing vacuous grids', () => {
  const dir = mkdtempSync(join(tmpdir(), 'pivotkw-oracle-'));
  const path = join(dir, 'Observation.ndjson');

  writeFileSync(path, [
    { resourceType: 'Observation', status: { value: 'final' }, subject: { reference: 'Patient/x' }, valueQuantity: { value: 1 } },
    { resourceType: 'Observation', status: { value: 'amended' }, subject: { reference: 'Patient/x' }, valueQuantity: { value: 2 } },
  ].map((row) => JSON.stringify(row)).join('\n') + '\n');
  assert.throws(() => buildPivotKeywordOracle(path), /row count drifted/);

  const tampered = [
    { resourceType: 'Observation', subject: { reference: 'Patient/dev-patient-001' }, valueQuantity: { value: 352.5 } },
    { resourceType: 'Observation', subject: { reference: 'Patient/dev-patient-002' }, valueQuantity: { value: 69 } },
    { resourceType: 'Observation', subject: { reference: 'Patient/dev-patient-002' }, valueQuantity: { value: 68 } },
    { resourceType: 'Observation' },
    { resourceType: 'Observation' },
    { resourceType: 'Observation' },
  ];
  writeFileSync(path, tampered.map((row) => JSON.stringify(row)).join('\n') + '\n');
  assert.throws(() => buildPivotKeywordOracle(path), /per-category sums drifted/);
});

test('oracle module keeps the shared fixture path absolute and file-backed', () => {
  assert.ok(DEVLOOP_OBSERVATION_PATH.startsWith('/'));
  assert.ok(DEVLOOP_OBSERVATION_PATH.endsWith('Observation.ndjson'));
});
