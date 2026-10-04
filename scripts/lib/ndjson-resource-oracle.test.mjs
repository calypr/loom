import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { assertVisibleRowsMatchOracle, readNDJSONResourceIdentityOracle } from './ndjson-resource-oracle.mjs';

test('raw resource oracle retains its file identity and orders ids by storage key', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-ndjson-oracle-'));
  try {
    const path = join(directory, 'Specimen.ndjson');
    const content = [
      { resourceType: 'Specimen', id: 'zeta' },
      { resourceType: 'Specimen', id: 'alpha' },
      { resourceType: 'Specimen', id: 'middle' },
    ].map(record => JSON.stringify(record)).join('\n') + '\n';
    await writeFile(path, content);
    const oracle = await readNDJSONResourceIdentityOracle({ path, project: 'qa', generation: 'cda-fhir-v1', resourceType: 'Specimen' });
    const expectedKey = id => createHash('sha256').update(['vertex', 'qa', 'cda-fhir-v1', 'Specimen', id, ''].join('\0')).digest('hex');
    assert.deepEqual(oracle.ids, ['zeta', 'alpha', 'middle'].sort((left, right) => expectedKey(left).localeCompare(expectedKey(right))));
    assert.equal(oracle.count, 3);
    assert.equal(oracle.sha256, createHash('sha256').update(content).digest('hex'));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('raw resource oracle rejects duplicate identities and wrong resource types', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-ndjson-oracle-'));
  try {
    const path = join(directory, 'Specimen.ndjson');
    await writeFile(path, `${JSON.stringify({ resourceType: 'Specimen', id: 'same' })}\n${JSON.stringify({ resourceType: 'Specimen', id: 'same' })}\n`);
    await assert.rejects(readNDJSONResourceIdentityOracle({ path, project: 'qa', generation: 'cda-fhir-v1', resourceType: 'Specimen' }), /identities must be unique/);
    await writeFile(path, `${JSON.stringify({ resourceType: 'Patient', id: 'wrong-type' })}\n`);
    await assert.rejects(readNDJSONResourceIdentityOracle({ path, project: 'qa', generation: 'cda-fhir-v1', resourceType: 'Specimen' }), /must contain Specimen records/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('visible preview rows must be a contiguous oracle window with the exact independent preview count', () => {
  const sourceIds = ['one', 'two', 'three'];
  const rows = [
    { ordinal: 1, cells: ['one'] },
    { ordinal: 2, cells: ['two'] },
  ];
  assert.deepEqual(assertVisibleRowsMatchOracle({ rows, sourceIds, ariaRowCount: '4', previewLimit: 25 }), rows);
  assert.throws(() => assertVisibleRowsMatchOracle({ rows: rows.slice(1), sourceIds, ariaRowCount: '4' }), /contiguous from the start/,
    'An omitted leading visible row must fail');
  assert.throws(() => assertVisibleRowsMatchOracle({ rows: [rows[0], { ordinal: 3, cells: ['three'] }], sourceIds, ariaRowCount: '4' }), /independent source preview window/,
    'A missing visible row ordinal must fail');
  assert.throws(() => assertVisibleRowsMatchOracle({ rows: [{ ordinal: 1, cells: ['incorrect'] }], sourceIds, ariaRowCount: '4' }), /independent source preview window/,
    'An incorrect visible value must fail');
  assert.throws(() => assertVisibleRowsMatchOracle({ rows, sourceIds, ariaRowCount: '3' }), /must report 3 data rows/,
    'A preview that claims fewer rows must fail');
});
