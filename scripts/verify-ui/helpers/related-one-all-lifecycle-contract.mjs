const ZERO_OBSERVATION_CHECKS = Object.freeze([
  ['correctness', 'bounded zero-match Patient stays in the exact project/generation with the typed Observation route'],
  ['correctness', 'PRESERVE_PARENT retains the exact Patient row with a nullable null Observation ID'],
  ['correctness', 'native ONE returns one exact Patient row with a null Observation ID'],
  ['correctness', 'Canceling ONE preserves the grouped source workspace and rendered rows'],
  ['correctness', 'native ALL returns an empty array for the exact zero-match Patient'],
  ['correctness', 'Canceling ALL preserves the grouped source workspace and rendered rows'],
  ['correctness', 'Apply saves the exact related ALL source and renders the Patient with an empty array'],
  ['persistence', 'reload preserves the applied empty ALL array and exact related source binding'],
  ['persistence', 'editing the output label and reload preserve the empty ALL array and source binding'],
  ['persistence', 'removing the related source restores the exact grouped rows after reload and final raw reread'],
  ['performance', 'zero-match ONE/ALL action-to-render checkpoints finish within five seconds'],
]);

const STATUS_ONE_ALL_CHECKS = Object.freeze([
  ['correctness', 'bounded Observation.status witness stays exact in the project/generation and final raw reread'],
  ['correctness', 'Observation.status chooser source is the compiler-proved scalar string on the signed route'],
  ['correctness', 'grouped-row ONE rejects distinct raw status values and preserves the same chooser and Group'],
  ['correctness', 'same-chooser ALL repair preserves every raw status value by Observation identity'],
  ['correctness', 'Canceling ALL preserves the grouped source workspace and rendered rows'],
  ['correctness', 'Apply saves the exact Observation.status ALL source and its raw values'],
  ['persistence', 'reload preserves the exact Observation.status values and ALL source binding'],
  ['persistence', 'editing the output label and reload preserve the status route, source, and values'],
  ['persistence', 'removing the status source restores exact grouped rows after reload and final raw reread'],
  ['performance', 'Observation.status ONE-to-ALL action-to-render checkpoints finish within five seconds'],
]);

const checkResult = (dimension, name, passed, evidence) => ({
  dimension,
  name,
  passed: Boolean(passed),
  evidence,
});

const withinBudget = (report, names) => {
  const checkpoints = new Map((report.cases ?? []).map((entry) => [entry.name, entry]));
  const measured = names.map((name) => ({ name, durationMs: checkpoints.get(name)?.durationMs }));
  return {
    passed: measured.every(({ durationMs }) => Number.isFinite(durationMs) && durationMs <= 5000),
    measured,
    limitMs: 5000,
  };
};

export const zeroObservationTimingCheckpoints = Object.freeze([
  'expand-Patient-Observation-preview',
  'expand-Patient-Observation-apply-to-render',
  'cancel-zero-observation-one-preserves-group',
  'same-chooser-all-repair-preview',
  'cancel-related-all-proposal-preserves-group',
  'apply-related-all-to-native-table-render',
  'native-preview-matches-exact-related-field-oracle',
  'reload-related-all-available-cardinality-values',
  'edit-related-field-output-label',
  'reload-edited-related-field-output-label',
  'remove-related-source-step-preview',
  'apply-remove-related-step-restores-native-group-table',
  'reload-restored-available-witness-group-table',
]);

export const statusOneAllTimingCheckpoints = Object.freeze([
  'group-available-patient-witnesses-preview',
  'group-available-patient-witnesses-apply-to-render',
  'reload-independent-grouped-witnesses',
  'related-observation-one-disagreement',
  'same-chooser-all-repair-preview',
  'cancel-related-all-proposal-preserves-group',
  'reopened-all-preview-before-apply',
  'apply-related-all-to-native-table-render',
  'native-preview-matches-exact-related-field-oracle',
  'reload-related-all-available-cardinality-values',
  'edit-related-field-output-label',
  'reload-edited-related-field-output-label',
  'remove-related-source-step-preview',
  'apply-remove-related-step-restores-native-group-table',
  'reload-restored-available-witness-group-table',
]);

export const zeroObservationLifecycleCheckNames = Object.freeze(ZERO_OBSERVATION_CHECKS.map(([, name]) => name));
export const statusOneAllLifecycleCheckNames = Object.freeze(STATUS_ONE_ALL_CHECKS.map(([, name]) => name));

