import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';
import {
  CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT,
  prepareCdaGroupPivotJoinOracle,
} from './cda-group-pivot-join-oracle.mjs';

const nonempty = value => typeof value === 'string' && value.trim().length > 0;
const supportedOperators = new Set(['GT', 'GTE', 'LT', 'LTE']);

export function matchesExactGroupSourceOptionsRequest(request, {
  uiOrigin, project, explorer, outputId, snapshotToken,
}) {
  if (![uiOrigin, project, explorer, outputId, snapshotToken].every(nonempty)) return false;
  try {
    const url = new URL(request.url());
    const headers = request.headers();
    const body = request.postDataJSON();
    const expectedKeys = ['limit', 'outputId', 'resourceType', 'snapshotToken'];
    return request.method() === 'POST' && url.origin === new URL(uiOrigin).origin &&
      url.pathname === `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/frame-source-options` &&
      !Object.keys(headers).some(name => name.toLowerCase() === 'x-request-id') &&
      body && typeof body === 'object' && !Array.isArray(body) &&
      JSON.stringify(Object.keys(body).sort()) === JSON.stringify(expectedKeys) &&
      body.snapshotToken === snapshotToken && body.outputId === outputId &&
      body.resourceType === 'Observation' && body.limit === 50;
  } catch {
    return false;
  }
}

export function hasCancellableGroupSourceOptionsResponseState(entry) {
  if (!entry || entry.failure !== 'net::ERR_ABORTED' || !Number.isFinite(entry.completedAt)) return false;
  if (entry.status === undefined) return entry.responseReceivedAt === undefined;
  return entry.status === 200 && Number.isFinite(entry.responseReceivedAt);
}

export function isExactGroupSourceOptionsCancellation(entry, {
  uiOrigin, project, explorer, outputId, snapshotToken,
}) {
  if (!entry || ![uiOrigin, project, explorer, outputId, snapshotToken].every(nonempty) ||
    !hasCancellableGroupSourceOptionsResponseState(entry) || entry.expected !== true || entry.canceled !== true ||
    entry.method !== 'POST' || entry.requestId !== entry.browserRequestId) return false;
  try {
    const origin = new URL(uiOrigin).origin;
    const path = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/frame-source-options`;
    const cancellation = entry.expectedCancellation;
    const proof = cancellation?.proof;
    const request = proof?.request;
    const requestAction = proof?.requestAction;
    const failureAction = proof?.failureAction;
    const bodyKeys = ['limit', 'outputId', 'resourceType', 'snapshotToken'];
    return entry.origin === origin && entry.path === path && entry.failure === 'net::ERR_ABORTED' &&
      entry.body && typeof entry.body === 'object' && !Array.isArray(entry.body) &&
      JSON.stringify(Object.keys(entry.body).sort()) === JSON.stringify(bodyKeys) &&
      entry.body.snapshotToken === snapshotToken && entry.body.outputId === outputId &&
      entry.body.resourceType === 'Observation' && entry.body.limit === 50 &&
      cancellation?.browserRequestId === entry.browserRequestId && nonempty(cancellation.requestId) &&
      cancellation.playwrightRequestId === cancellation.requestId &&
      cancellation.method === 'POST' && cancellation.url === `${origin}${path}` &&
      cancellation.reason === 'Group Cancel retires its exact pending GroupCodedValuePicker source-options request.' &&
      proof.action === 'Group Cancel' && proof.project === project && proof.explorer === explorer &&
      proof.outputId === outputId && proof.snapshotToken === snapshotToken &&
      request?.method === 'POST' && request.resourceType === 'Observation' && request.limit === 50 &&
      proof.owner === 'ConstructionReshapeEditor GroupCodedValuePicker AbortController on Cancel unmount' &&
      requestAction?.id && requestAction.label === entry.triggerAction && /^Combine rows into groups\b/.test(requestAction.label) &&
      failureAction?.id && failureAction.label === 'Cancel' &&
      proof.responseStatus === (entry.status ?? null) && proof.responseReceivedAt === (entry.responseReceivedAt ?? null);
  } catch {
    return false;
  }
}

export function authoredColumnIds(columns) {
  if (!Array.isArray(columns) || columns.length === 0) return undefined;
  const ids = columns.map(column => column?.columnId);
  const outputColumns = columns.map(column => column?.column);
  if (!ids.every(nonempty) || new Set(ids).size !== ids.length ||
    !outputColumns.every(nonempty) || new Set(outputColumns).size !== outputColumns.length) return undefined;
  return ids;
}

export function hasExactAuthoredColumnRestoration(sourceColumns, restoredColumns) {
  const sourceIds = authoredColumnIds(sourceColumns);
  const restoredIds = authoredColumnIds(restoredColumns);
  return sourceIds !== undefined && restoredIds !== undefined &&
    isDeepStrictEqual(sourceIds, restoredIds) && isDeepStrictEqual(sourceColumns, restoredColumns);
}

function assertSourceScope({ project, generation }) {
  assert(nonempty(project), 'A numeric Group Filter oracle requires the exact project.');
  assert(nonempty(generation), 'A numeric Group Filter oracle requires the pinned generation.');
}

const observationScope = ({ project, generation }) => `r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation"
      AND IS_STRING(r.payload.subject.reference) AND LENGTH(TRIM(r.payload.subject.reference)) > 0
      AND IS_STRING(r.payload.status) AND LENGTH(TRIM(r.payload.status)) > 0
      AND IS_ARRAY(r.payload.code.coding) AND LENGTH(r.payload.code.coding) > 0
      AND IS_STRING(r.payload.code.coding[0].code) AND LENGTH(TRIM(r.payload.code.coding[0].code)) > 0`;
const observationProjection = `RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,
      resourceType:r.payload.resourceType,subjectReference:r.payload.subject.reference,status:r.payload.status,
      codeCodingCodes:(IS_ARRAY(r.payload.code.coding) ? r.payload.code.coding[*].code : [])}`;

export function buildCdaGroupNumericFilterInitialQuery({ project, generation }) {
  assertSourceScope({ project, generation });
  return `FOR r IN Observation FILTER ${observationScope({ project, generation })}
    SORT r._id LIMIT ${CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT} ${observationProjection}`;
}

export function buildCdaGroupNumericFilterTargetedQuery({ project, generation, selectedSubjects }) {
  assertSourceScope({ project, generation });
  assert(Array.isArray(selectedSubjects) && selectedSubjects.length > 0 && selectedSubjects.length <= 10,
    'The targeted Group Filter source query requires one to ten selected subjects.');
  assert(selectedSubjects.every(nonempty) && new Set(selectedSubjects).size === selectedSubjects.length,
    'Selected subject references must be unique nonempty strings.');
  return `FOR r IN Observation FILTER ${observationScope({ project, generation })}
      AND r.payload.subject.reference IN ${JSON.stringify(selectedSubjects)}
      SORT r._id LIMIT ${CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT} ${observationProjection}`;
}

export function buildCdaGroupNumericFilterExactReadQuery({ project, generation, exactIds }) {
  assertSourceScope({ project, generation });
  assert(Array.isArray(exactIds) && exactIds.length === 4 && exactIds.every(nonempty) && new Set(exactIds).size === 4,
    'The exact Group Filter witness must contain four distinct Observation document IDs.');
  return `FOR r IN Observation FILTER r._id IN ${JSON.stringify(exactIds)}
      AND r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation" SORT r._id ${observationProjection}`;
}

