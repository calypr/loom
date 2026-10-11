import assert from 'node:assert/strict';
import { inspectDatasetArtifactZip } from './viewer-output-dataset-zip-oracle.mjs';

export const MAX_CDA_PATIENT_DATASET_ZIP_SCAN = 2_000;

const queryValue = value => JSON.stringify(value);
const requiredText = (value, name) => {
  assert.equal(typeof value, 'string', `${name} must be a string`);
  assert.notEqual(value.trim(), '', `${name} must be non-empty`);
};

export const cdaPatientDatasetZipScanQuery = ({ project, generation }) => {
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  return `FOR r IN Patient FILTER r.project == ${queryValue(project)} AND r.dataset_generation == ${queryValue(generation)} AND r.payload.resourceType == "Patient" AND IS_STRING(r.id) AND LENGTH(TRIM(r.id)) > 0 SORT r._id LIMIT ${MAX_CDA_PATIENT_DATASET_ZIP_SCAN} RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,fieldPresent:HAS(r.payload,"id"),fieldValue:r.payload.id}`;
};

export const cdaPatientDatasetZipRereadQuery = ({ project, generation, documentIDs }) => {
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  assert(Array.isArray(documentIDs) && documentIDs.length === 2 &&
    documentIDs.every(id => typeof id === 'string' && id.length > 0),
  'Exact CDA Patient reread requires two selected Arango document keys');
  return `FOR r IN Patient FILTER r._id IN ${queryValue(documentIDs)} AND r.project == ${queryValue(project)} AND r.dataset_generation == ${queryValue(generation)} AND r.payload.resourceType == "Patient" SORT r._id RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,fieldPresent:HAS(r.payload,"id"),fieldValue:r.payload.id}`;
};

export class CdaPatientDatasetZipWitnessUnavailable extends Error {
  constructor(message, evidence) {
    super(message);
    this.name = 'CdaPatientDatasetZipWitnessUnavailable';
    this.evidence = evidence;
  }
}

const normalizeRawRows = (rows, { project, generation }) => {
  assert(Array.isArray(rows), 'Bounded CDA Patient scan must return raw rows');
  assert(rows.length <= MAX_CDA_PATIENT_DATASET_ZIP_SCAN,
    `Bounded CDA Patient scan exceeded its ${MAX_CDA_PATIENT_DATASET_ZIP_SCAN}-row limit`);
  assert(rows.every(row => row?.project === project && row?.generation === generation &&
    row?.resourceType === 'Patient' && typeof row?._id === 'string' && row._id.length > 0 &&
    typeof row?.id === 'string' && row.id.trim().length > 0 && typeof row?.fieldPresent === 'boolean'),
  'Bounded CDA Patient scan contains an out-of-scope record or invalid raw identity');
  assert.equal(new Set(rows.map(row => row._id)).size, rows.length,
    'Bounded CDA Patient scan repeated an Arango document key');
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length,
    'Bounded CDA Patient scan repeated a FHIR id');
  return [...rows].sort((left, right) => left._id.localeCompare(right._id));
};

const rawRow = row => ({
  _id: row._id,
  id: row.id,
  project: row.project,
  generation: row.generation,
  resourceType: row.resourceType,
  fieldPresent: row.fieldPresent === true,
  fieldValue: row.fieldValue ?? null,
});

export function prepareCdaPatientDatasetZipOracle(scanRows, { project, generation }) {
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  const rows = normalizeRawRows(scanRows, { project, generation });
  const matching = rows.filter(row => row.fieldPresent && row.fieldValue === row.id);
  if (matching.length < 2) {
    const evidence = {
      project,
      generation,
      scanLimitPerResource: MAX_CDA_PATIENT_DATASET_ZIP_SCAN,
      returnedRows: rows.length,
      matchingPayloadIDs: matching.length,
      requiredMatchingPayloadIDs: 2,
      rejectedRows: rows.length - matching.length,
    };
    throw new CdaPatientDatasetZipWitnessUnavailable(
      'The bounded real-CDA Patient scan cannot provide two payload Patient.id values that match their FHIR IDs.', evidence);
  }
  const selected = matching.slice(0, 2).map(rawRow);
  const patientIDs = selected.map(row => row.id);
  assert.equal(new Set(selected.map(row => row._id)).size, 2,
    'CDA Patient ZIP witnesses must come from two distinct raw documents');
  assert.equal(new Set(patientIDs).size, 2, 'CDA Patient ZIP witnesses must have distinct FHIR IDs');
  return {
    project,
    generation,
    scanLimitPerResource: MAX_CDA_PATIENT_DATASET_ZIP_SCAN,
    scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
    returnedRows: rows.length,
    selected,
    patientIDs,
    rows: patientIDs.map(patient_id => ({ patient_id })),
    references: patientIDs.map(id => ({ project, generation, resourceType: 'Patient', id })),
  };
}

export function assertCdaPatientDatasetZipReread(actualRows, oracle) {
  assert(Array.isArray(actualRows), 'Exact CDA Patient reread must return raw rows');
  assert(oracle && Array.isArray(oracle.selected) && oracle.selected.length === 2,
    'CDA Patient reread requires two independent raw witnesses');
  const actual = normalizeRawRows(actualRows, oracle);
  const expected = [...oracle.selected].sort((left, right) => left._id.localeCompare(right._id));
  assert.deepEqual(actual.map(rawRow), expected.map(rawRow),
    'Exact selected CDA Patient raw reread differs from the bounded witness');
  return { exact: true, count: actual.length, project: oracle.project, generation: oracle.generation };
}

