import assert from 'node:assert/strict';
import test from 'node:test';
import {
  assertCdaDiagnosticReportAppendReread,
  CDA_DIAGNOSTIC_REPORT_APPEND_RESOURCE,
  CDA_DIAGNOSTIC_REPORT_APPEND_SOURCES,
  cdaDiagnosticReportAppendRereadQuery,
  cdaDiagnosticReportAppendScanQuery,
  CdaDiagnosticReportAppendWitnessUnavailable,
  MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN,
  prepareCdaDiagnosticReportAppendOracle,
} from '../cda-current-draft-diagnostic-report-append-oracle.mjs';

const project = 'loom-cda-oracle';
const generation = 'cda-fhir-v1';
const row = (_id, id, fieldPresent, fieldValue, overrides = {}) => ({
  _id: `DiagnosticReport/${_id}`,
  id,
  project,
  generation,
  resourceType: 'DiagnosticReport',
  fieldPresent,
  fieldValue,
  ...overrides,
});
const scans = () => [
  row('report-04', 'report-d', true, 'final'),
  row('report-02', 'report-b', true, 'final'),
  row('report-01', 'report-a', true, 'final'),
  row('report-03', 'report-c', true, 'final'),
  row('report-05', 'report-preliminary', true, 'preliminary'),
  row('report-06', 'report-missing', false, null),
  row('report-07', 'report-object-status', true, { code: 'final' }),
];

test('bounded raw scan and exact reread bind the DiagnosticReport.status scope', () => {
  assert.deepEqual(CDA_DIAGNOSTIC_REPORT_APPEND_RESOURCE, {
    resourceType: 'DiagnosticReport', fieldPath: 'status',
  });
  assert.deepEqual(CDA_DIAGNOSTIC_REPORT_APPEND_SOURCES.map(({ sourceKey }) => sourceKey), [
    'diagnostic-report-left', 'diagnostic-report-right',
  ]);
  const query = cdaDiagnosticReportAppendScanQuery({ project, generation });
  assert.match(query, /^FOR r IN DiagnosticReport FILTER/);
  assert.ok(query.includes(`r.project == ${JSON.stringify(project)}`));
  assert.ok(query.includes(`r.dataset_generation == ${JSON.stringify(generation)}`));
  assert.ok(query.includes('r.payload.resourceType == "DiagnosticReport"'));
  assert.match(query, /SORT r\._id LIMIT 2000/);
  assert.match(query, /fieldPresent:HAS\(r\.payload,"status"\)/);
  assert.match(query, /fieldValue:r\.payload\["status"\]/);

  const reread = cdaDiagnosticReportAppendRereadQuery({
    project, generation, documentIDs: ['DiagnosticReport/report-01', 'DiagnosticReport/report-02'],
  });
  assert.ok(reread.includes('r._id IN ["DiagnosticReport/report-01","DiagnosticReport/report-02"]'));
  assert.ok(reread.includes('r.payload.resourceType == "DiagnosticReport"'));
  assert.throws(() => cdaDiagnosticReportAppendRereadQuery({
    project, generation, documentIDs: ['same', 'same'],
  }), /unique/);
  assert.equal(MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN, 2_000);
});

test('oracle picks deterministic disjoint status populations and preserves duplicate GROUP rows in APPEND', () => {
  const input = scans();
  const oracle = prepareCdaDiagnosticReportAppendOracle(input, { project, generation });
  const left = oracle.sources['diagnostic-report-left'];
  const right = oracle.sources['diagnostic-report-right'];
  assert.deepEqual(left.map(item => item._id), ['DiagnosticReport/report-01', 'DiagnosticReport/report-02']);
  assert.deepEqual(right.map(item => item._id), ['DiagnosticReport/report-03', 'DiagnosticReport/report-04']);
  assert.equal(new Set([...left, ...right].map(item => item._id)).size, 4);
  assert([...left, ...right].every(item => item.resourceType === 'DiagnosticReport' && item.fieldValue === 'final'));
  assert.deepEqual(oracle.grouped['diagnostic-report-left'], [['final', 2]]);
  assert.deepEqual(oracle.grouped['diagnostic-report-right'], [['final', 2]]);
  assert.deepEqual(oracle.append.rows, [['final', '2'], ['final', '2']]);
  assert.equal(oracle.append.duplicateStatusCount, 2);
  assert.equal(oracle.resources[0].eligibleScalarStatusRows, 5);
  assert.equal(oracle.resources[0].excludedMissingNullNonStringOrBlankStatus, 2);
  assert.deepEqual(oracle.exactExpected['diagnostic-report-left'].map(item => item.fieldValue), ['final', 'final']);

  const permuted = prepareCdaDiagnosticReportAppendOracle([...input].reverse(), { project, generation });
  assert.deepEqual(permuted.selected, oracle.selected, 'Selection must not depend on scan return order');
  assertCdaDiagnosticReportAppendReread(
    [input[2], input[1]], oracle.exactExpected['diagnostic-report-left'], { project, generation },
  );
});

