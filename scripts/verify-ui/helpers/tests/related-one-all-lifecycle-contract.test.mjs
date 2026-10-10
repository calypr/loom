import assert from 'node:assert/strict';
import test from 'node:test';
import {
  statusOneAllLifecycleCheckNames,
  statusOneAllLifecycleChecks,
  statusOneAllTimingCheckpoints,
  zeroObservationLifecycleCheckNames,
  zeroObservationLifecycleChecks,
  zeroObservationTimingCheckpoints,
} from '../related-one-all-lifecycle-contract.mjs';

const passedTimings = names => names.map(name => ({ name, durationMs: 250 }));
const unchangedBuild = { sourceFreeze: { unchanged: true }, sourceFingerprint: { unchanged: true }, apiBuildFreeze: { unchanged: true } };

function zeroObservationReport() {
  const patientId = 'patient-with-no-observations';
  return {
    project: 'fixture-project',
    generation: 'fixture-generation',
    ...unchangedBuild,
    oracle: {
      exactMembershipScope: { project: 'fixture-project', generation: 'fixture-generation', rootResourceType: 'Patient', selectedRootCount: 1, incomingObservationEdgeCount: 0 },
      witnessSummary: { patientId },
      finalScopedRereadMatched: true,
    },
    zeroObservationExpansion: {
      route: [{ edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node',
        fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient',
        storageDirection: 'INBOUND', matchMode: 'OPTIONAL' }],
      savedPolicy: 'PRESERVE_PARENT', emptyPolicy: 'PRESERVE_PARENT', previewRowCount: 1,
      outputNullable: true, previewSchemaNullable: true, unmatchedObservationOutputIsNull: true,
    },
    zeroObservationOne: { previewStatus: 'READY', rowCount: 1, patientId, value: null },
    zeroObservationOneCancel: { workspaceUnchanged: true, populationUnchanged: true, constructionUnchanged: true, renderedRowsMatch: true },
    zeroObservationAll: { previewStatus: 'READY', rowCount: 1, patientId, values: [] },
    zeroObservationAllCancel: { workspaceUnchanged: true, populationUnchanged: true, constructionUnchanged: true, renderedRowsMatch: true },
    zeroObservationSavedAll: {
      candidateSourceMatches: true, form: 'ALL', rowValuePolicy: 'ALL', savedParentCount: 1,
      previewValues: [], reloadParentCount: 1, reloadValues: [], reloadSourceMatches: true,
    },
    relatedOutputEditEvidence: { labelChanged: true, candidateSourceMatches: true, policy: 'ALL', reloadValues: [] },
    relatedSourceRestoration: {
      constructionMatchesBaseline: true, populationMatchesBaseline: true, columnsMatchBaseline: true,
      previewRowsMatchBaseline: true, relatedOutputAbsent: true,
    },
    cases: passedTimings(zeroObservationTimingCheckpoints),
  };
}

