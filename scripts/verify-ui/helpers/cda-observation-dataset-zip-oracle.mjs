import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';
import { inspectDatasetArtifactZip } from './viewer-output-dataset-zip-oracle.mjs';

export const CDA_OBSERVATION_DATASET_ZIP_PROJECT = 'loom_dev_cda_fhir';
export const CDA_OBSERVATION_DATASET_ZIP_GENERATION = 'cda-fhir-v1';
export const CDA_OBSERVATION_DATASET_ZIP_SYSTEM = 'https://cda.readthedocs.io';
export const CDA_OBSERVATION_DATASET_ZIP_CODE = 'specimen_type';
export const CDA_OBSERVATION_DATASET_ZIP_WITNESSES = Object.freeze([
  Object.freeze({ id: '00004888-0740-540e-bc75-68b5192fbf5a', status: 'final', valueString: 'aliquot' }),
  Object.freeze({ id: '00008c9a-64db-55f9-96bf-7a0db4e32551', status: 'final', valueString: 'sample' }),
  Object.freeze({ id: '0000961c-775c-52cf-b25c-c71ee28c423f', status: 'final', valueString: 'aliquot' }),
]);

const queryValue = value => JSON.stringify(value);
const requiredText = (value, name) => {
  assert.equal(typeof value, 'string', `${name} must be a string`);
  assert.notEqual(value.trim(), '', `${name} must be non-empty`);
};

const witnessIDs = CDA_OBSERVATION_DATASET_ZIP_WITNESSES.map(row => row.id);

export const cdaObservationDatasetZipWitnessQuery = ({ project, generation }) => {
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  return `FOR r IN Observation FILTER r.id IN ${queryValue(witnessIDs)} AND r.project == ${queryValue(project)} AND r.dataset_generation == ${queryValue(generation)} AND r.payload.resourceType == "Observation" AND r.payload.id == r.id SORT r.id RETURN {_id:r._id,id:r.id,payloadId:r.payload.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,status:r.payload.status,components:r.payload.component}`;
};

export const cdaObservationDatasetZipRereadQuery = cdaObservationDatasetZipWitnessQuery;

const normalizeRawRows = (rows, { project, generation }) => {
  assert(Array.isArray(rows), 'Exact CDA Observation witness query must return raw rows');
  assert(rows.every(row => row?.project === project && row?.generation === generation &&
    row?.resourceType === 'Observation' && typeof row?._id === 'string' && row._id.length > 0 &&
    typeof row?.id === 'string' && row.id.trim().length > 0 && row?.payloadId === row.id &&
    typeof row?.status === 'string' && row.status.trim().length > 0 && Array.isArray(row?.components)),
  'Exact CDA Observation witness query contains an out-of-scope record or invalid source identity');
  assert.equal(new Set(rows.map(row => row._id)).size, rows.length,
    'Exact CDA Observation witness query repeated an Arango document key');
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length,
    'Exact CDA Observation witness query repeated a FHIR ID');
  return [...rows].sort((left, right) => left.id.localeCompare(right.id));
};

const rawRow = row => ({
  _id: row._id,
  id: row.id,
  payloadId: row.payloadId,
  project: row.project,
  generation: row.generation,
  resourceType: row.resourceType,
  status: row.status,
  components: structuredClone(row.components),
});

const componentWitnessFor = row => {
  const matches = [];
  row.components.forEach((owner, ownerOrdinal) => {
    const codingMatches = owner?.code?.coding?.filter(coding =>
      coding?.system === CDA_OBSERVATION_DATASET_ZIP_SYSTEM &&
      coding?.code === CDA_OBSERVATION_DATASET_ZIP_CODE) ?? [];
    if (codingMatches.length) matches.push({ owner, ownerOrdinal, codingMatches });
  });
  assert.equal(matches.length, 1,
    `Observation ${row.id} must contain exactly one component owner for the selected specimen_type coding`);
  const [match] = matches;
  assert.equal(match.codingMatches.length, 1,
    `Observation ${row.id} selected component must have exactly one matching specimen_type coding`);
  assert.equal(typeof match.owner.valueString, 'string',
    `Observation ${row.id} specimen_type owner must retain its raw valueString choice`);
  const valueArms = Object.keys(match.owner).filter(key => /^value[A-Z]/.test(key) && match.owner[key] != null);
  assert.deepEqual(valueArms, ['valueString'],
    `Observation ${row.id} specimen_type owner must have exactly the valueString choice arm`);
  return match;
};

const ownerRecordFor = (row, match) => ({
  source: {
    resourceType: 'Observation',
    resourceId: row.id,
    ownerPath: 'component[]',
    ownerOrdinal: match.ownerOrdinal,
  },
  codings: structuredClone(match.codingMatches),
  choiceArm: 'valueString',
  logicalType: 'string',
  value: match.owner.valueString,
  values: [match.owner.valueString],
  unit: null,
  status: 'VALUE',
  owner: structuredClone(match.owner),
});

