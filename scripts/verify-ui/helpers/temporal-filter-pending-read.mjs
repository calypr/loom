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

export const validateTemporalPendingReadScope = (request, scope, builderReads) => {
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

  const facts = requestSnapshot(request);
  assert.equal(facts.outputId, scope.outputId,
    'Pending temporal read must be bound to the actual output.');
  assert(typeof facts.snapshotToken === 'string' && facts.snapshotToken.length > 0,
    'Pending temporal read must retain its actual snapshot token.');
  assert(Number.isInteger(facts.draftVersion) && facts.draftVersion > 0,
    'Pending temporal read must retain its actual saved draft version.');
  assert(typeof facts.draftDigest === 'string' && facts.draftDigest.length > 0,
    'Pending temporal read must retain its actual saved draft digest.');
  const matchingReads = builderReads.filter(read => read.status === 200 &&
    read.snapshotToken === facts.snapshotToken &&
    read.draftVersion === facts.draftVersion &&
    read.draftDigest === facts.draftDigest &&
    Array.isArray(read.workspaceDocuments) &&
    read.workspaceDocuments.some(document => document?.output?.id === scope.outputId));
  assert(matchingReads.length > 0,
    'Pending temporal read CAS and output must match an actual successful saved Builder read.');
  return { ...facts, matchingBuilderReadIds: matchingReads.map(read => read.readId) };
};

export const temporalPendingReadSuffixes = PENDING_READ_SUFFIXES;