function statusOneAllReport() {
  const values = ['final', 'preliminary'];
  return {
    project: 'fixture-project',
    generation: 'fixture-generation',
    ...unchangedBuild,
    statusSourceContract: { path: 'Observation.status', logicalType: 'string', cardinality: 'optional_one', relatedSourceForm: 'ALL' },
    relatedFieldCandidate: { fieldPath: 'status', logicalType: 'string', cardinality: 'optional_one' },
    oracle: {
      exactMembershipScope: { project: 'fixture-project', generation: 'fixture-generation' },
      fixtureAvailability: { requiredManyWitnessAvailable: true },
      finalScopedRereadMatched: true,
    },
    oneRejection: { status: 422, errorCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', distinctStatusValues: values },
    oneChooserAfterRejection: {
      open: true, policy: 'ONE', routeChecked: true, formChecked: true, addEnabled: true, savedGroupUnchanged: true,
    },
    relatedAllOutputEvidence: {
      policy: 'ALL', form: 'ALL', contributorPolicy: 'ALL_MATCHES',
      expectedValues: values, previewValues: values, identitiesMatch: true,
    },
    relatedAllCancelEvidence: { workspaceUnchanged: true, populationUnchanged: true, constructionUnchanged: true, renderedRowsMatch: true },
    relatedAllSavedEvidence: {
      candidateSourceMatches: true, form: 'ALL', rowValuePolicy: 'ALL',
      expectedValues: values, previewValues: values, reloadValues: values, reloadSourceMatches: true,
    },
    relatedOutputEditEvidence: {
      labelChanged: true, candidateSourceMatches: true, policy: 'ALL', expectedValues: values, reloadValues: values,
    },
    relatedSourceRestoration: {
      constructionMatchesBaseline: true, populationMatchesBaseline: true, columnsMatchBaseline: true,
      previewRowsMatchBaseline: true, relatedOutputAbsent: true,
    },
    cases: passedTimings(statusOneAllTimingCheckpoints),
  };
}

test('zero-match ONE/null and ALL/[] lifecycle checks reject fabricated values and missing restoration proof', () => {
  const report = zeroObservationReport();
  const checks = zeroObservationLifecycleChecks(report);
  assert.deepEqual(checks.map(({ name }) => name), zeroObservationLifecycleCheckNames);
  assert(checks.every(({ passed }) => passed));
  assert.deepEqual(checks[1].evidence.route, report.zeroObservationExpansion.route,
    'The report must retain the signed route metadata while the contract checks only its semantic hop shape');

  report.zeroObservationExpansion.route[0].relationship = 'encounter_Patient';
  let routeCheck = zeroObservationLifecycleChecks(report).find(({ name }) =>
    name === 'PRESERVE_PARENT retains the exact Patient row with a nullable null Observation ID');
  assert.equal(routeCheck.passed, false);
  report.zeroObservationExpansion.route[0].relationship = 'subject_Patient';
  report.zeroObservationExpansion.route.push({ edgeId: 'observation-encounter', fromNodeId: 'observation-node',
    toNodeId: 'encounter-node', fromResourceType: 'Observation', toResourceType: 'Encounter',
    relationship: 'encounter', storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL' });
  routeCheck = zeroObservationLifecycleChecks(report).find(({ name }) =>
    name === 'PRESERVE_PARENT retains the exact Patient row with a nullable null Observation ID');
  assert.equal(routeCheck.passed, false);

  report.zeroObservationOne.value = 'fabricated-observation';
  report.zeroObservationAll.values = [null];
  report.relatedSourceRestoration.previewRowsMatchBaseline = false;
  const corrected = new Map(zeroObservationLifecycleChecks(report).map(({ name, passed }) => [name, passed]));
  assert.equal(corrected.get('native ONE returns one exact Patient row with a null Observation ID'), false);
  assert.equal(corrected.get('native ALL returns an empty array for the exact zero-match Patient'), false);
  assert.equal(corrected.get('removing the related source restores the exact grouped rows after reload and final raw reread'), false);
});

test('Observation.status ONE-to-ALL checks require a distinct-value 422, same-chooser repair, and exact reload values', () => {
  const report = statusOneAllReport();
  const checks = statusOneAllLifecycleChecks(report);
  assert.deepEqual(checks.map(({ name }) => name), statusOneAllLifecycleCheckNames);
  assert(checks.every(({ passed }) => passed));

  report.oneRejection.status = 200;
  report.oneChooserAfterRejection.open = false;
  report.relatedAllSavedEvidence.reloadValues = ['final'];
  const corrected = new Map(statusOneAllLifecycleChecks(report).map(({ name, passed }) => [name, passed]));
  assert.equal(corrected.get('grouped-row ONE rejects distinct raw status values and preserves the same chooser and Group'), false);
  assert.equal(corrected.get('reload preserves the exact Observation.status values and ALL source binding'), false);
});

test('lifecycle acceptance checks fail closed when a required native timing checkpoint is absent or over budget', () => {
  const zero = zeroObservationReport();
  zero.cases.pop();
  const zeroTiming = zeroObservationLifecycleChecks(zero).find(({ dimension }) => dimension === 'performance');
  assert.equal(zeroTiming.passed, false);

  const status = statusOneAllReport();
  status.cases[0].durationMs = 5001;
  const statusTiming = statusOneAllLifecycleChecks(status).find(({ dimension }) => dimension === 'performance');
  assert.equal(statusTiming.passed, false);
});