test('oracle reports bounded unavailability when no DiagnosticReport status category has four rows', () => {
  assert.throws(() => prepareCdaDiagnosticReportAppendOracle([], { project, generation }), error => {
    assert(error instanceof CdaDiagnosticReportAppendWitnessUnavailable);
    assert.match(error.message, /bounded 2000-row CDA DiagnosticReport\.status scan/);
    assert.deepEqual(error.evidence, {
      project,
      generation,
      resourceType: 'DiagnosticReport',
      fieldPath: 'status',
      scanLimitPerResource: 2_000,
      scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
      requiredSourceWitnesses: [
        { sourceKey: 'diagnostic-report-left', resourceType: 'DiagnosticReport', fieldPath: 'status', memberCount: 2 },
        { sourceKey: 'diagnostic-report-right', resourceType: 'DiagnosticReport', fieldPath: 'status', memberCount: 2,
          disjointFrom: 'diagnostic-report-left' },
      ],
      resources: [{
        resourceType: 'DiagnosticReport', fieldPath: 'status', returnedRows: 0, eligibleScalarStatusRows: 0,
        excludedMissingNullNonStringOrBlankStatus: 0, categoriesWithFourRecords: [],
      }],
      reason: ['the bounded DiagnosticReport.status scan has no nonblank scalar status category with four rows'],
    });
    return true;
  });
});

test('oracle rejects out-of-scope, repeated, and over-limit raw scan evidence', () => {
  const outOfScope = scans();
  outOfScope[0] = { ...outOfScope[0], project: 'other-project' };
  assert.throws(() => prepareCdaDiagnosticReportAppendOracle(outOfScope, { project, generation }), /out-of-scope/);

  const duplicateDocument = scans();
  duplicateDocument[0] = { ...duplicateDocument[0], _id: duplicateDocument[1]._id };
  assert.throws(() => prepareCdaDiagnosticReportAppendOracle(duplicateDocument, { project, generation }), /repeated an Arango/);

  const duplicateFHIRID = scans();
  duplicateFHIRID[0] = { ...duplicateFHIRID[0], id: duplicateFHIRID[1].id };
  assert.throws(() => prepareCdaDiagnosticReportAppendOracle(duplicateFHIRID, { project, generation }), /repeated a FHIR id/);

  assert.throws(() => prepareCdaDiagnosticReportAppendOracle(Array(MAX_CDA_DIAGNOSTIC_REPORT_APPEND_SCAN + 1)
    .fill(null).map((_, index) => row(`extra-${index}`, `extra-${index}`, false, null)), { project, generation }), /exceeded its 2000-row limit/);
});

test('exact raw reread rejects changed or cross-generation DiagnosticReport fields', () => {
  const oracle = prepareCdaDiagnosticReportAppendOracle(scans(), { project, generation });
  const expected = oracle.exactExpected['diagnostic-report-left'];
  assert.throws(() => assertCdaDiagnosticReportAppendReread(
    expected.map(item => ({ ...item, fieldValue: 'amended' })), expected, { project, generation },
  ), /differs from the independent witness/);
  assert.throws(() => assertCdaDiagnosticReportAppendReread(
    expected.map(item => ({ ...item, generation: 'other-generation' })), expected, { project, generation },
  ), /escaped its project, generation, or resource type/);
});
