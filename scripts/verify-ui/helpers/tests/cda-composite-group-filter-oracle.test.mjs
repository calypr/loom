import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CDA_COMPOSITE_GROUP_FILTER_SCOPE,
  prepareCdaCompositeGroupFilterOracle,
  validateCdaCompositeGroupCandidate,
  validateCdaCompositeScalarFieldCandidate,
} from '../cda-composite-group-filter-oracle.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const rawRows = [
  {
    _id: 'Observation/g_002482914ae97ded29d46c1544fe91cfe27045c3bad2f6c2aa47c78d1aceba8a',
    id: '9bd424bb-9617-561e-a787-c9506066eb59',
    project,
    generation,
    resourceType: 'Observation',
    subjectReference: 'Patient/033718b9-adff-54b7-81a3-b1c390990d28',
  },
  {
    _id: 'Observation/g_0085ddbf0da77b57206f4b61163804ed6bf8acfd753ab0df2cfed281467795e4',
    id: '7d2fe423-f934-56ff-bd96-426fb1fdf94e',
    project,
    generation,
    resourceType: 'Observation',
    subjectReference: 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2',
  },
  {
    _id: 'Observation/g_00949cd27ee02ff9d84c2ec3f7bbbd0310ec111549529630f72612193c5a7676',
    id: '26c7500c-f54f-5229-b5a7-a6757614bc21',
    project,
    generation,
    resourceType: 'Observation',
    subjectReference: 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2',
  },
  {
    _id: 'Observation/g_2cf7f289f769d5ee33f965257888b111d64234e442fc59d5552868583265ff61',
    id: '64704041-c4da-5375-aac0-88f951f78344',
    project,
    generation,
    resourceType: 'Observation',
    subjectReference: 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2',
  },
];

const sourceIDColumn = {
  columnId: 'source-observation-id',
  logicalType: 'string',
  source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
};
const sourceSubjectColumn = {
  columnId: 'source-observation-subject-reference',
  logicalType: 'string',
  source: { kind: 'field', field: { path: 'subject.reference', projectionMode: 'VALUE' } },
};
const sourceIDCandidate = {
  candidateId: 'candidate-observation-id',
  fieldPath: 'id',
  cardinality: 'ONE',
  shape: 'SCALAR',
  logicalType: 'string',
  valueType: 'STRING',
};
const sourceSubjectCandidate = {
  candidateId: 'candidate-observation-subject-reference',
  fieldPath: 'subject.reference',
  cardinality: 'ONE',
  shape: 'SCALAR',
  logicalType: 'string',
  valueType: 'STRING',
};
const candidateConstruction = {
  version: 1,
  steps: [{
    id: 'group-composite-observation-keys',
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: 'group-composite-observation-keys',
        missingKeyPolicy: 'GROUP',
        keys: [
          { inputColumnId: sourceIDColumn.columnId, outputColumnId: 'group-observation-id' },
          { inputColumnId: sourceSubjectColumn.columnId, outputColumnId: 'group-subject-reference' },
        ],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'group-row-count' }],
      },
    },
  }],
};

test('composite Group oracle derives four exact raw id/subject tuples and both subject filters', () => {
  assert.deepEqual(CDA_COMPOSITE_GROUP_FILTER_SCOPE, { project, generation });
  const oracle = prepareCdaCompositeGroupFilterOracle(rawRows, { project, generation });

  assert.deepEqual(oracle.groupRows, [
    ['26c7500c-f54f-5229-b5a7-a6757614bc21', 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2', '1'],
    ['64704041-c4da-5375-aac0-88f951f78344', 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2', '1'],
    ['7d2fe423-f934-56ff-bd96-426fb1fdf94e', 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2', '1'],
    ['9bd424bb-9617-561e-a787-c9506066eb59', 'Patient/033718b9-adff-54b7-81a3-b1c390990d28', '1'],
  ]);
  assert.deepEqual(oracle.sharedSubjectRows, [
    ['26c7500c-f54f-5229-b5a7-a6757614bc21', 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2', '1'],
    ['64704041-c4da-5375-aac0-88f951f78344', 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2', '1'],
    ['7d2fe423-f934-56ff-bd96-426fb1fdf94e', 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2', '1'],
  ]);
  assert.deepEqual(oracle.leftOnlySubjectRows, [
    ['9bd424bb-9617-561e-a787-c9506066eb59', 'Patient/033718b9-adff-54b7-81a3-b1c390990d28', '1'],
  ]);
  assert(new Set(oracle.groupRows.map(([id]) => id)).size === 4,
    'Composite keys must use all four distinct raw FHIR Observation id values.');
  assert(oracle.groupRows.every(([id]) => !id.startsWith('Observation/')),
    'The Group key uses raw Observation.id values, not Arango document keys.');
});

test('composite Group candidate binds the exact two scalar source IDs and COUNT_ROWS', () => {
  assert.deepEqual(validateCdaCompositeGroupCandidate({
    candidateConstruction,
    sourceIDColumn,
    sourceSubjectColumn,
  }), {
    stepId: 'group-composite-observation-keys',
    keyInputColumnIds: ['source-observation-id', 'source-observation-subject-reference'],
    keyOutputColumnIds: ['group-observation-id', 'group-subject-reference'],
    aggregate: { operation: 'COUNT_ROWS', outputColumnId: 'group-row-count' },
  });
});

test('composite Group source candidates require explicit ONE/SCALAR fields', () => {
  assert.equal(validateCdaCompositeScalarFieldCandidate(sourceIDCandidate, 'id', 'Observation.id'),
    'candidate-observation-id');
  assert.equal(validateCdaCompositeScalarFieldCandidate(sourceSubjectCandidate, 'subject.reference', 'Observation.subject.reference'),
    'candidate-observation-subject-reference');

  assert.throws(() => validateCdaCompositeScalarFieldCandidate({
    ...sourceSubjectCandidate,
    fieldPath: 'code.coding.code',
    cardinality: 'MANY',
    shape: 'REPEATED',
  }, 'subject.reference', 'Observation.subject.reference'), /must resolve to subject\.reference/);
  assert.throws(() => validateCdaCompositeScalarFieldCandidate({
    ...sourceSubjectCandidate,
    cardinality: 'MANY',
  }, 'subject.reference', 'Observation.subject.reference'), /one-valued source cardinality/);
  assert.throws(() => validateCdaCompositeScalarFieldCandidate({
    ...sourceSubjectCandidate,
    shape: 'REPEATED',
  }, 'subject.reference', 'Observation.subject.reference'), /scalar field form/);
});

test('composite Group oracle rejects bad scope, duplicate IDs, and a missing second key', () => {
  assert.throws(() => prepareCdaCompositeGroupFilterOracle(rawRows, { project: 'other-project', generation }), /retained CDA project/);
  assert.throws(() => prepareCdaCompositeGroupFilterOracle(rawRows, { project, generation: 'other-generation' }), /retained CDA generation/);
  assert.throws(() => prepareCdaCompositeGroupFilterOracle([
    ...rawRows.slice(0, 3), { ...rawRows[3], id: rawRows[2].id },
  ], { project, generation }), /repeated raw Observation id/);

  const missingSecondKey = structuredClone(candidateConstruction);
  missingSecondKey.steps[0].operation.group.keys.pop();
  assert.throws(() => validateCdaCompositeGroupCandidate({
    candidateConstruction: missingSecondKey,
    sourceIDColumn,
    sourceSubjectColumn,
  }), /exact scalar Observation id and subject\.reference columns/);
});
