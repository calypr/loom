import assert from 'node:assert/strict';

export const MAX_CDA_UPSTREAM_APPEND_SCAN = 2_000;
export const CDA_UPSTREAM_APPEND_RESOURCES = Object.freeze([
  { resourceType: 'Patient', fieldPath: 'id', outputKey: 'id' },
  { resourceType: 'Observation', fieldPath: 'status', outputKey: 'status' },
]);
export const CDA_UPSTREAM_APPEND_SOURCES = Object.freeze([
  { sourceKey: 'observation-left', title: 'Observation final-status source A', resourceType: 'Observation', fieldPath: 'status', outputKey: 'status' },
  { sourceKey: 'observation-right', title: 'Observation final-status source B', resourceType: 'Observation', fieldPath: 'status', outputKey: 'status' },
  { sourceKey: 'patient-id', title: 'Patient FHIR ID source', resourceType: 'Patient', fieldPath: 'id', outputKey: 'id' },
]);

const queryString = value => JSON.stringify(value);

export const cdaUpstreamAppendScanQuery = ({ project, generation, resourceType, fieldPath }) => {
  assert(typeof project === 'string' && project.length > 0);
  assert(typeof generation === 'string' && generation.length > 0);
  assert(CDA_UPSTREAM_APPEND_RESOURCES.some(resource =>
    resource.resourceType === resourceType && resource.fieldPath === fieldPath));
  return `FOR r IN ${resourceType} FILTER r.project == ${queryString(project)} AND r.dataset_generation == ${queryString(generation)} AND r.payload.resourceType == ${queryString(resourceType)} AND IS_STRING(r.id) AND LENGTH(TRIM(r.id)) > 0 SORT r._id LIMIT ${MAX_CDA_UPSTREAM_APPEND_SCAN} RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,fieldPresent:HAS(r.payload,${queryString(fieldPath)}),fieldValue:r.payload[${queryString(fieldPath)}]}`;
};

export const cdaUpstreamAppendRereadQuery = ({ project, generation, resourceType, fieldPath, documentIDs }) => {
  assert(Array.isArray(documentIDs) && documentIDs.length > 0 && documentIDs.length <= 2);
  assert(documentIDs.every(id => typeof id === 'string' && id.length > 0));
  assert(CDA_UPSTREAM_APPEND_RESOURCES.some(resource =>
    resource.resourceType === resourceType && resource.fieldPath === fieldPath));
  return `FOR r IN ${resourceType} FILTER r._id IN ${queryString(documentIDs)} AND r.project == ${queryString(project)} AND r.dataset_generation == ${queryString(generation)} AND r.payload.resourceType == ${queryString(resourceType)} SORT r._id RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,fieldPresent:HAS(r.payload,${queryString(fieldPath)}),fieldValue:r.payload[${queryString(fieldPath)}]}`;
};

export class CdaUpstreamAppendWitnessUnavailable extends Error {
  constructor(message, evidence) {
    super(message);
    this.name = 'CdaUpstreamAppendWitnessUnavailable';
    this.evidence = evidence;
  }
}

const validateScan = (rows, { project, generation, resourceType }) => {
  assert(Array.isArray(rows), `Bounded ${resourceType} scan must return raw rows`);
  assert(rows.length <= MAX_CDA_UPSTREAM_APPEND_SCAN,
    `Bounded ${resourceType} scan exceeded its ${MAX_CDA_UPSTREAM_APPEND_SCAN}-row limit`);
  assert(rows.every(row => row?.project === project && row?.generation === generation &&
    row?.resourceType === resourceType && typeof row?._id === 'string' && row._id.length > 0 &&
    typeof row?.id === 'string' && row.id.trim().length > 0),
  `Bounded ${resourceType} scan contains an out-of-scope record or an invalid FHIR id`);
  assert.equal(new Set(rows.map(row => row._id)).size, rows.length,
    `Bounded ${resourceType} scan repeated an Arango document key`);
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length,
    `Bounded ${resourceType} scan repeated a FHIR id`);
  return [...rows].sort((left, right) => left._id.localeCompare(right._id));
};

const validFieldValue = row => row.fieldPresent === true && typeof row.fieldValue === 'string' && row.fieldValue.trim().length > 0;

const categoryPairs = rows => {
  const groups = new Map();
  for (const row of rows.filter(validFieldValue)) {
    const group = groups.get(row.fieldValue) ?? [];
    group.push(row);
    groups.set(row.fieldValue, group);
  }
  return groups;
};

const countRows = rows => {
  const counts = new Map();
  for (const row of rows) counts.set(row.key, (counts.get(row.key) ?? 0) + 1);
  return [...counts.entries()].sort(([left], [right]) => left.localeCompare(right));
};

const rawSelection = rows => rows.map(row => ({
  _id: row._id,
  id: row.id,
  project: row.project,
  generation: row.generation,
  resourceType: row.resourceType,
  fieldPresent: row.fieldPresent,
  fieldValue: row.fieldValue ?? null,
  key: row.fieldValue,
}));

const groupSourceRows = rows => countRows(rows.map(row => ({ key: row.key })));

