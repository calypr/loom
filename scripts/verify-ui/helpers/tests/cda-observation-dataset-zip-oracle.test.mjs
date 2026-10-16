import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  assertCdaObservationDatasetZipReread,
  cdaObservationDatasetZipExpectedRows,
  cdaObservationDatasetZipPreviewRowsMatch,
  cdaObservationDatasetZipRereadQuery,
  cdaObservationDatasetZipWitnessQuery,
  CDA_OBSERVATION_DATASET_ZIP_CODE,
  CDA_OBSERVATION_DATASET_ZIP_GENERATION,
  CDA_OBSERVATION_DATASET_ZIP_PROJECT,
  CDA_OBSERVATION_DATASET_ZIP_SYSTEM,
  CDA_OBSERVATION_DATASET_ZIP_WITNESSES,
  prepareCdaObservationDatasetZipOracle,
} from '../cda-observation-dataset-zip-oracle.mjs';

const project = CDA_OBSERVATION_DATASET_ZIP_PROJECT;
const generation = CDA_OBSERVATION_DATASET_ZIP_GENERATION;

const rawRows = () => CDA_OBSERVATION_DATASET_ZIP_WITNESSES.map((witness, index) => ({
  _id: `Observation/${witness.id}`,
  id: witness.id,
  payloadId: witness.id,
  project,
  generation,
  resourceType: 'Observation',
  status: witness.status,
  components: [
    {
      code: { coding: [{ system: CDA_OBSERVATION_DATASET_ZIP_SYSTEM, code: CDA_OBSERVATION_DATASET_ZIP_CODE }] },
      valueString: witness.valueString,
    },
    {
      code: { coding: [{ system: CDA_OBSERVATION_DATASET_ZIP_SYSTEM, code: 'primary_disease_type' }] },
      valueString: `Disease ${index + 1}`,
    },
  ],
}));

test('Observation witness lookup is bounded to the exact pinned IDs, project, and generation', () => {
  const query = cdaObservationDatasetZipWitnessQuery({ project, generation });
  for (const witness of CDA_OBSERVATION_DATASET_ZIP_WITNESSES) assert(query.includes(witness.id));
  assert(query.includes(`r.project == "${project}"`));
  assert(query.includes(`r.dataset_generation == "${generation}"`));
  assert(query.includes('r.payload.component'));
  assert(!query.includes('LIMIT'), 'Exact-ID witness lookup must not imply a broader resource scan');
  assert.equal(cdaObservationDatasetZipRereadQuery({ project, generation }), query);
});

test('raw oracle preserves exact coded component owner objects as typed records', () => {
  const rows = rawRows();
  const oracle = prepareCdaObservationDatasetZipOracle(rows, { project, generation });
  assert.deepEqual(oracle.observationIDs, CDA_OBSERVATION_DATASET_ZIP_WITNESSES.map(row => row.id).sort());
  assert.deepEqual(oracle.references, oracle.observationIDs.map(id => ({
    project, generation, resourceType: 'Observation', id,
  })));
  assert.equal(oracle.rows.length, 3);
  for (const row of oracle.rows) {
    const records = row.component_owner_records;
    assert.equal(records.length, 1);
    assert.deepEqual(records[0], {
      source: { resourceType: 'Observation', resourceId: row.observation_id,
        ownerPath: 'component[]', ownerOrdinal: 0 },
      codings: [{ system: CDA_OBSERVATION_DATASET_ZIP_SYSTEM, code: CDA_OBSERVATION_DATASET_ZIP_CODE }],
      choiceArm: 'valueString', logicalType: 'string',
      value: CDA_OBSERVATION_DATASET_ZIP_WITNESSES.find(item => item.id === row.observation_id).valueString,
      values: [CDA_OBSERVATION_DATASET_ZIP_WITNESSES.find(item => item.id === row.observation_id).valueString],
      unit: null, status: 'VALUE', owner: rows.find(item => item.id === row.observation_id).components[0],
    });
  }
});