export function bindCdaPatientDatasetZipPublication(oracle, {
  explorerId,
  outputId,
  revisionId,
  schemaDigest,
  columnName,
  candidate,
  compiledPreviewColumn,
}) {
  assert(oracle && Array.isArray(oracle.rows) && oracle.rows.length === 2,
    'CDA Patient ZIP publication requires the exact two-row raw oracle');
  for (const [name, value] of Object.entries({ explorerId, outputId, revisionId, schemaDigest, columnName })) {
    requiredText(value, name);
  }
  assert.match(schemaDigest, /^[a-f0-9]{64}$/i, 'CDA Patient ZIP schemaDigest must be a SHA-256 hex digest');
  assert.equal(candidate?.fieldPath, 'id', 'CDA Patient ZIP column candidate must select Patient.id');
  assert.equal(candidate?.logicalType, 'string', 'CDA Patient ZIP column must have scalar string type');
  assert.equal(candidate?.repeated, false, 'CDA Patient ZIP Patient.id column must not repeat');
  assert(['required_one', 'optional_one'].includes(candidate?.cardinality),
    'CDA Patient ZIP Patient.id column must have one-valued cardinality');
  assert(compiledPreviewColumn && typeof compiledPreviewColumn === 'object',
    'CDA Patient ZIP schema binding requires the exact compiled Preview column');
  assert.equal(compiledPreviewColumn.column, columnName,
    'CDA compiled Preview column must belong to the authored Patient ID output');
  assert.equal(compiledPreviewColumn.label, 'Patient ID',
    'CDA compiled Preview column must retain the authored Patient ID label');
  assert.equal(compiledPreviewColumn.logicalType, 'string',
    'CDA compiled Preview column must retain scalar string type');
  assert.equal(compiledPreviewColumn.shape, 'scalar',
    'CDA compiled Preview column must have scalar shape');
  assert.equal(typeof compiledPreviewColumn.nullable, 'boolean',
    'CDA compiled Preview column must declare its compiled nullability');

  return {
    ...oracle,
    explorerId,
    identity: {
      project: oracle.project,
      datasetGeneration: oracle.generation,
      outputId,
      revisionId,
      schemaDigest,
    },
    expectedColumn: {
      name: columnName,
      outputKey: columnName,
      label: 'Patient ID',
      logicalType: 'string',
      shape: 'scalar',
      nullable: compiledPreviewColumn.nullable,
      repeated: false,
      occurrenceId: 'base',
      construction: 'VALUE',
      reductionPolicy: 'VALUE',
      sourceResourceType: 'Patient',
      sourcePath: 'id',
    },
    expectedArchiveRows: oracle.patientIDs.map(patient_id => ({ [columnName]: patient_id })),
  };
}

export function inspectCdaPatientDatasetZip(archivePath, oracle, { expectedArchiveSHA256 } = {}) {
  assert(oracle?.identity && oracle?.expectedColumn && Array.isArray(oracle.rows),
    'CDA Patient ZIP inspection requires the independent publication oracle');
  const inspected = inspectDatasetArtifactZip(archivePath, {
    expectedIdentity: oracle.identity,
    expectedExplorerId: oracle.explorerId,
    expectedRows: oracle.expectedArchiveRows,
    expectedArchiveSHA256,
  });
  const { artifact } = inspected;
  assert.equal(artifact.manifest.rows, oracle.rows.length,
    'CDA Patient ZIP manifest must declare the exact raw Patient count');
  assert.equal(artifact.manifest.features, 1, 'CDA Patient ZIP must contain one authored Patient ID feature');
  assert.equal(artifact.manifest.descriptor.rowGrain, 'patient',
    'CDA Patient ZIP descriptor must retain Patient row grain');
  assert.equal(artifact.manifest.descriptor.rowMultiplication, 'none',
    'CDA Patient ZIP descriptor must not multiply Patient rows');
  assert.equal(artifact.manifest.descriptor.rowIdentity.sourceResourceType, 'Patient',
    'CDA Patient ZIP row identity must originate from Patient');
  assert.equal(artifact.manifest.descriptor.rowIdentity.sourceIdColumn, oracle.expectedColumn.name,
    'CDA Patient ZIP row identity must bind the authored Patient ID column');
  assert.equal(artifact.schema.columns.length, 1, 'CDA Patient ZIP schema must contain one column');
  const [column] = artifact.schema.columns;
  for (const [field, expected] of Object.entries(oracle.expectedColumn)) {
    const omittedFalse = expected === false && !Object.hasOwn(column, field);
    const actual = omittedFalse ? false : column[field];
    assert.equal(actual, expected, `CDA Patient ZIP schema ${field} must match the authored Patient.id output`);
  }
  assert.equal(typeof column.emissionId, 'string', 'CDA Patient ZIP schema must retain an emission identity');
  assert.notEqual(column.emissionId.trim(), '', 'CDA Patient ZIP emission identity must be non-empty');
  assert.equal(typeof column.candidateId, 'string', 'CDA Patient ZIP schema must retain a candidate identity');
  assert.notEqual(column.candidateId.trim(), '', 'CDA Patient ZIP candidate identity must be non-empty');
  const actualPatientIDs = artifact.rows.map(row => row.values[column.name]);
  assert.deepEqual([...actualPatientIDs].sort(), [...oracle.patientIDs].sort(),
    'CDA Patient ZIP data member must preserve the exact independent raw Patient IDs');
  return inspected;
}