export function prepareCdaUpstreamAppendOracle(scans, { project, generation }) {
  assert(typeof project === 'string' && project.length > 0, 'CDA upstream APPEND needs an exact project');
  assert(typeof generation === 'string' && generation.length > 0, 'CDA upstream APPEND needs an exact generation');
  const normalized = Object.fromEntries(CDA_UPSTREAM_APPEND_RESOURCES.map(({ resourceType, fieldPath, outputKey }) => {
    const rows = validateScan(scans?.[resourceType], { project, generation, resourceType });
    return [resourceType, { rows, fieldPath, outputKey, groups: categoryPairs(rows) }];
  }));
  const patientRows = normalized.Patient.rows.filter(row => validFieldValue(row) && row.fieldValue === row.id);
  const observationGroups = normalized.Observation.groups;
  const observationFinalRows = observationGroups.get('final') ?? [];
  const resources = [
    {
      resourceType: 'Patient', fieldPath: 'id', returnedRows: normalized.Patient.rows.length,
      eligibleRows: patientRows.length,
      excludedMissingNullNonStringOrMismatchedFHIRID: normalized.Patient.rows.length - patientRows.length,
      uniqueFHIRIDs: new Set(patientRows.map(row => row.id)).size,
    },
    {
      resourceType: 'Observation', fieldPath: 'status', returnedRows: normalized.Observation.rows.length,
      eligibleRows: normalized.Observation.rows.filter(validFieldValue).length,
      excludedMissingNullOrNonString: normalized.Observation.rows.length - normalized.Observation.rows.filter(validFieldValue).length,
      categoriesWithFourRecords: [...observationGroups].filter(([, members]) => members.length >= 4)
        .map(([category]) => category).sort(),
      selectedStatus: 'final', selectedFinalStatusRows: observationFinalRows.length,
    },
  ];
  const failures = [];
  if (patientRows.length < 2) failures.push('the bounded Patient.id scan has fewer than two eligible unique FHIR IDs');
  if (observationFinalRows.length < 4) failures.push('the bounded Observation.status scan has fewer than four final rows for two disjoint pairs');
  if (failures.length) {
    const evidence = {
      project,
      generation,
      scanLimitPerResource: MAX_CDA_UPSTREAM_APPEND_SCAN,
      scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
      requiredSourceWitnesses: [
        { sourceKey: 'observation-left', resourceType: 'Observation', fieldPath: 'status', category: 'final', memberCount: 2 },
        { sourceKey: 'observation-right', resourceType: 'Observation', fieldPath: 'status', category: 'final', memberCount: 2, disjointFrom: 'observation-left' },
        { sourceKey: 'patient-id', resourceType: 'Patient', fieldPath: 'id', memberCount: 2, groupRows: 2 },
      ],
      resources,
      reason: failures,
    };
    throw new CdaUpstreamAppendWitnessUnavailable(
      `The bounded ${MAX_CDA_UPSTREAM_APPEND_SCAN}-row-per-resource CDA scan cannot form the required Patient.id and disjoint Observation final-status witnesses: ${failures.join('; ')}`,
      evidence,
    );
  }

  const selected = {
    'observation-left': observationFinalRows.slice(0, 2),
    'observation-right': observationFinalRows.slice(2, 4),
    'patient-id': patientRows.slice(0, 2),
  };
  assert.equal(new Set(selected['observation-left'].concat(selected['observation-right']).map(row => row._id)).size, 4,
    'The two Observation source populations must be disjoint');
  const sources = Object.fromEntries(Object.entries(selected).map(([sourceKey, rows]) => [sourceKey, rawSelection(rows)]));
  const grouped = Object.fromEntries(Object.entries(sources).map(([sourceKey, rows]) => [sourceKey, groupSourceRows(rows)]));
  const deriveRows = offset => grouped['patient-id'].map(([id, count]) => [id, count, count + offset]);
  const appendRows = offset => [
    ...grouped['observation-left'],
    ...grouped['observation-right'],
    ...deriveRows(offset).map(([id, , derived]) => [id, derived]),
  ].map(([category, count]) => [String(category), String(count)]);
  const exactExpected = Object.fromEntries(Object.entries(sources).map(([sourceKey, rows]) => [
    sourceKey,
    rows.map(row => ({ project: row.project, generation: row.generation, resourceType: row.resourceType,
      id: row.id, _id: row._id, fieldValue: row.fieldValue, fieldPresent: row.fieldPresent })),
  ]));
  const duplicateCount = appendRows(1).filter(([category]) => category === 'final').length;
  assert.equal(duplicateCount, 2, 'Raw APPEND oracle must preserve the duplicate final-status rows from disjoint Observation groups');
  return {
    project,
    generation,
    scanLimitPerResource: MAX_CDA_UPSTREAM_APPEND_SCAN,
    scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
    resources,
    selected,
    sources,
    exactExpected,
    categories: { observationStatus: 'final', patientFHIRIDs: patientRows.slice(0, 2).map(row => row.id) },
    grouped,
    patientDerived: { plusOne: deriveRows(1), plusTwo: deriveRows(2) },
    append: { plusOne: appendRows(1), plusTwo: appendRows(2), duplicateStatusCount: duplicateCount },
  };
}

export function assertCdaUpstreamAppendReread(actualRows, expectedRows, { project, generation, resourceType }) {
  assert(Array.isArray(actualRows) && Array.isArray(expectedRows));
  const normalize = rows => [...rows].map(row => ({
    _id: row._id,
    id: row.id,
    project: row.project,
    generation: row.generation,
    resourceType: row.resourceType,
    fieldPresent: row.fieldPresent === true,
    fieldValue: row.fieldValue ?? null,
  })).sort((left, right) => left._id.localeCompare(right._id));
  const actual = normalize(actualRows);
  const expected = normalize(expectedRows);
  assert(actual.every(row => row.project === project && row.generation === generation && row.resourceType === resourceType),
    `Exact ${resourceType} reread escaped its project, generation, or resource type`);
  assert.deepEqual(actual, expected, `Exact selected ${resourceType} raw reread differs from the independent witness`);
  return { count: actual.length, exact: true, resourceType, project, generation };
}