export function zeroObservationLifecycleChecks(report) {
  const scope = report.oracle?.exactMembershipScope;
  const expansion = report.zeroObservationExpansion;
  const one = report.zeroObservationOne;
  const oneCancel = report.zeroObservationOneCancel;
  const all = report.zeroObservationAll;
  const allCancel = report.zeroObservationAllCancel;
  const saved = report.zeroObservationSavedAll;
  const edit = report.relatedOutputEditEvidence;
  const restoration = report.relatedSourceRestoration;
  const timings = withinBudget(report, zeroObservationTimingCheckpoints);
  const routeShape = Array.isArray(expansion?.route) ? expansion.route.map((hop) => ({
    fromResourceType: hop?.fromResourceType,
    toResourceType: hop?.toResourceType,
    relationship: hop?.relationship,
    storageDirection: hop?.storageDirection,
  })) : undefined;
  const exactRoute = JSON.stringify(routeShape) === JSON.stringify([
    { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
  ]);
  return [
    checkResult('correctness', ZERO_OBSERVATION_CHECKS[0][1],
      scope?.project === report.project && scope?.generation === report.generation &&
        scope?.rootResourceType === 'Patient' && scope?.selectedRootCount === 1 &&
        scope?.incomingObservationEdgeCount === 0 && report.oracle?.finalScopedRereadMatched === true,
      { scope, finalScopedRereadMatched: report.oracle?.finalScopedRereadMatched, route: expansion?.route }),
    checkResult('correctness', ZERO_OBSERVATION_CHECKS[1][1],
      exactRoute && expansion?.savedPolicy === 'PRESERVE_PARENT' && expansion?.emptyPolicy === 'PRESERVE_PARENT' &&
        expansion?.previewRowCount === 1 && expansion?.outputNullable === true &&
        expansion?.previewSchemaNullable === true && expansion?.unmatchedObservationOutputIsNull === true,
      expansion),
    checkResult('correctness', ZERO_OBSERVATION_CHECKS[2][1],
      one?.previewStatus === 'READY' && one?.rowCount === 1 && one?.patientId === report.oracle?.witnessSummary?.patientId &&
        one?.value === null,
      one),
    checkResult('correctness', ZERO_OBSERVATION_CHECKS[3][1],
      oneCancel?.workspaceUnchanged === true && oneCancel?.populationUnchanged === true &&
        oneCancel?.constructionUnchanged === true && oneCancel?.renderedRowsMatch === true,
      oneCancel),
    checkResult('correctness', ZERO_OBSERVATION_CHECKS[4][1],
      all?.previewStatus === 'READY' && all?.rowCount === 1 &&
        all?.patientId === report.oracle?.witnessSummary?.patientId &&
        Array.isArray(all?.values) && all.values.length === 0,
      all),
    checkResult('correctness', ZERO_OBSERVATION_CHECKS[5][1],
      allCancel?.workspaceUnchanged === true && allCancel?.populationUnchanged === true &&
        allCancel?.constructionUnchanged === true && allCancel?.renderedRowsMatch === true,
      allCancel),
    checkResult('correctness', ZERO_OBSERVATION_CHECKS[6][1],
      saved?.candidateSourceMatches === true && saved?.form === 'ALL' && saved?.rowValuePolicy === 'ALL' &&
        saved?.savedParentCount === 1 && Array.isArray(saved?.previewValues) && saved.previewValues.length === 0,
      saved),
    checkResult('persistence', ZERO_OBSERVATION_CHECKS[7][1],
      saved?.reloadParentCount === 1 && Array.isArray(saved?.reloadValues) && saved.reloadValues.length === 0 &&
        saved?.reloadSourceMatches === true,
      saved),
    checkResult('persistence', ZERO_OBSERVATION_CHECKS[8][1],
      edit?.labelChanged === true && edit?.candidateSourceMatches === true && edit?.policy === 'ALL' &&
        Array.isArray(edit?.reloadValues) && edit.reloadValues.length === 0,
      edit),
    checkResult('persistence', ZERO_OBSERVATION_CHECKS[9][1],
      restoration?.constructionMatchesBaseline === true && restoration?.populationMatchesBaseline === true &&
        restoration?.columnsMatchBaseline === true && restoration?.previewRowsMatchBaseline === true &&
        restoration?.relatedOutputAbsent === true && report.oracle?.finalScopedRereadMatched === true,
      restoration),
    checkResult('performance', ZERO_OBSERVATION_CHECKS[10][1], timings.passed, timings),
  ];
}

export function statusOneAllLifecycleChecks(report) {
  const scope = report.oracle?.exactMembershipScope;
  const contract = report.statusSourceContract;
  const rejection = report.oneRejection;
  const retained = report.oneChooserAfterRejection;
  const all = report.relatedAllOutputEvidence;
  const cancel = report.relatedAllCancelEvidence;
  const saved = report.relatedAllSavedEvidence;
  const edit = report.relatedOutputEditEvidence;
  const restoration = report.relatedSourceRestoration;
  const timings = withinBudget(report, statusOneAllTimingCheckpoints);
  return [
    checkResult('correctness', STATUS_ONE_ALL_CHECKS[0][1],
      scope?.project === report.project && scope?.generation === report.generation &&
        report.oracle?.fixtureAvailability?.requiredManyWitnessAvailable === true &&
        report.oracle?.finalScopedRereadMatched === true,
      { scope, fixtureAvailability: report.oracle?.fixtureAvailability,
        finalScopedRereadMatched: report.oracle?.finalScopedRereadMatched }),
    checkResult('correctness', STATUS_ONE_ALL_CHECKS[1][1],
      contract?.path === 'Observation.status' && contract?.logicalType === 'string' &&
        contract?.cardinality === 'optional_one' && contract?.relatedSourceForm === 'ALL' &&
        report.relatedFieldCandidate?.fieldPath === 'status' &&
        report.relatedFieldCandidate?.logicalType === 'string' &&
        report.relatedFieldCandidate?.cardinality === 'optional_one',
      { contract, candidate: report.relatedFieldCandidate, route: report.relatedChoiceAssertions?.at(-1)?.route }),
    checkResult('correctness', STATUS_ONE_ALL_CHECKS[2][1],
      rejection?.status === 422 && rejection?.errorCode === 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' &&
        Array.isArray(rejection?.distinctStatusValues) && rejection.distinctStatusValues.length > 1 &&
        retained?.open === true && retained?.policy === 'ONE' && retained?.routeChecked === true &&
        retained?.formChecked === true && retained?.addEnabled === true &&
        retained?.savedGroupUnchanged === true,
      { rejection, retained }),
    checkResult('correctness', STATUS_ONE_ALL_CHECKS[3][1],
      all?.policy === 'ALL' && all?.form === 'ALL' && all?.contributorPolicy === 'ALL_MATCHES' &&
        Array.isArray(all?.expectedValues) && JSON.stringify(all.previewValues) === JSON.stringify(all.expectedValues) &&
        all?.identitiesMatch === true,
      all),
    checkResult('correctness', STATUS_ONE_ALL_CHECKS[4][1],
      cancel?.workspaceUnchanged === true && cancel?.populationUnchanged === true &&
        cancel?.constructionUnchanged === true && cancel?.renderedRowsMatch === true,
      cancel),
    checkResult('correctness', STATUS_ONE_ALL_CHECKS[5][1],
      saved?.candidateSourceMatches === true && saved?.form === 'ALL' && saved?.rowValuePolicy === 'ALL' &&
        JSON.stringify(saved.previewValues) === JSON.stringify(saved.expectedValues),
      saved),
    checkResult('persistence', STATUS_ONE_ALL_CHECKS[6][1],
      JSON.stringify(saved?.reloadValues) === JSON.stringify(saved?.expectedValues) &&
        saved?.reloadSourceMatches === true,
      saved),
    checkResult('persistence', STATUS_ONE_ALL_CHECKS[7][1],
      edit?.labelChanged === true && edit?.candidateSourceMatches === true && edit?.policy === 'ALL' &&
        JSON.stringify(edit?.reloadValues) === JSON.stringify(edit?.expectedValues),
      edit),
    checkResult('persistence', STATUS_ONE_ALL_CHECKS[8][1],
      restoration?.constructionMatchesBaseline === true && restoration?.populationMatchesBaseline === true &&
        restoration?.columnsMatchBaseline === true && restoration?.previewRowsMatchBaseline === true &&
        restoration?.relatedOutputAbsent === true && report.oracle?.finalScopedRereadMatched === true,
      restoration),
    checkResult('performance', STATUS_ONE_ALL_CHECKS[9][1], timings.passed, timings),
  ];
}
