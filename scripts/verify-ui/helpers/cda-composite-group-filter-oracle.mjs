import assert from 'node:assert/strict';

export const CDA_COMPOSITE_GROUP_FILTER_SCOPE = Object.freeze({
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
});

const expectedWitness = [
  {
    _id: 'Observation/g_002482914ae97ded29d46c1544fe91cfe27045c3bad2f6c2aa47c78d1aceba8a',
    id: '9bd424bb-9617-561e-a787-c9506066eb59',
    subjectReference: 'Patient/033718b9-adff-54b7-81a3-b1c390990d28',
  },
  {
    _id: 'Observation/g_0085ddbf0da77b57206f4b61163804ed6bf8acfd753ab0df2cfed281467795e4',
    id: '7d2fe423-f934-56ff-bd96-426fb1fdf94e',
    subjectReference: 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2',
  },
  {
    _id: 'Observation/g_00949cd27ee02ff9d84c2ec3f7bbbd0310ec111549529630f72612193c5a7676',
    id: '26c7500c-f54f-5229-b5a7-a6757614bc21',
    subjectReference: 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2',
  },
  {
    _id: 'Observation/g_2cf7f289f769d5ee33f965257888b111d64234e442fc59d5552868583265ff61',
    id: '64704041-c4da-5375-aac0-88f951f78344',
    subjectReference: 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2',
  },
];

const sharedSubject = 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2';
const leftOnlySubject = 'Patient/033718b9-adff-54b7-81a3-b1c390990d28';
const nonempty = value => typeof value === 'string' && value.trim().length > 0;

function validateSourceColumn(column, expectedPath, label) {
  assert(nonempty(column?.columnId), `${label} must have an exact scalar source columnId.`);
  assert.equal(column.logicalType, 'string', `${label} must be a scalar string.`);
  assert.equal(column.source?.kind, 'field', `${label} must be a direct source field.`);
  assert.equal(column.source?.field?.path, expectedPath, `${label} must bind to ${expectedPath}.`);
  assert.equal(column.source?.field?.projectionMode, 'VALUE', `${label} must project its scalar value.`);
  return column.columnId;
}

export function validateCdaCompositeScalarFieldCandidate(candidate, expectedPath, label, expectedResourceType) {
  assert(nonempty(candidate?.candidateId), `${label} must have an exact catalog candidate ID.`);
  assert.equal(candidate.fieldPath, expectedPath, `${label} must resolve to ${expectedPath}.`);
  assert.equal(candidate.source?.kind, 'FIELD', `${label} must bind to a direct source field.`);
  assert.equal(candidate.source?.candidateId, candidate.candidateId, `${label} source binding must retain its candidate ID.`);
  assert(nonempty(candidate.nodeId), `${label} must bind to an exact catalog node.`);
  assert.equal(candidate.source?.nodeId, candidate.nodeId, `${label} source binding must retain its catalog node.`);
  assert.equal(candidate.source?.resourceType, expectedResourceType, `${label} must bind to ${expectedResourceType}.`);
  assert.equal(candidate.source?.path, expectedPath, `${label} source binding must target ${expectedPath}.`);
  assert(['one', 'optional_one'].includes(candidate.cardinality), `${label} must have one-valued source cardinality.`);
  assert.equal(candidate.source?.cardinality, candidate.cardinality, `${label} source binding must retain its declared cardinality.`);
  assert.equal(candidate.repeated, false, `${label} must be a non-repeated source field.`);
  assert.equal(candidate.logicalType, 'string', `${label} must remain a string candidate.`);
  assert.equal(candidate.defaultProjectionMode, 'VALUE', `${label} must default to scalar VALUE projection.`);
  assert(candidate.projectionModes?.includes('VALUE'), `${label} must advertise scalar VALUE projection.`);
  const valueOptions = candidate.constructionChoice?.options?.filter(option => option.form === 'VALUE') ?? [];
  assert.equal(valueOptions.length, 1, `${label} must advertise exactly one VALUE option.`);
  assert.equal(valueOptions[0].shape, 'SCALAR', `${label} VALUE option must use the scalar form.`);
  assert.equal(valueOptions[0].support, 'SUPPORTED', `${label} scalar VALUE option must be supported.`);
  assert.equal(valueOptions[0].rowEffect, 'PRESERVES_ROW_GRAIN', `${label} scalar VALUE option must preserve row grain.`);
  return candidate.candidateId;
}