test('raw reread rejects changed component identity, coding, order, and source status', () => {
  const source = rawRows();
  const oracle = prepareCdaObservationDatasetZipOracle(source, { project, generation });
  const accepted = assertCdaObservationDatasetZipReread(source, oracle);
  assert.deepEqual(accepted, { exact: true, count: 3, project, generation });
  assert.doesNotThrow(() => assertCdaObservationDatasetZipReread([...source].reverse(), oracle),
    'Top-level reread row order is canonicalized by Observation ID');
  const reorderedOwners = structuredClone(source);
  reorderedOwners[0].components.reverse();
  assert.throws(() => assertCdaObservationDatasetZipReread(reorderedOwners, oracle),
    /complete ordered component objects/, 'Component owner order remains exact');
  const altered = structuredClone(source);
  altered[0].components[0].valueString = 'changed';
  assert.throws(() => assertCdaObservationDatasetZipReread(altered, oracle), /complete ordered component objects/);
  const wrongStatus = structuredClone(source);
  wrongStatus[0].status = 'amended';
  assert.throws(() => assertCdaObservationDatasetZipReread(wrongStatus, oracle), /statuses/);
});

test('expected archive rows keep object-list values without scalarizing component owners', () => {
  const oracle = prepareCdaObservationDatasetZipOracle(rawRows(), { project, generation });
  const rows = cdaObservationDatasetZipExpectedRows(oracle, {
    id: 'observation_id', status: 'observation_status', componentOwnerRecords: 'specimen_type',
  });
  assert.equal(rows.length, 3);
  for (const row of rows) {
    assert.equal(typeof row.observation_id, 'string');
    assert.equal(row.observation_status, 'final');
    assert.equal(Array.isArray(row.specimen_type), true);
    assert.equal(typeof row.specimen_type[0].owner, 'object');
    assert.deepEqual(row.specimen_type[0].owner.code.coding, [
      { system: CDA_OBSERVATION_DATASET_ZIP_SYSTEM, code: CDA_OBSERVATION_DATASET_ZIP_CODE },
    ]);
  }
});

test('compiled Preview comparison excludes only unique internal row IDs and keeps the exact public typed schema', () => {
  const oracle = prepareCdaObservationDatasetZipOracle(rawRows(), { project, generation });
  const expected = cdaObservationDatasetZipExpectedRows(oracle, {
    id: 'observation_id', status: 'observation_status', componentOwnerRecords: 'specimen_type',
  });
  const actual = [...expected].reverse().map((row, index) => ({
    __loom_row_id: `row-${index + 1}`,
    ...structuredClone(row),
  }));
  assert.equal(cdaObservationDatasetZipPreviewRowsMatch(actual, expected, { idColumn: 'observation_id' }), true,
    'Preview may add unique compiler row identities while preserving every authored public value');

  const wrongOwner = structuredClone(actual);
  wrongOwner[0].specimen_type[0].owner.valueString = 'wrong';
  assert.equal(cdaObservationDatasetZipPreviewRowsMatch(wrongOwner, expected, { idColumn: 'observation_id' }), false,
    'The full nested owner record remains exact');

  const extraColumn = structuredClone(actual);
  extraColumn[0].debug = 'unexpected';
  assert.equal(cdaObservationDatasetZipPreviewRowsMatch(extraColumn, expected, { idColumn: 'observation_id' }), false,
    'Unknown Preview columns are not ignored');

  const missingInternalIdentity = structuredClone(actual);
  delete missingInternalIdentity[0].__loom_row_id;
  assert.equal(cdaObservationDatasetZipPreviewRowsMatch(missingInternalIdentity, expected,
    { idColumn: 'observation_id' }), false, 'Compiler row identities remain present');

  const duplicateInternalIdentity = structuredClone(actual);
  duplicateInternalIdentity[1].__loom_row_id = duplicateInternalIdentity[0].__loom_row_id;
  assert.equal(cdaObservationDatasetZipPreviewRowsMatch(duplicateInternalIdentity, expected,
    { idColumn: 'observation_id' }), false, 'Internal row identities remain unique');
});