export function prepareCdaObservationDatasetZipOracle(rawRows, { project, generation }) {
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  assert.equal(project, CDA_OBSERVATION_DATASET_ZIP_PROJECT,
    'CDA Observation ZIP witnesses must use the pinned project');
  assert.equal(generation, CDA_OBSERVATION_DATASET_ZIP_GENERATION,
    'CDA Observation ZIP witnesses must use the pinned generation');
  const rows = normalizeRawRows(rawRows, { project, generation });
  assert.deepEqual(rows.map(row => row.id), [...witnessIDs].sort(),
    'Exact CDA Observation lookup must return the complete three-ID witness set');
  const expectedSource = new Map(CDA_OBSERVATION_DATASET_ZIP_WITNESSES.map(row => [row.id, row]));
  const selected = rows.map(rawRow);
  const records = selected.map(row => {
    const expected = expectedSource.get(row.id);
    assert(expected, `Observation ${row.id} is not an approved raw witness`);
    assert.equal(row.status, expected.status, `Observation ${row.id} status differs from the retained raw witness`);
    const match = componentWitnessFor(row);
    assert.equal(match.owner.valueString, expected.valueString,
      `Observation ${row.id} selected specimen_type value differs from the retained raw witness`);
    return { row, ownerRecord: ownerRecordFor(row, match) };
  });
  const observationIDs = selected.map(row => row.id);
  return {
    project,
    generation,
    sourceScope: 'three exact existing Observation FHIR IDs in one pinned CDA project/generation',
    selected,
    observationIDs,
    references: observationIDs.map(id => ({ project, generation, resourceType: 'Observation', id })),
    rows: records.map(({ row, ownerRecord }) => ({
      observation_id: row.id,
      status: row.status,
      component_owner_records: [ownerRecord],
    })),
  };
}

export function assertCdaObservationDatasetZipReread(actualRows, oracle) {
  assert(Array.isArray(actualRows), 'Exact CDA Observation reread must return raw rows');
  assert(oracle && Array.isArray(oracle.selected) && oracle.selected.length === witnessIDs.length,
    'CDA Observation reread requires the exact independent raw witnesses');
  const actual = normalizeRawRows(actualRows, oracle);
  const expected = [...oracle.selected].sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(actual.map(rawRow), expected.map(rawRow),
    'Exact selected CDA Observation reread differs in IDs, statuses, or complete ordered component objects');
  return { exact: true, count: actual.length, project: oracle.project, generation: oracle.generation };
}

export function cdaObservationDatasetZipExpectedRows(oracle, columnNames) {
  assert(oracle && Array.isArray(oracle.rows) && oracle.rows.length === witnessIDs.length,
    'CDA Observation ZIP expected rows require the exact raw-owner oracle');
  for (const name of ['id', 'status', 'componentOwnerRecords']) {
    requiredText(columnNames?.[name], `columnNames.${name}`);
  }
  return oracle.rows.map(row => ({
    [columnNames.id]: row.observation_id,
    [columnNames.status]: row.status,
    [columnNames.componentOwnerRecords]: structuredClone(row.component_owner_records),
  }));
}

export function cdaObservationDatasetZipPreviewRowsMatch(actualRows, expectedRows, { idColumn } = {}) {
  if (!Array.isArray(actualRows) || !Array.isArray(expectedRows) || expectedRows.length === 0 ||
      actualRows.length !== expectedRows.length ||
      typeof idColumn !== 'string' || idColumn.trim() === '') return false;
  const expectedKeys = Object.keys(expectedRows[0] ?? {}).sort();
  if (!expectedKeys.includes(idColumn) || expectedRows.some(row =>
    !row || typeof row !== 'object' || Array.isArray(row) ||
    !isDeepStrictEqual(Object.keys(row).sort(), expectedKeys))) return false;

  const rowIdentityKeys = [];
  const publicRows = [];
  for (const row of actualRows) {
    if (!row || typeof row !== 'object' || Array.isArray(row) ||
        !isDeepStrictEqual(Object.keys(row).sort(), [...expectedKeys, '__loom_row_id'].sort()) ||
        typeof row.__loom_row_id !== 'string' || row.__loom_row_id.trim() === '') return false;
    rowIdentityKeys.push(row.__loom_row_id);
    const { __loom_row_id: _internalRowId, ...publicColumns } = row;
    publicRows.push(publicColumns);
  }
  if (new Set(rowIdentityKeys).size !== rowIdentityKeys.length) return false;

  const byId = rows => [...rows].sort((left, right) => String(left[idColumn]).localeCompare(String(right[idColumn])));
  return isDeepStrictEqual(byId(publicRows), byId(expectedRows));
}

