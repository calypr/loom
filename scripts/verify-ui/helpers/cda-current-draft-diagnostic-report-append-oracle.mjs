import assert from 'node:assert/strict';

export const MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN = 2_000;
export const CDA_DIAGNOSTIC_REPORT_APPEND_RESOURCE = Object.freeze({
  resourceType: 'DiagnosticReport',
  fieldPath: 'status',
});
export const CDA_DIAGNOSTIC_REPORT_APPEND_SOURCES = Object.freeze([
  { sourceKey: 'diagnostic-report-left', title: 'DiagnosticReport status source A' },
  { sourceKey: 'diagnostic-report-right', title: 'DiagnosticReport status source B' },
]);

const queryString = value => JSON.stringify(value);

export function cdaDiagnosticReportAppendScanQuery({ project, generation }) {
  assert(typeof project === 'string' && project.length > 0);
  assert(typeof generation === 'string' && generation.length > 0);
  const { resourceType, fieldPath } = CDA_DIAGNOSTIC_REPORT_APPEND_RESOURCE;
  return `FOR r IN ${resourceType} FILTER r.project == ${queryString(project)} AND r.dataset_generation == ${queryString(generation)} AND r.payload.resourceType == ${queryString(resourceType)} AND IS_STRING(r.id) AND LENGTH(TRIM(r.id)) > 0 SORT r._id LIMIT ${MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN} RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,fieldPresent:HAS(r.payload,${queryString(fieldPath)}),fieldValue:r.payload[${queryString(fieldPath)}]}`;
}

export function cdaDiagnosticReportAppendRereadQuery({ project, generation, documentIDs }) {
  assert(typeof project === 'string' && project.length > 0);
  assert(typeof generation === 'string' && generation.length > 0);
  assert(Array.isArray(documentIDs) && documentIDs.length > 0 && documentIDs.length <= 4);
  assert(documentIDs.every(id => typeof id === 'string' && id.length > 0));
  assert.equal(new Set(documentIDs).size, documentIDs.length, 'Exact reread keys must be unique');
  const { resourceType, fieldPath } = CDA_DIAGNOSTIC_REPORT_APPEND_RESOURCE;
  return `FOR r IN ${resourceType} FILTER r._id IN ${queryString(documentIDs)} AND r.project == ${queryString(project)} AND r.dataset_generation == ${queryString(generation)} AND r.payload.resourceType == ${queryString(resourceType)} SORT r._id RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,fieldPresent:HAS(r.payload,${queryString(fieldPath)}),fieldValue:r.payload[${queryString(fieldPath)}]}`;
}

export class CdaDiagnosticReportAppendWitnessUnavailable extends Error {
  constructor(message, evidence) {
    super(message);
    this.name = 'CdaDiagnosticReportAppendWitnessUnavailable';
    this.evidence = evidence;
  }
}

const validateScan = (rows, { project, generation }) => {
  assert(Array.isArray(rows), 'Bounded DiagnosticReport scan must return raw rows');
  assert(rows.length <= MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN,
    `Bounded DiagnosticReport scan exceeded its ${MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN}-row limit`);
  assert(rows.every(row => row?.project === project && row?.generation === generation &&
    row?.resourceType === 'DiagnosticReport' && typeof row?._id === 'string' && row._id.length > 0 &&
    typeof row?.id === 'string' && row.id.trim().length > 0),
  'Bounded DiagnosticReport scan contains an out-of-scope record or an invalid FHIR id');
  assert.equal(new Set(rows.map(row => row._id)).size, rows.length,
    'Bounded DiagnosticReport scan repeated an Arango document key');
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length,
    'Bounded DiagnosticReport scan repeated a FHIR id');
  return [...rows].sort((left, right) => left._id.localeCompare(right._id));
};

const hasScalarStatus = row => row.fieldPresent === true && typeof row.fieldValue === 'string' &&
  row.fieldValue.trim().length > 0;

const countStatusRows = rows => {
  const counts = new Map();
  for (const row of rows) counts.set(row.key, (counts.get(row.key) ?? 0) + 1);
  return [...counts.entries()].sort(([left], [right]) => left.localeCompare(right));
};

const exactRawRow = row => ({
  _id: row._id,
  id: row.id,
  project: row.project,
  generation: row.generation,
  resourceType: row.resourceType,
  fieldPresent: row.fieldPresent,
  fieldValue: row.fieldValue ?? null,
});

