import assert from 'node:assert/strict';
import test from 'node:test';
import { hashScopedPayloadRows } from '../cda-payload-content-hashes.mjs';

const IDS = Object.freeze([
  '26c7500c-f54f-5229-b5a7-a6757614bc21',
  '64704041-c4da-5375-aac0-88f951f78344',
  '7d2fe423-f934-56ff-bd96-426fb1fdf94e',
]);
const PAYLOADS = Object.freeze([
  Object.freeze({ id: IDS[0], payload: Object.freeze({ resourceType: 'Observation', status: 'final', code: { coding: [{ system: 'http://snomed.info/sct', code: '1222593009' }] } }) }),
  Object.freeze({ id: IDS[1], payload: Object.freeze({ resourceType: 'Observation', status: 'final', code: { coding: [{ system: 'http://snomed.info/sct', code: '1222593009' }] } }) }),
  Object.freeze({ id: IDS[2], payload: Object.freeze({ resourceType: 'Observation', status: 'final', code: { coding: [{ system: 'http://loinc.org', code: '81247-9' }] } }) }),
]);

test('hashes exactly the selected payload content deterministically without exposing payloads', () => {
  const expected = hashScopedPayloadRows(PAYLOADS, IDS);
  assert.deepEqual(hashScopedPayloadRows([...PAYLOADS].reverse(), IDS), expected,
    'Database row order must not change the payload digests.');
  const reorderedKeys = PAYLOADS.map(({ id, payload }) => ({ id, payload: {
    code: { coding: payload.code.coding.map(({ system, code }) => ({ code, system })) },
    status: payload.status,
    resourceType: payload.resourceType,
  } }));
  assert.deepEqual(hashScopedPayloadRows(reorderedKeys, IDS), expected,
    'JSON object key order must not change the payload digests.');
  assert.deepEqual(expected.map(({ id }) => id), [...IDS].sort());
  assert.deepEqual(expected.map(({ payloadHash }) => payloadHash), [
    'adb9f600c135c75736c4644d7adb442e7c519f3353c86f8de28b5416f1904912',
    'adb9f600c135c75736c4644d7adb442e7c519f3353c86f8de28b5416f1904912',
    '13c4f7aedb484bd5aa694a02c0eb3ef6c6f102895975028363a73a7a3214662f',
  ]);
  const changed = PAYLOADS.map(row => row.id === IDS[0]
    ? { ...row, payload: { ...row.payload, status: 'amended' } }
    : row);
  assert.notEqual(hashScopedPayloadRows(changed, IDS).find(row => row.id === IDS[0]).payloadHash,
    expected.find(row => row.id === IDS[0]).payloadHash,
    'A selected payload content change must change its digest.');
  assert.throws(() => hashScopedPayloadRows([...PAYLOADS, { id: 'outside-scope', payload: {} }], IDS),
    /exactly the selected source IDs/);
});