export function inspectCdaObservationDatasetZip(archivePath, {
  expectedIdentity,
  expectedExplorerId,
  oracle,
  columns,
  expectedArchiveSHA256,
} = {}) {
  assert(oracle && Array.isArray(oracle.rows) && oracle.rows.length === witnessIDs.length,
    'CDA Observation ZIP inspection requires the exact independent raw-owner oracle');
  for (const key of ['id', 'status', 'componentOwnerRecords']) {
    assert(columns?.[key]?.authored && columns?.[key]?.preview,
      `CDA Observation ZIP schema requires the exact saved column and compiled Preview for ${key}`);
    if (key !== 'componentOwnerRecords') {
      assert(columns?.[key]?.candidate,
        `CDA Observation ZIP schema requires the exact candidate for ${key}`);
    }
  }
  const columnNames = {
    id: columns.id.authored.column,
    status: columns.status.authored.column,
    componentOwnerRecords: columns.componentOwnerRecords.authored.column,
  };
  const sourceLabel = source => {
    requiredText(source?.authored?.label, 'authored owner-record label');
    return source.authored.label;
  };
  const expectedRows = cdaObservationDatasetZipExpectedRows(oracle, columnNames);
  const inspected = inspectDatasetArtifactZip(archivePath, {
    expectedIdentity, expectedExplorerId, expectedRows, expectedArchiveSHA256,
  });
  const { artifact } = inspected;
  assert.equal(artifact.manifest.format, 'JSONL', 'CDA Observation owner-record ZIP must use typed JSON Lines');
  assert.equal(artifact.manifest.arrayEncoding, 'native', 'CDA Observation ZIP must preserve native JSON arrays');
  assert.equal(artifact.schema.arrayEncoding, 'native', 'CDA Observation schema must declare native JSON arrays');
  assert.equal(artifact.manifest.rows, oracle.rows.length, 'CDA ZIP must declare the exact selected Observation count');
  assert.equal(artifact.manifest.features, 3, 'CDA Observation ZIP must declare ID, status, and component owners');
  assert.equal(artifact.manifest.descriptor.rowMultiplication, 'none', 'CDA Observation ZIP must preserve Observation row grain');
  assert.equal(artifact.manifest.descriptor.rowIdentity.sourceResourceType, 'Observation');
  assert.equal(artifact.manifest.descriptor.rowIdentity.sourceIdColumn, columns.id.authored.column);
  assert.equal(artifact.schema.columns.length, 3);
  const expectedSchema = [
    { key: 'id', path: 'id', projection: 'VALUE', label: 'Observation ID', logicalType: 'string', shape: 'scalar', repeated: false },
    { key: 'status', path: 'status', projection: 'VALUE', label: 'Observation Status', logicalType: 'string', shape: 'scalar', repeated: false },
    { key: 'componentOwnerRecords', path: 'component[].valueString', projection: 'OWNER_RECORDS', label: sourceLabel(columns.componentOwnerRecords),
      logicalType: 'object', shape: 'record_list', repeated: true },
  ];
  expectedSchema.forEach((expected, index) => {
    const source = columns[expected.key];
    const actual = artifact.schema.columns[index];
    assert.equal(actual.name, source.authored.column);
    assert.equal(actual.outputKey, source.authored.column);
    assert.equal(actual.label, expected.label);
    assert.equal(actual.logicalType, expected.logicalType);
    assert.equal(actual.shape, expected.shape);
    assert.equal(Boolean(actual.repeated), expected.repeated);
    assert.equal(actual.occurrenceId, 'base');
    assert.equal(actual.construction, expected.projection);
    assert.equal(actual.reductionPolicy, expected.projection);
    assert.equal(actual.sourceResourceType, 'Observation');
    assert.equal(actual.sourcePath, expected.path);
    assert.equal(typeof actual.nullable, 'boolean');
    assert.equal(actual.nullable, source.preview.nullable);
    if (source.candidate) assert.equal(actual.candidateId, source.candidate.candidateId);
    assert.equal(typeof actual.emissionId, 'string');
    assert.notEqual(actual.emissionId.trim(), '');
    if (expected.key === 'componentOwnerRecords') {
      assert.equal(actual.choiceArm, 'valueString');
      assert.notEqual(actual.candidateId, '', 'Owner-record schema must retain its derived source identity');
    }
  });
  assert.deepEqual(artifact.rows.map(row => row.values).sort((left, right) =>
    String(left[columnNames.id]).localeCompare(String(right[columnNames.id]))),
  expectedRows.sort((left, right) => String(left[columnNames.id]).localeCompare(String(right[columnNames.id]))),
  'CDA Observation ZIP rows must preserve exact typed IDs, statuses, and full component owner objects');
  return inspected;
}
