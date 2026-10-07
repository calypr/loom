import assert from 'node:assert/strict';

export const MAX_CDA_PUBLISHED_APPEND_SCAN = 2_000;
export const CDA_PUBLISHED_APPEND_RESOURCES = Object.freeze([
  { resourceType: 'Observation', valueField: 'status' },
  { resourceType: 'Patient', valueField: 'id' },
]);
export const CDA_PUBLISHED_APPEND_SOURCES = Object.freeze([
  { sourceKey: 'observation-left', title: 'Observation source A', resourceType: 'Observation', valueField: 'status' },
  { sourceKey: 'observation-right', title: 'Observation source B', resourceType: 'Observation', valueField: 'status' },
  { sourceKey: 'patient', title: 'Patient ID source', resourceType: 'Patient', valueField: 'id' },
]);

const q = value => JSON.stringify(value);
const resourceFor = resourceType => CDA_PUBLISHED_APPEND_RESOURCES.find(item => item.resourceType === resourceType);

export const cdaPublishedAppendScanQuery = ({ project, generation, resourceType }) => {
  assert(typeof project === 'string' && project.length > 0);
  assert(typeof generation === 'string' && generation.length > 0);
  const resource = resourceFor(resourceType);
  assert(resource, `Unsupported published CDA APPEND resource ${resourceType}`);
  const field = resource.valueField;
  return `FOR r IN ${resourceType} FILTER r.project == ${q(project)} AND r.dataset_generation == ${q(generation)} AND r.payload.resourceType == ${q(resourceType)} AND IS_STRING(r.id) AND LENGTH(TRIM(r.id)) > 0 SORT r._id LIMIT ${MAX_CDA_PUBLISHED_APPEND_SCAN} RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,fieldPresent:HAS(r.payload,${q(field)}),fieldValue:r.payload[${q(field)}]}`;
};

export const cdaPublishedAppendRereadQuery = ({ project, generation, resourceType, documentIDs }) => {
  assert(typeof project === 'string' && project.length > 0);
  assert(typeof generation === 'string' && generation.length > 0);
  assert(resourceFor(resourceType), `Unsupported published CDA APPEND resource ${resourceType}`);
  assert(Array.isArray(documentIDs) && documentIDs.length > 0 && documentIDs.length <= 4);
  assert(documentIDs.every(id => typeof id === 'string' && id.length > 0));
  const field = resourceFor(resourceType).valueField;
  return `FOR r IN ${resourceType} FILTER r._id IN ${q(documentIDs)} AND r.project == ${q(project)} AND r.dataset_generation == ${q(generation)} AND r.payload.resourceType == ${q(resourceType)} SORT r._id RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,fieldPresent:HAS(r.payload,${q(field)}),fieldValue:r.payload[${q(field)}]}`;
};

export class CdaPublishedAppendWitnessUnavailable extends Error {
  constructor(message, evidence) {
    super(message);
    this.name = 'CdaPublishedAppendWitnessUnavailable';
    this.evidence = evidence;
  }
}

const validateRows = (rows, { project, generation, resourceType }) => {
  assert(Array.isArray(rows), `Bounded ${resourceType} scan must return raw rows`);
  assert(rows.length <= MAX_CDA_PUBLISHED_APPEND_SCAN,
    `Bounded ${resourceType} scan exceeded its ${MAX_CDA_PUBLISHED_APPEND_SCAN}-row limit`);
  assert(rows.every(row => row?.project === project && row?.generation === generation &&
    row?.resourceType === resourceType && typeof row?._id === 'string' && row._id.length > 0 &&
    typeof row?.id === 'string' && row.id.trim().length > 0),
  `Bounded ${resourceType} scan contains an out-of-scope record or invalid FHIR id`);
  assert.equal(new Set(rows.map(row => row._id)).size, rows.length,
    `Bounded ${resourceType} scan repeated an Arango document key`);
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length,
    `Bounded ${resourceType} scan repeated a FHIR id`);
  return [...rows].sort((left, right) => left._id.localeCompare(right._id));
};

const hasStringValue = row => row.fieldPresent === true && typeof row.fieldValue === 'string';
const rawRow = row => ({
  _id: row._id,
  id: row.id,
  project: row.project,
  generation: row.generation,
  resourceType: row.resourceType,
  fieldPresent: row.fieldPresent === true,
  fieldValue: row.fieldValue ?? null,
});