export function prepareCdaDiagnosticReportAppendOracle(scanRows, { project, generation }) {
  assert(typeof project === 'string' && project.length > 0,
    'DiagnosticReport current-draft APPEND needs an exact project');
  assert(typeof generation === 'string' && generation.length > 0,
    'DiagnosticReport current-draft APPEND needs an exact generation');
  const rows = validateScan(scanRows, { project, generation });
  const eligibleRows = rows.filter(hasScalarStatus);
  const groups = new Map();
  for (const row of eligibleRows) {
    const members = groups.get(row.fieldValue) ?? [];
    members.push(row);
    groups.set(row.fieldValue, members);
  }
  const categoriesWithFourRecords = [...groups]
    .filter(([, members]) => members.length >= 4)
    .map(([status]) => status)
    .sort((left, right) => left.localeCompare(right));
  const resources = [{
    resourceType: 'DiagnosticReport',
    fieldPath: 'status',
    returnedRows: rows.length,
    eligibleScalarStatusRows: eligibleRows.length,
    excludedMissingNullNonStringOrBlankStatus: rows.length - eligibleRows.length,
    categoriesWithFourRecords,
  }];

  if (!categoriesWithFourRecords.length) {
    const evidence = {
      project,
      generation,
      resourceType: 'DiagnosticReport',
      fieldPath: 'status',
      scanLimitPerResource: MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN,
      scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
      requiredSourceWitnesses: [
        { sourceKey: 'diagnostic-report-left', resourceType: 'DiagnosticReport', fieldPath: 'status', memberCount: 2 },
        { sourceKey: 'diagnostic-report-right', resourceType: 'DiagnosticReport', fieldPath: 'status', memberCount: 2,
          disjointFrom: 'diagnostic-report-left' },
      ],
      resources,
      reason: ['the bounded DiagnosticReport.status scan has no nonblank scalar status category with four rows'],
    };
    throw new CdaDiagnosticReportAppendWitnessUnavailable(
      `The bounded ${MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN}-row CDA DiagnosticReport.status scan cannot form two disjoint duplicate-status populations: ${evidence.reason[0]}`,
      evidence,
    );
  }

  const status = categoriesWithFourRecords[0];
  const statusRows = groups.get(status);
  const selected = {
    'diagnostic-report-left': statusRows.slice(0, 2),
    'diagnostic-report-right': statusRows.slice(2, 4),
  };
  const selectedIDs = Object.values(selected).flat().map(row => row._id);
  assert.equal(new Set(selectedIDs).size, 4, 'The two DiagnosticReport populations must be disjoint');
  const sources = Object.fromEntries(Object.entries(selected).map(([sourceKey, selectedRows]) => [sourceKey,
    selectedRows.map(row => ({ ...exactRawRow(row), key: row.fieldValue }))]));
  const grouped = Object.fromEntries(Object.entries(sources).map(([sourceKey, selectedRows]) => [sourceKey,
    countStatusRows(selectedRows.map(row => ({ key: row.key })))]));
  const appendRows = [
    ...grouped['diagnostic-report-left'],
    ...grouped['diagnostic-report-right'],
  ].map(([key, count]) => [String(key), String(count)]);
  const duplicateStatusCount = appendRows.filter(([key]) => key === status).length;
  assert.equal(duplicateStatusCount, 2,
    'Raw APPEND oracle must preserve duplicate status rows from disjoint DiagnosticReport groups');

  return {
    project,
    generation,
    resourceType: 'DiagnosticReport',
    fieldPath: 'status',
    scanLimitPerResource: MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN,
    scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
    resources,
    selected,
    sources,
    exactExpected: Object.fromEntries(Object.entries(sources).map(([sourceKey, selectedRows]) => [sourceKey,
      selectedRows.map(({ key, ...row }) => row)])),
    status,
    grouped,
    append: { rows: appendRows, duplicateStatusCount },
  };
}

export function assertCdaDiagnosticReportAppendReread(actualRows, expectedRows, { project, generation }) {
  assert(Array.isArray(actualRows) && Array.isArray(expectedRows));
  const normalize = input => [...input].map(exactRawRow)
    .sort((left, right) => left._id.localeCompare(right._id));
  const actual = normalize(actualRows);
  const expected = normalize(expectedRows);
  assert(actual.every(row => row.project === project && row.generation === generation &&
    row.resourceType === 'DiagnosticReport'),
  'Exact DiagnosticReport reread escaped its project, generation, or resource type');
  assert.deepEqual(actual, expected,
    'Exact selected DiagnosticReport raw reread differs from the independent witness');
  return { count: actual.length, exact: true, resourceType: 'DiagnosticReport', project, generation };
}
