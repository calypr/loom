import assert from 'node:assert/strict';
import { fixtureProjectExplorerCollectionPath } from './fixture-context.mjs';

export const TEMPORAL_CAPTURE_DRAIN_TIMEOUT_MS = 5_000;

const PENDING_READ_SUFFIXES = Object.freeze([
  '/authoring/v2/construction-capabilities',
  '/authoring/v2/semantic-inventory',
]);

const requestSnapshot = request => {
  const caps = request.path.endsWith('/authoring/v2/construction-capabilities');
  const body = request.request;
  const presentation = body?.fieldPresentation;
  return {
    routeSuffix: caps
      ? '/authoring/v2/construction-capabilities'
      : '/authoring/v2/semantic-inventory',
    snapshotToken: body?.snapshotToken,
    draftVersion: caps ? body?.expectedDraftVersion : presentation?.expectedDraftVersion,
    draftDigest: caps ? body?.expectedDraftDigest : presentation?.expectedDraftDigest,
    outputId: body?.outputId,
  };
};

const validateTemporalReadRoute = (request, scope, builderReads) => {
  assert(request && typeof request === 'object', 'Pending temporal read must retain its raw observer request.');
  assert(scope && typeof scope === 'object', 'Pending temporal read must retain its capture owner scope.');
  assert(Array.isArray(builderReads), 'Pending temporal read must retain the captured Builder reads.');
  assert(PENDING_READ_SUFFIXES.some(suffix => request.path === `${scope.browserExplorerRoot}${suffix}`),
    'Pending temporal read must use an explicitly supported read route under the owned Explorer.');
  assert.equal(scope.browserExplorerRoot,
    `${fixtureProjectExplorerCollectionPath(scope.project)}/${encodeURIComponent(scope.explorerId)}`,
    'Pending temporal read scope must use the exact double-encoded owned project and Explorer route.');
  assert.equal(request.origin, scope.uiOrigin,
    'Pending temporal read must retain the owned browser proxy origin.');
  assert.equal(request.method, 'POST');
  assert.equal(request.isMutation, false,
    'Pending temporal read cannot be observer-classified as a mutation.');
  const facts = requestSnapshot(request);
  assert.equal(facts.outputId, scope.outputId,
    'Pending temporal read must be bound to the actual output.');
  assert(typeof facts.snapshotToken === 'string' && facts.snapshotToken.length > 0,
    'Pending temporal read must retain its actual snapshot token.');
  const matchingReads = builderReads.filter(read => read.status === 200 &&
    read.snapshotToken === facts.snapshotToken &&
    Array.isArray(read.workspaceDocuments) &&
    read.workspaceDocuments.some(document => document?.output?.id === scope.outputId));
  assert(matchingReads.length > 0,
    'Pending temporal read snapshot and output must match an actual successful saved Builder read.');
  return { ...facts, matchingBuilderReadIds: matchingReads.map(read => read.readId) };
};

export const validateTemporalPendingReadScope = (request, scope, builderReads) => {
  const facts = validateTemporalReadRoute(request, scope, builderReads);
  assert.equal(request.settlement, 'pending',
    'Pending temporal read must retain an unresolved network settlement.');
  assert.equal(request.status, null,
    'Pending temporal read cannot claim an HTTP status.');
  assert.equal(request.networkComplete, false,
    'Pending temporal read cannot claim a completed network request.');
  assert.equal(request.responseCapture, 'not-configured',
    'Pending temporal read cannot claim a partial response-body read.');
  assert.equal(request.responseCaptureComplete, true,
    'Pending temporal read cannot leave a response-body capture open.');
  assert.equal(request.responseBodyCaptured, false,
    'Pending temporal read cannot claim a captured response body.');
  assert.equal(request.responseCaptureError ?? null, null,
    'Pending temporal read cannot hide a response-body capture failure.');
  assert.equal(request.response, null,
    'Pending temporal read cannot contain response facts.');
  assert.equal(request.responseReceivedAt, null,
    'Pending temporal read cannot claim observed response headers.');
  assert.equal(request.completedAt, null,
    'Pending temporal read cannot claim a terminal completion time.');
  assert.equal(request.responseMountEpoch, null,
    'Pending temporal read cannot claim a response owner mount.');
  assert.equal(request.failure, null,
    'Pending temporal read cannot be relabeled as an observed request failure.');
  assert.equal(request.failureMountEpoch, null,
    'Pending temporal read cannot claim a failure mount.');
  assert.equal(request.readCancellationDiagnostic, false,
    'Pending temporal read cannot be relabeled as an observed cancellation.');

  assert(Number.isInteger(facts.draftVersion) && facts.draftVersion > 0,
    'Pending temporal read must retain its actual saved draft version.');
  assert(typeof facts.draftDigest === 'string' && facts.draftDigest.length > 0,
    'Pending temporal read must retain its actual saved draft digest.');
  const matchingCASReads = builderReads.filter(read => read.status === 200 &&
    read.snapshotToken === facts.snapshotToken &&
    read.draftVersion === facts.draftVersion &&
    read.draftDigest === facts.draftDigest &&
    Array.isArray(read.workspaceDocuments) &&
    read.workspaceDocuments.some(document => document?.output?.id === scope.outputId));
  assert(matchingCASReads.length > 0,
    'Pending temporal read CAS and output must match an actual successful saved Builder read.');
  return { ...facts, matchingBuilderReadIds: matchingCASReads.map(read => read.readId) };
};

export const validateTemporalResponseBodyFailureScope = (request, scope, builderReads) => {
  const facts = validateTemporalReadRoute(request, scope, builderReads);
  assert.equal(request.status >= 200 && request.status < 300, true,
    'A response-body abort can retain only an observed successful HTTP response header.');
  assert(Number.isFinite(request.responseReceivedAt) && Number.isFinite(request.completedAt) &&
    request.startedAt <= request.responseReceivedAt && request.responseReceivedAt <= request.completedAt,
  'The observed response header and failed body must preserve request chronology.');
  assert(Number.isInteger(request.responseMountEpoch) && Number.isInteger(request.failureMountEpoch),
    'A response-body abort must preserve both actual response and failure mount epochs.');
  assert.equal(request.networkComplete, true,
    'A failed response-body read must retain the actual terminal transport event.');
  assert.equal(request.failure?.errorText, 'net::ERR_ABORTED',
    'Only the observed browser abort is accepted for a failed safe-read body.');
  assert.equal(request.failure?.canceled, true);
  assert.equal(request.readCancellationDiagnostic, true);
  assert.equal(request.responseCapture, 'failed');
  assert.equal(request.responseCaptureComplete, true);
  assert.equal(request.responseBodyCaptured, false);
  assert(typeof request.responseCaptureError === 'string' && request.responseCaptureError.length > 0,
    'A failed response-body read must retain the actual body-capture error.');
  assert.equal(request.response, null,
    'A failed response-body read cannot retain response facts from an unavailable body.');
  return facts;
};

export const temporalPendingReadSuffixes = PENDING_READ_SUFFIXES;
