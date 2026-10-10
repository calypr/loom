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

export function prepareCdaUpstreamAppendPatientSubsetOracle(oracle, patientSubset) {
  assert(typeof oracle?.project === 'string' && oracle.project.length > 0,
    'Patient subset APPEND needs the prepared current-draft oracle');
  assert(typeof oracle?.generation === 'string' && oracle.generation.length > 0,
    'Patient subset APPEND needs the prepared current-draft oracle generation');
  assert(Array.isArray(patientSubset) && patientSubset.length === 1,
    'Patient starting-collection subset must contain exactly one selected witness');

  const selected = patientSubset[0];
  assert(selected && typeof selected === 'object' && !Array.isArray(selected),
    'Patient starting-collection subset must contain a raw Patient witness');
  assert.equal(selected.project, oracle.project,
    'Selected Patient subset escaped its exact project');
  assert.equal(selected.generation, oracle.generation,
    'Selected Patient subset escaped its exact generation');
  assert.equal(selected.resourceType, 'Patient',
    'Selected starting-collection subset must be a Patient');

  const witnesses = oracle.sources?.['patient-id'];
  assert(Array.isArray(witnesses) && witnesses.length === 2,
    'Prepared current-draft oracle must contain the two Patient.id witnesses');
  const witness = witnesses.find(row => row?._id === selected._id && row?.id === selected.id);
  assert(witness, 'Selected Patient subset must be a member of the exact two-row Patient witness');
  const identity = row => ({
    project: row.project,
    generation: row.generation,
    resourceType: row.resourceType,
    id: row.id,
    _id: row._id,
    fieldPresent: row.fieldPresent,
    fieldValue: row.fieldValue,
    key: row.key,
  });
  assert.deepEqual(identity(selected), identity(witness),
    'Selected Patient subset must preserve the exact source identity and id field');

  const selectedPatient = identity(witness);
  const grouped = countRows([{ key: selectedPatient.key }]);
  const derived = grouped.map(([id, count]) => [id, count, count + 1]);
  const narrowedAppend = [
    ...oracle.grouped['observation-left'],
    ...oracle.grouped['observation-right'],
    ...derived.map(([id, , count]) => [id, count]),
  ].map(([category, count]) => [String(category), String(count)]);
  const baselineAppend = oracle.append?.plusOne;
  assert(Array.isArray(baselineAppend), 'Prepared current-draft oracle must contain its APPEND');
  assert.equal(baselineAppend.length, 4, 'Prepared current-draft oracle must contain its four-row APPEND');

  return {
    project: oracle.project,
    generation: oracle.generation,
    startingCollection: {
      before: witnesses.map(identity),
      selected: selectedPatient,
      afterNarrowing: [selectedPatient],
      afterRestoration: witnesses.map(identity),
    },
    grouped: {
      before: oracle.grouped['patient-id'],
      afterNarrowing: grouped,
      afterRestoration: oracle.grouped['patient-id'],
    },
    derived: {
      before: oracle.patientDerived.plusOne,
      afterNarrowing: derived,
      afterRestoration: oracle.patientDerived.plusOne,
    },
    append: {
      before: baselineAppend,
      afterNarrowing: narrowedAppend,
      afterRestoration: baselineAppend,
      rowCounts: {
        before: baselineAppend.length,
        afterNarrowing: narrowedAppend.length,
        afterRestoration: baselineAppend.length,
      },
      duplicateFinalRowsAfterNarrowing: narrowedAppend.filter(([category]) => category === 'final').length,
    },
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

export function proveSupersededEmptyGroupProposal({
  canceledEntry,
  canceledBody,
  replacementEntry,
  replacementBody,
  replacementResponse,
  uiOrigin,
  proposalPath,
  outputId,
  snapshotToken,
  draftVersion,
  draftDigest,
  inputColumnId,
  checkboxClickedAt,
  previewRowCount,
  previewRowsMatched,
}) {
  const sameRequestBase = (value, label) => {
    assert.equal(value?.outputId, outputId, `${label} must target the exact current-draft output`);
    assert.equal(value?.snapshotToken, snapshotToken, `${label} must use the exact current catalog snapshot`);
    assert.equal(value?.expectedDraftVersion, draftVersion, `${label} must use the exact current draft version`);
    assert.equal(value?.expectedDraftDigest, draftDigest, `${label} must use the exact current draft digest`);
    if (Object.hasOwn(value, 'draftVersion')) assert.equal(value.draftVersion, draftVersion);
    if (Object.hasOwn(value, 'draftDigest')) assert.equal(value.draftDigest, draftDigest);
  };
  const sameResponseBase = (value, label) => {
    assert.equal(value?.outputId, outputId, `${label} must target the exact current-draft output`);
    assert.equal(value?.snapshotToken, snapshotToken, `${label} must use the exact current catalog snapshot`);
    assert.equal(value?.draftVersion, draftVersion, `${label} must use the exact current draft version`);
    assert.equal(value?.draftDigest, draftDigest, `${label} must use the exact current draft digest`);
    if (Object.hasOwn(value, 'expectedDraftVersion')) assert.equal(value.expectedDraftVersion, draftVersion);
    if (Object.hasOwn(value, 'expectedDraftDigest')) assert.equal(value.expectedDraftDigest, draftDigest);
  };
  const groupStep = (body, label) => {
    const steps = body?.candidateConstruction?.steps;
    assert(Array.isArray(steps) && steps.length === 1, `${label} must contain only the new GROUP step`);
    const step = steps[0];
    assert.equal(step.operation?.kind, 'GROUP', `${label} must be a GROUP proposal`);
    assert.equal(body.changedStepId, step.id, `${label} must bind the changed step identity`);
    return step;
  };
  const countRowsOutput = (step, label) => {
    const aggregates = step.operation.group?.aggregates;
    assert(Array.isArray(aggregates) && aggregates.length === 1,
      `${label} must contain exactly one GROUP aggregate`);
    const aggregate = aggregates[0];
    assert.equal(aggregate.operation, 'COUNT_ROWS', `${label} must retain COUNT_ROWS`);
    assert.equal(typeof aggregate.outputColumnId, 'string', `${label} must identify its count output`);
    return aggregate.outputColumnId;
  };

  assert.equal(canceledEntry?.origin, uiOrigin, 'Canceled proposal must use the independently validated UI origin');
  assert.equal(canceledEntry?.path, proposalPath, 'Canceled proposal must use this Explorer proposal endpoint');
  assert.equal(canceledEntry?.method, 'POST', 'Canceled proposal must be a native POST');
  assert.equal(canceledEntry?.failure, 'net::ERR_ABORTED', 'Only the observed native abort can be superseded');
  assert.equal(typeof canceledEntry?.requestId, 'string');
  assert.equal(typeof canceledEntry?.browserRequestId, 'string');
  assert(Number.isFinite(canceledEntry?.startedAt));
  assert(Number.isFinite(canceledEntry?.completedAt));
  assert(canceledEntry.completedAt >= canceledEntry.startedAt,
    'Canceled proposal completion time must follow its start time');
  assert.equal(canceledEntry.status, undefined, 'Canceled intermediate proposal must have no HTTP response');
  assert.equal(canceledEntry.expected, undefined, 'The workflow must prove cancellation before classifying it');
  assert.equal(typeof checkboxClickedAt, 'number');
  assert(canceledEntry.startedAt < checkboxClickedAt && checkboxClickedAt < canceledEntry.completedAt,
    'The empty GROUP proposal must be in flight when the selected-key checkbox is clicked');

  assert.equal(replacementEntry?.origin, uiOrigin, 'Replacement must use the independently validated UI origin');
  assert.equal(replacementEntry?.path, proposalPath, 'Replacement must use this Explorer proposal endpoint');
  assert.equal(replacementEntry?.method, 'POST', 'Replacement must be a native POST');
  assert.equal(replacementEntry?.status, 200, 'Replacement proposal must return HTTP 200');
  assert.equal(replacementEntry.failure, undefined, 'Replacement request must not have a native failure');
  assert.equal(replacementEntry.responseReadError, undefined, 'Replacement response body must be captured cleanly');
  assert(Number.isFinite(replacementEntry?.startedAt));
  assert(Number.isFinite(replacementEntry?.completedAt));
  assert(replacementEntry.completedAt >= replacementEntry.startedAt,
    'Replacement proposal completion time must follow its start time');
  assert(replacementEntry.startedAt > checkboxClickedAt && canceledEntry.completedAt < replacementEntry.startedAt,
    'The selected-key replacement must start after the click and after the intermediate request aborts');
  assert.notEqual(replacementEntry.browserRequestId, canceledEntry.browserRequestId,
    'Replacement must be a distinct native browser request');
  assert.notEqual(replacementEntry.requestId, canceledEntry.requestId,
    'Replacement must have a distinct request identity');

  sameRequestBase(canceledBody, 'Canceled empty GROUP request');
  sameRequestBase(replacementBody, 'Selected-key replacement request');
  sameResponseBase(replacementResponse, 'Selected-key replacement response');
  const canceledStep = groupStep(canceledBody, 'Canceled intermediate candidate');
  const replacementStep = groupStep(replacementBody, 'Selected-key replacement candidate');
  assert.equal(canceledStep.id, replacementStep.id, 'Both proposals must edit the same new GROUP step');
  assert.equal(replacementResponse.changedStepId, replacementStep.id,
    'READY response must belong to the selected-key GROUP step');
  const canceledKeys = canceledStep.operation.group?.keys;
  const replacementKeys = replacementStep.operation.group?.keys;
  assert.deepEqual(canceledKeys, [], 'Superseded intermediate candidate must have no selected group key');
  assert(Array.isArray(replacementKeys) && replacementKeys.length === 1,
    'Replacement candidate must contain exactly the newly selected group key');
  assert.equal(replacementKeys[0].inputColumnId, inputColumnId,
    'Replacement candidate must use the exact source key selected by the user');
  const canceledCountID = countRowsOutput(canceledStep, 'Canceled intermediate candidate');
  const replacementCountID = countRowsOutput(replacementStep, 'Selected-key replacement candidate');
  assert.equal(canceledCountID, replacementCountID,
    'Replacement must preserve the initial GROUP count output identity');
  const replacementKeyOutput = replacementStep.outputs?.find(output => output.id === replacementKeys[0].outputColumnId);
  assert(replacementKeyOutput, 'Replacement candidate must expose its selected key output');

  const responseStep = groupStep(replacementResponse, 'READY response candidate');
  assert.equal(responseStep.id, replacementStep.id);
  assert.equal(responseStep.operation.group?.keys?.length, 1);
  assert.equal(responseStep.operation.group.keys[0].inputColumnId, inputColumnId);
  assert.equal(countRowsOutput(responseStep, 'READY response candidate'), replacementCountID);
  assert.equal(replacementResponse.previewStatus, 'READY', 'Replacement response must be READY');
  assert.equal(replacementResponse.preview?.outputId, outputId, 'READY preview must target the exact output');
  assert.equal(typeof replacementResponse.proposalId, 'string');
  assert(replacementResponse.proposalId.length > 0, 'READY replacement must have a proposal receipt');
  assert.equal(replacementResponse.preview?.receiptId, replacementResponse.proposalId,
    'READY preview must be bound to the exact replacement receipt');
  assert.equal(replacementResponse.preview?.rowCount, previewRowCount,
    'READY preview row count must match the independent raw GROUP oracle');
  assert.equal(previewRowsMatched, true, 'Replacement preview must already match the independent raw GROUP rows');

  return {
    supersession: 'initial-empty-group-candidate-replaced-by-selected-key',
    uiOriginBound: true,
    proposalEndpointBound: true,
    outputId,
    changedStepId: replacementStep.id,
    request: {
      canceledRequestId: canceledEntry.requestId,
      canceledBrowserRequestId: canceledEntry.browserRequestId,
      replacementRequestId: replacementEntry.requestId,
      replacementBrowserRequestId: replacementEntry.browserRequestId,
    },
    base: { sameSnapshot: true, sameDraftVersion: draftVersion, sameDraftDigest: true },
    canceledCandidate: { operation: 'GROUP', keyCount: 0, aggregate: 'COUNT_ROWS' },
    replacementCandidate: {
      operation: 'GROUP', keyCount: 1, inputColumnId, aggregate: 'COUNT_ROWS',
      keyOutputColumnId: replacementKeyOutput.id, countOutputColumnId: replacementCountID,
    },
    replacementResponse: {
      status: replacementEntry.status,
      previewStatus: replacementResponse.previewStatus,
      receiptBound: true,
      previewRowCount,
      independentRowsMatched: true,
    },
    timing: {
      intermediateStartedAt: canceledEntry.startedAt,
      checkboxClickedAt,
      intermediateAbortedAt: canceledEntry.completedAt,
      replacementStartedAt: replacementEntry.startedAt,
    },
  };
}

export function proveObservedSupersededEmptyGroupProposals({ proposalEntries, ...replacementEvidence }) {
  assert(Array.isArray(proposalEntries), 'Observed GROUP proposal evidence must be an array');
  const canceledEntries = proposalEntries.filter(({ entry }) => entry?.failure === 'net::ERR_ABORTED');
  return canceledEntries.map(({ entry, body }) => proveSupersededEmptyGroupProposal({
    ...replacementEvidence,
    canceledEntry: entry,
    canceledBody: body,
  }));
}
