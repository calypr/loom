import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { readNDJSONResourceIdentityOracle } from './ndjson-resource-oracle.mjs';

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