export function prepareCdaPublishedAppendOracle(scans, { project, generation }) {
  assert(typeof project === 'string' && project.length > 0, 'Published CDA APPEND needs an exact project');
  assert(typeof generation === 'string' && generation.length > 0, 'Published CDA APPEND needs an exact generation');
  const rows = Object.fromEntries(CDA_PUBLISHED_APPEND_RESOURCES.map(({ resourceType }) => [resourceType,
    validateRows(scans?.[resourceType], { project, generation, resourceType })]));
  const observations = rows.Observation.filter(row => hasStringValue(row) && row.fieldValue === 'final');
  const patients = rows.Patient.filter(row => hasStringValue(row) && row.fieldValue === row.id);
  const resources = [
    { resourceType: 'Observation', fieldPath: 'status', returnedRows: rows.Observation.length,
      selectedFinalRows: observations.length },
    { resourceType: 'Patient', fieldPath: 'id', returnedRows: rows.Patient.length,
      eligibleFHIRIDRows: patients.length, mismatchedOrMissingPayloadIDs: rows.Patient.length - patients.length },
  ];
  const failures = [];
  if (observations.length < 4) failures.push('the bounded Observation.status scan has fewer than four final rows for two disjoint pairs');
  if (patients.length < 2) failures.push('the bounded Patient.id scan has fewer than two payload IDs matching their top-level FHIR IDs');
  if (failures.length) {
    const evidence = {
      project, generation, scanLimitPerResource: MAX_CDA_PUBLISHED_APPEND_SCAN,
      scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
      resources,
      requiredSourceWitnesses: [
        { sourceKey: 'observation-left', resourceType: 'Observation', fieldPath: 'status', value: 'final', memberCount: 2 },
        { sourceKey: 'observation-right', resourceType: 'Observation', fieldPath: 'status', value: 'final', memberCount: 2, disjointFrom: 'observation-left' },
        { sourceKey: 'patient', resourceType: 'Patient', fieldPath: 'id', memberCount: 2, requirePayloadIDMatchesFHIRID: true },
      ],
      reason: failures,
    };
    throw new CdaPublishedAppendWitnessUnavailable(
      `The bounded ${MAX_CDA_PUBLISHED_APPEND_SCAN}-row-per-resource CDA scan cannot form the required disjoint Observation and Patient populations: ${failures.join('; ')}`,
      evidence,
    );
  }

  const selected = {
    'observation-left': observations.slice(0, 2),
    'observation-right': observations.slice(2, 4),
    patient: patients.slice(0, 2),
  };
  const observationIDs = [...selected['observation-left'], ...selected['observation-right']].map(row => row._id);
  assert.equal(new Set(observationIDs).size, observationIDs.length, 'Observation source populations must be disjoint');
  const sources = Object.fromEntries(Object.entries(selected).map(([key, members]) => [key, members.map(rawRow)]));
  const sourceReferences = Object.fromEntries(Object.entries(sources).map(([sourceKey, members]) => [sourceKey,
    members.map(member => ({ project, generation, resourceType: member.resourceType, id: member.id }))]));
  const appendRows = [
    ...selected['observation-left'].map(row => [row.id, row.fieldValue]),
    ...selected['observation-right'].map(row => [row.id, row.fieldValue]),
    ...selected.patient.map(row => [row.id, null]),
  ];
  const duplicateFinalStatusCount = appendRows.filter(row => row[1] === 'final').length;
  assert.equal(duplicateFinalStatusCount, 4, 'APPEND must preserve all four selected final-status rows');
  const exactExpected = Object.fromEntries(Object.entries(sources).map(([sourceKey, members]) => [sourceKey, members]));
  return {
    project, generation, scanLimitPerResource: MAX_CDA_PUBLISHED_APPEND_SCAN,
    scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
    resources, selected, sources, sourceReferences, exactExpected,
    categories: { observationStatus: 'final', patientFHIRIDs: selected.patient.map(row => row.id) },
    append: { rows: appendRows, duplicateFinalStatusCount },
  };
}

export function assertCdaPublishedAppendReread(actualRows, expectedRows, { project, generation, resourceType }) {
  assert(Array.isArray(actualRows) && Array.isArray(expectedRows));
  const normalize = values => [...values].map(rawRow).sort((left, right) => left._id.localeCompare(right._id));
  const actual = normalize(actualRows);
  const expected = normalize(expectedRows);
  assert(actual.every(row => row.project === project && row.generation === generation && row.resourceType === resourceType),
    `Exact ${resourceType} reread escaped its project, generation, or resource type`);
  assert.deepEqual(actual, expected, `Exact selected ${resourceType} raw reread differs from the bounded witness`);
  return { count: actual.length, exact: true, resourceType, project, generation };
}
