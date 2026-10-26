import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';

export const BASIC_RECORDED_CODE_GROUPS = [
  {
    system: 'https://example.test/codes',
    version: null,
    code: 'height',
    count: 2,
    sourceIDs: ['dev-observation-001', 'dev-observation-003'],
  },
  {
    system: 'https://example.test/codes',
    version: null,
    code: 'weight',
    count: 1,
    sourceIDs: ['dev-observation-002'],
  },
  {
    system: 'urn:loom:fixture:panel',
    version: null,
    code: 'paired',
    count: 3,
    sourceIDs: ['dev-pair-001', 'dev-pair-002', 'dev-pair-003'],
  },
];

const tupleKey = ({ system, version, code }) => JSON.stringify([system, version, code]);

const compareTuples = (left, right) => left.system.localeCompare(right.system)
  || (left.version ?? '').localeCompare(right.version ?? '')
  || left.code.localeCompare(right.code);

export function groupRecordedCodesBySource(records) {
  assert(Array.isArray(records) && records.length > 0, 'Basic recorded-code oracle requires source records');
  const sourceIDs = new Set();
  const groups = new Map();

  for (const record of records) {
    assert.equal(record?.resourceType, 'Observation', 'Recorded-code source must contain Observation resources');
    assert(typeof record.id === 'string' && record.id.length > 0, 'Every Observation needs a literal source identity');
    assert(!sourceIDs.has(record.id), `Observation source identity ${record.id} must be unique`);
    sourceIDs.add(record.id);

    const codings = record.code?.coding;
    assert(Array.isArray(codings) && codings.length > 0,
      `Observation/${record.id} must have populated direct code.coding[] values`);
    const sourceTuples = new Map();
    for (const coding of codings) {
      assert(typeof coding?.system === 'string' && coding.system.trim().length > 0,
        `Observation/${record.id} direct Coding needs a populated system`);
      assert(typeof coding.code === 'string' && coding.code.trim().length > 0,
        `Observation/${record.id} direct Coding needs a populated code`);
      const tuple = {
        system: coding.system,
        version: typeof coding.version === 'string' && coding.version.trim().length > 0 ? coding.version : null,
        code: coding.code,
      };
      sourceTuples.set(tupleKey(tuple), tuple);
    }

    for (const [key, tuple] of sourceTuples) {
      const group = groups.get(key) ?? { ...tuple, count: 0, sourceIDs: [] };
      group.count += 1;
      group.sourceIDs.push(record.id);
      groups.set(key, group);
    }
  }

  return [...groups.values()]
    .map(group => ({ ...group, sourceIDs: group.sourceIDs.sort() }))
    .sort(compareTuples);
}

export async function readBasicCodedGroupRecordedCodeOracle({ path, project, generation }) {
  for (const [name, value] of Object.entries({ path, project, generation })) {
    assert(typeof value === 'string' && value.length > 0, `${name} is required for the Basic coded-group oracle`);
  }
  const source = await readFile(path);
  const records = source.toString('utf8').split(/\r?\n/).filter(line => line.trim()).map(line => JSON.parse(line));
  const groups = groupRecordedCodesBySource(records);
  const ids = records.map(record => record.id).sort();
  assert.deepEqual(ids, [
    'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
    'dev-pair-001', 'dev-pair-002', 'dev-pair-003',
  ], 'Basic Observation fixture must retain the six exact source identities');
  assert.deepEqual(groups, BASIC_RECORDED_CODE_GROUPS,
    'Basic Observation fixture must yield the exact literal direct-code tuples and source counts');

  return {
    path,
    sha256: createHash('sha256').update(source).digest('hex'),
    project,
    generation,
    resourceType: 'Observation',
    codingPath: 'code.coding[]',
    sourceCount: records.length,
    sourceIDs: ids,
    groups,
  };
}

export const visibleRecordedCodeGroupRows = groups => groups.map(group => [
  group.system,
  group.version ?? '—',
  group.code,
  String(group.count),
]);