export function validateCdaCompositeGroupCandidate({
  candidateConstruction,
  sourceIDColumn,
  sourceSubjectColumn,
}) {
  const idColumnId = validateSourceColumn(sourceIDColumn, 'id', 'Observation ID');
  const subjectColumnId = validateSourceColumn(sourceSubjectColumn, 'subject.reference', 'Observation subject reference');
  assert.notEqual(idColumnId, subjectColumnId, 'The two Group keys must use distinct source column IDs.');

  const steps = candidateConstruction?.steps;
  assert(Array.isArray(steps) && steps.length === 1, 'The composite Group candidate must contain exactly one step.');
  const step = steps[0];
  assert(nonempty(step?.id), 'The composite Group step must have a stable ID.');
  assert.equal(step.inputs?.length, 1, 'The composite Group must have one source input.');
  assert.equal(step.inputs[0]?.kind, 'SOURCE_PROJECTION', 'The composite Group must read the Observation source projection.');
  assert.equal(step.operation?.kind, 'GROUP', 'The candidate step must be GROUP.');
  const group = step.operation.group;
  assert.equal(group?.constructionId, step.id, 'The Group construction ID must remain bound to its step ID.');
  assert.deepEqual(group?.keys?.map(key => key.inputColumnId), [idColumnId, subjectColumnId],
    'The Group must use the exact scalar Observation id and subject.reference columns in order.');
  assert(group.keys.every(key => nonempty(key.outputColumnId)), 'Both Group key output IDs must be present.');
  assert.equal(new Set(group.keys.map(key => key.outputColumnId)).size, 2, 'The two Group key output IDs must be unique.');
  assert.equal(group.aggregates?.length, 1, 'The composite Group must contain one row-count aggregate.');
  const aggregate = group.aggregates[0];
  assert.equal(aggregate.operation, 'COUNT_ROWS', 'The composite Group must count raw Observation rows.');
  assert(nonempty(aggregate.outputColumnId), 'The COUNT_ROWS output ID must be present.');
  assert(!group.keys.some(key => key.outputColumnId === aggregate.outputColumnId),
    'The COUNT_ROWS output ID must be distinct from both Group key outputs.');

  return {
    stepId: step.id,
    keyInputColumnIds: [idColumnId, subjectColumnId],
    keyOutputColumnIds: group.keys.map(key => key.outputColumnId),
    aggregate,
  };
}

function validateWitness(rows, { project, generation }) {
  assert.equal(project, CDA_COMPOSITE_GROUP_FILTER_SCOPE.project,
    'Composite Group Filter oracle requires the retained CDA project.');
  assert.equal(generation, CDA_COMPOSITE_GROUP_FILTER_SCOPE.generation,
    'Composite Group Filter oracle requires the retained CDA generation.');
  assert(Array.isArray(rows) && rows.length === expectedWitness.length,
    'Composite Group Filter oracle requires the exact four-Observation witness.');

  const documentKeys = new Set();
  const fhirIDs = new Set();
  const expectedByDocumentKey = new Map(expectedWitness.map(row => [row._id, row]));
  for (const row of rows) {
    assert.equal(row?.project, project, 'Observation project must remain in the retained CDA scope.');
    assert.equal(row?.generation, generation, 'Observation generation must remain in the retained CDA scope.');
    assert.equal(row?.resourceType, 'Observation', 'Composite Group witness rows must be Observations.');
    assert(nonempty(row?._id), 'Every Observation must have its raw Arango document key.');
    assert(nonempty(row?.id), 'Every Observation must have its raw FHIR id field.');
    assert(nonempty(row?.subjectReference), 'Every Observation must have its raw subject.reference value.');
    assert(!documentKeys.has(row._id), `Composite Group witness repeated document key ${row._id}.`);
    assert(!fhirIDs.has(row.id), `Composite Group witness repeated raw Observation id ${row.id}.`);
    documentKeys.add(row._id);
    fhirIDs.add(row.id);

    const expected = expectedByDocumentKey.get(row._id);
    assert(expected, `Composite Group witness contains unselected document ${row._id}.`);
    assert.equal(row.id, expected.id, `Raw Observation id changed for ${row._id}.`);
    assert.equal(row.subjectReference, expected.subjectReference, `Raw subject.reference changed for ${row._id}.`);
  }
  assert.deepEqual([...documentKeys].sort(), [...expectedByDocumentKey.keys()].sort(),
    'Composite Group witness must contain all and only the retained four Observation IDs.');
  return rows;
}

const compareRows = (left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right));

export function prepareCdaCompositeGroupFilterOracle(rows, { project, generation }) {
  validateWitness(rows, { project, generation });

  const counts = new Map();
  for (const row of rows) {
    const key = JSON.stringify([row.id, row.subjectReference]);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  const groupRows = [...counts]
    .map(([key, count]) => [...JSON.parse(key), String(count)])
    .sort(compareRows);

  return {
    project,
    generation,
    groupRows,
    sharedSubject,
    leftOnlySubject,
    sharedSubjectRows: groupRows.filter(([, subject]) => subject === sharedSubject),
    leftOnlySubjectRows: groupRows.filter(([, subject]) => subject === leftOnlySubject),
  };
}
