import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';
import { constructionCandidateWireEquivalent } from './builder-combine-draft-helpers.mjs';

export const MAX_CDA_MEMBERSHIP_SCAN = 2_000;
export const CDA_SOURCE_GROUP_PREVIEW_LIMIT = 25;

export function cdaSourceGroupCandidateResponseEquivalent({ request, response, selectedChoice }) {
  const requestCandidate = request?.candidateConstruction;
  const responseCandidate = response?.candidateConstruction;
  const groupSources = request?.groupSources;
  const step = requestCandidate?.steps?.at(-1);
  const group = step?.operation?.group;
  if (!requestCandidate || !responseCandidate || !Array.isArray(requestCandidate.steps) ||
    requestCandidate.steps.length !== 1 || Object.hasOwn(requestCandidate, 'sourceProjections') ||
    !Array.isArray(groupSources) || groupSources.length !== 1 ||
    typeof selectedChoice?.choiceId !== 'string' || !selectedChoice.choiceId.startsWith('rc1.') ||
    selectedChoice.isPopulated !== true ||
    !['fieldPath', 'fhirType', 'logicalType', 'label', 'occurrenceId'].every(key =>
      typeof selectedChoice[key] === 'string' && selectedChoice[key].length > 0)) return false;

  const [source] = groupSources;
  if (source?.rowChoiceId !== selectedChoice.choiceId || typeof source.columnId !== 'string' || !source.columnId ||
    step?.operation?.kind !== 'GROUP' || step.inputs?.length !== 1 ||
    step.inputs[0]?.kind !== 'SOURCE_PROJECTION' || group?.keys?.length !== 1 ||
    group.keys[0]?.inputColumnId !== source.columnId) return false;

  const expectedProjection = {
    columnId: source.columnId,
    fhirType: selectedChoice.fhirType,
    fieldPath: selectedChoice.fieldPath,
    label: selectedChoice.label,
    logicalType: selectedChoice.logicalType,
    occurrenceId: selectedChoice.occurrenceId,
    ownerStepId: step.id,
  };
  if (!isDeepStrictEqual(responseCandidate.sourceProjections, [expectedProjection])) return false;

  const { sourceProjections: _serverDerivedSourceProjections, ...canonicalCandidate } = responseCandidate;
  return constructionCandidateWireEquivalent(requestCandidate, canonicalCandidate);
}