export function prepareCdaGroupNumericFilterOracle(witness, { project, generation, operator = 'GT', threshold = 1, requireStrictSubset = true }) {
  assertSourceScope({ project, generation });
  assert(supportedOperators.has(operator), `Unsupported numeric Group Filter operator ${operator}.`);
  assert(Number.isSafeInteger(threshold) && threshold >= 0, 'The integer COUNT_ROWS threshold must be a nonnegative safe integer.');
  const grouped = prepareCdaGroupPivotJoinOracle(witness, { project, generation });
  const groupRows = grouped.leftGroups.map(([subject, count]) => [subject, String(count)]);
  const compare = value => operator === 'GT' ? value > threshold
    : operator === 'GTE' ? value >= threshold
      : operator === 'LT' ? value < threshold : value <= threshold;
  const filteredRows = grouped.leftGroups
    .filter(([, count]) => compare(count))
    .map(([subject, count]) => [subject, String(count)]);

  assert.equal(groupRows.length, 2, 'The bounded witness must retain exactly two numeric Group rows.');
  assert.deepEqual(groupRows.map(row => Number(row[1])).sort((left, right) => left - right), [1, 3],
    'The bounded witness must independently derive COUNT_ROWS values 1 and 3.');
  if (requireStrictSubset) assert(filteredRows.length > 0 && filteredRows.length < groupRows.length,
    'The numeric summary Filter must retain a nonempty strict subset of the raw Group rows.');
  assert(filteredRows.every(row => Number.isSafeInteger(Number(row[1])) && compare(Number(row[1]))),
    'Every retained Group row must satisfy the numeric comparison.');
  return {
    groupRows,
    filteredRows,
    groupCounts: grouped.leftGroups.map(([subject, count]) => ({ subject, count })),
    operator,
    threshold,
  };
}