export function proveCdaSourceGroupSupersededChoiceCancellation({
  supersededEvent,
  supersededBody,
  failureDiagnostic,
  replacementEvent,
  replacementBody,
  replacementResponse,
  project,
  explorer,
  generation,
  uiOrigin,
  outputId,
  snapshotToken,
  draftVersion,
  draftDigest,
  supersededChoice,
  replacementChoice,
  actionLabel,
  actionStartedAt,
}) {
  const fail = reason => ({ ok: false, reason });
  if (![project, explorer, generation, outputId, snapshotToken, draftDigest, actionLabel].every(value =>
    typeof value === 'string' && value.length > 0) || !Number.isInteger(draftVersion) ||
    !Number.isFinite(actionStartedAt) ||
    !supersededEvent || !replacementEvent || !failureDiagnostic) return fail('invalid source GROUP cancellation identity');

  const proposalPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/construction-proposals`;
  const origin = new URL(uiOrigin).origin;
  const expectedScope = {
    expectedProject: project,
    generation,
    configuredExplorer: explorer,
    requestProject: project,
    requestExplorer: explorer,
  };
  if (supersededEvent.origin !== origin || supersededEvent.path !== proposalPath || supersededEvent.method !== 'POST' ||
    supersededEvent.failure !== 'net::ERR_ABORTED' || supersededEvent.status !== undefined ||
    supersededEvent.response !== undefined || !Number.isFinite(supersededEvent.startedAt) ||
    !Number.isFinite(supersededEvent.completedAt) ||
    !supersededEvent.requestId || !supersededEvent.browserRequestId) return fail('superseded request is not one exact response-free native abort');
  if (failureDiagnostic.errorText !== 'net::ERR_ABORTED' || failureDiagnostic.method !== 'POST' ||
    failureDiagnostic.requestId !== supersededEvent.requestId ||
    failureDiagnostic.browserRequestId !== supersededEvent.browserRequestId ||
    failureDiagnostic.url !== `${origin}${proposalPath}` || failureDiagnostic.failureAction?.label !== actionLabel ||
    !isDeepStrictEqual(failureDiagnostic.requestScope, expectedScope)) return fail('native failure does not bind the exact owned action and explorer');

  const sourceStep = body => body?.candidateConstruction?.steps?.at(-1);
  const hasExactSourceChoice = (body, choice) => {
    const step = sourceStep(body);
    return body?.outputId === outputId && body?.snapshotToken === snapshotToken &&
      body?.expectedDraftVersion === draftVersion && body?.expectedDraftDigest === draftDigest &&
      body?.groupSources?.length === 1 && body.groupSources[0]?.rowChoiceId === choice?.choiceId &&
      typeof body.groupSources[0]?.columnId === 'string' && body.groupSources[0].columnId.length > 0 &&
      step?.id === body?.changedStepId && step.operation?.kind === 'GROUP' &&
      step.inputs?.length === 1 && step.inputs[0]?.kind === 'SOURCE_PROJECTION' &&
      step.operation.group?.keys?.length === 1 &&
      step.operation.group.keys[0]?.inputColumnId === body.groupSources[0].columnId;
  };
  if (!supersededChoice || !replacementChoice || supersededChoice.choiceId === replacementChoice.choiceId ||
    !hasExactSourceChoice(supersededBody, supersededChoice) ||
    !hasExactSourceChoice(replacementBody, replacementChoice) ||
    supersededBody.changedStepId !== replacementBody.changedStepId) return fail('proposal bodies do not bind the two exact source choices to the same draft GROUP');
  if (replacementEvent.origin !== origin || replacementEvent.path !== proposalPath || replacementEvent.method !== 'POST' ||
    replacementEvent.status !== 200 || !Number.isFinite(replacementEvent.startedAt) || !Number.isFinite(replacementEvent.completedAt) ||
    !replacementEvent.requestId || !replacementEvent.browserRequestId ||
    replacementEvent.browserRequestId === supersededEvent.browserRequestId || replacementEvent.triggerAction !== actionLabel ||
    supersededEvent.startedAt >= actionStartedAt || supersededEvent.completedAt < actionStartedAt ||
    replacementEvent.startedAt < actionStartedAt ||
    supersededEvent.completedAt > replacementEvent.completedAt) return fail('replacement is not the later exact action-scoped proposal response');
  if (replacementResponse?.snapshotToken !== snapshotToken || replacementResponse?.draftVersion !== draftVersion ||
    replacementResponse?.draftDigest !== draftDigest || replacementResponse?.outputId !== outputId ||
    replacementResponse?.proposalId !== replacementResponse?.preview?.receiptId ||
    replacementResponse?.previewStatus !== 'READY' ||
    !cdaSourceGroupCandidateResponseEquivalent({ request: replacementBody, response: replacementResponse, selectedChoice: replacementChoice })) {
    return fail('replacement Status proposal is not an exact READY response for its selected source choice');
  }

  const reason = 'Selecting the explicit raw Observation status choice supersedes the automatic first-choice source GROUP preview.';
  return {
    ok: true,
    proof: {
      project, explorer, generation, outputId, snapshotToken, draftVersion, draftDigest,
      supersededAction: actionLabel,
      actionStartedAt,
      supersededRequest: {
        requestId: supersededEvent.requestId,
        browserRequestId: supersededEvent.browserRequestId,
        method: supersededEvent.method,
        path: supersededEvent.path,
        startedAt: supersededEvent.startedAt,
        completedAt: supersededEvent.completedAt,
        failure: supersededEvent.failure,
        choiceId: supersededBody.groupSources[0].rowChoiceId,
        fieldPath: supersededChoice.fieldPath,
        occurrenceId: supersededChoice.occurrenceId,
      },
      replacement: {
        requestId: replacementEvent.requestId,
        browserRequestId: replacementEvent.browserRequestId,
        startedAt: replacementEvent.startedAt,
        completedAt: replacementEvent.completedAt,
        status: replacementEvent.status,
        action: actionLabel,
        choiceId: replacementBody.groupSources[0].rowChoiceId,
        fieldPath: replacementChoice.fieldPath,
        occurrenceId: replacementChoice.occurrenceId,
        previewStatus: replacementResponse.previewStatus,
        responseExact: true,
        sourceProjections: replacementResponse.candidateConstruction.sourceProjections,
      },
      transition: 'the exact native source GROUP request for the automatic first populated choice was aborted when the user selected Status; the replacement request for that same output, snapshot, and draft completed with an exact READY preview',
      reason,
    },
  };
}

export const cdaMembershipObservationQuery = ({ project, generation }) => {
  assert.equal(typeof project, 'string');
  assert.equal(typeof generation, 'string');
  return `FOR r IN Observation FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)} AND r.payload.resourceType == "Observation" AND IS_STRING(r.id) AND LENGTH(TRIM(r.id)) > 0 SORT r._id LIMIT ${MAX_CDA_MEMBERSHIP_SCAN} RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType}`;
};

export function prepareCdaMembershipOracle(rows, { project, generation }) {
  assert(Array.isArray(rows), 'Bounded CDA Membership oracle requires raw Observation rows');
  assert(rows.length <= MAX_CDA_MEMBERSHIP_SCAN, 'Bounded CDA Membership oracle exceeded its 2,000-row limit');
  assert(typeof project === 'string' && project.length > 0, 'Bounded CDA Membership oracle requires an exact project');
  assert(typeof generation === 'string' && generation.length > 0, 'Bounded CDA Membership oracle requires an exact generation');
  assert(rows.every(row => row?.project === project && row?.generation === generation &&
    row?.resourceType === 'Observation' && typeof row?._id === 'string' && row._id.length > 0 &&
    typeof row?.id === 'string' && row.id.trim().length > 0),
  'Bounded CDA Membership oracle contains an out-of-scope or invalid Observation');
  assert.equal(new Set(rows.map(row => row._id)).size, rows.length, 'Bounded CDA Membership scan repeated a raw document key');
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length, 'Bounded CDA Membership scan repeated a FHIR Observation id');

  const ordered = [...rows].sort((left, right) => left._id.localeCompare(right._id));
  assert(ordered.length >= 3, `The first ${MAX_CDA_MEMBERSHIP_SCAN} scoped Observations need at least three distinct nonempty FHIR ids`);
  const [a, b, c] = ordered.slice(0, 3);
  const left = [a, b];
  const right = [a, c];
  const leftIDs = left.map(row => row.id);
  const rightIDs = right.map(row => row.id);
  const overlap = leftIDs.filter(id => new Set(rightIDs).has(id));
  const includeIDs = leftIDs.filter(id => new Set(rightIDs).has(id)).sort();
  const excludeIDs = leftIDs.filter(id => !new Set(rightIDs).has(id)).sort();
  assert.deepEqual(overlap, [a.id], 'CDA Membership witness must have exactly one shared Observation id');
  assert.deepEqual(includeIDs, [a.id].sort(), 'INCLUDE oracle must retain only the shared left id');
  assert.deepEqual(excludeIDs, [b.id].sort(), 'EXCLUDE oracle must retain only the left-only id');
  assert.deepEqual(rightIDs.sort(), [a.id, c.id].sort(), 'Right source must contain the shared and independent third id');

  return {
    scannedCount: rows.length,
    selectionLimit: 3,
    selected: { a: { _id: a._id, id: a.id }, b: { _id: b._id, id: b.id }, c: { _id: c._id, id: c.id } },
    left: left.map(row => ({ _id: row._id, id: row.id })),
    right: right.map(row => ({ _id: row._id, id: row.id })),
    leftIDs,
    rightIDs,
    includeIDs,
    excludeIDs,
  };
}

const sourceGroupKeyIsValid = value => value === null || typeof value === 'string';
const sourceGroupDisplay = value => value === null ? '—' : String(value);
const sourceGroupRowsEqual = (left, right) =>
  JSON.stringify([...left].map(row => JSON.stringify(row)).sort()) ===
  JSON.stringify([...right].map(row => JSON.stringify(row)).sort());

export function compareCdaSourceGroupPreview({
  previewRows,
  visibleRows,
  rawGroupRows,
  displayedRereadRows,
  rowCount,
  sampled,
  limit = CDA_SOURCE_GROUP_PREVIEW_LIMIT,
}) {
  const fail = reason => ({ ok: false, reason });
  if (!Number.isInteger(limit) || limit < 1 || !Array.isArray(previewRows) ||
    !Array.isArray(visibleRows) || !Array.isArray(rawGroupRows)) {
    return fail('invalid preview comparison inputs');
  }
  if (rawGroupRows.length > limit + 1) return fail('raw GROUP scan exceeded its limit-plus-one bound');

  const expectedRowCount = Math.min(rawGroupRows.length, limit);
  const expectedSampled = rawGroupRows.length >= limit;
  if (rowCount !== expectedRowCount || sampled !== expectedSampled ||
    previewRows.length !== expectedRowCount || visibleRows.length !== expectedRowCount) {
    return fail('preview row count or sampled metadata differs from the bounded raw GROUP scan');
  }

  const validRows = rows => rows.every(row => row && sourceGroupKeyIsValid(row.status) &&
    Number.isInteger(row.count) && row.count > 0);
  if (!validRows(rawGroupRows)) return fail('raw GROUP scan contains an invalid key or count');
  if (new Set(rawGroupRows.map(row => JSON.stringify(row.status))).size !== rawGroupRows.length) {
    return fail('raw GROUP scan repeated a status key');
  }

  const entries = [];
  for (const row of previewRows) {
    if (!Array.isArray(row) || row.length !== 2 || !sourceGroupKeyIsValid(row[0]) ||
      !Number.isInteger(row[1]) || row[1] < 1) return fail('preview contains an invalid status key or count');
    entries.push({ status: row[0], count: row[1] });
  }
  if (new Set(entries.map(row => JSON.stringify(row.status))).size !== entries.length) {
    return fail('preview repeated a status key');
  }

  const expectedVisibleRows = entries.map(row => [sourceGroupDisplay(row.status), String(row.count)]);
  if (!sourceGroupRowsEqualInOrder(visibleRows, expectedVisibleRows)) {
    return fail('visible GROUP rows differ from the completed preview response');
  }

  const sampledSubset = rawGroupRows.length > limit;
  const oracleRows = sampledSubset ? displayedRereadRows : rawGroupRows;
  if (!Array.isArray(oracleRows) || !validRows(oracleRows)) {
    return fail(sampledSubset
      ? 'displayed-key raw GROUP reread is missing or invalid'
      : 'complete raw GROUP oracle is missing or invalid');
  }
  if (new Set(oracleRows.map(row => JSON.stringify(row.status))).size !== oracleRows.length) {
    return fail('raw GROUP oracle repeated a status key');
  }
  if (sampledSubset && oracleRows.length !== entries.length) {
    return fail('displayed-key raw GROUP reread did not return every visible key');
  }

  const expectedOracleRows = entries.map(row => [row.status, row.count]);
  const actualOracleRows = oracleRows.map(row => [row.status, row.count]);
  if (!sourceGroupRowsEqual(actualOracleRows, expectedOracleRows)) {
    return fail(sampledSubset
      ? 'displayed status keys or counts differ from the independent raw reread'
      : 'preview status keys or counts differ from the complete independent raw GROUP oracle');
  }

  return {
    ok: true,
    comparison: sampledSubset ? 'displayed-key-reread' : 'complete-raw-group-set',
    expectedRowCount,
    expectedSampled,
    expectedRows: oracleRows.map(row => [sourceGroupDisplay(row.status), String(row.count)]),
    displayedStatusKeys: entries.map(row => row.status),
  };
}

function sourceGroupRowsEqualInOrder(left, right) {
  return Array.isArray(left) && left.length === right.length && left.every((row, index) =>
    Array.isArray(row) && row.length === 2 && row[0] === right[index][0] && row[1] === right[index][1]);
}
