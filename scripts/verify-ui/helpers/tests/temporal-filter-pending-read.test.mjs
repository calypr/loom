import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  validateTemporalPendingReadScope,
  validateTemporalResponseBodyFailureScope,
} from '../temporal-filter-pending-read.mjs';
import { adaptTemporalFilterJourney } from '../temporal-filter-trace-adapter.mjs';
import { compileTemporalFilterJourneyTrace } from '../temporal-filter-tla-adapter.mjs';
import {
  makeTemporalFilterJourneyCaptureFixture,
  makeTemporalResponseBodyAbortFixture,
} from './fixtures/temporal-filter-journey-schema-fixture.mjs';

const retained = JSON.parse(readFileSync(new URL(
  './fixtures/basic-temporal-filter-run4-pending-capability.json', import.meta.url,
), 'utf8'));
const pending = retained.pendingRequest;
const scope = retained.ownerScope;
const builderReads = [retained.builderReadProjection];

test('retained DATE request 70 stays pending when distinct request 76 succeeds', () => {
  assert.equal(retained.sourceRun, 'bf7532025-date-20261025-run4');
  assert.equal(pending.index, 70);
  assert.equal(pending.browserRequestId, 'playwright-71');
  assert.equal(pending.requestId, 'construction-capabilities-6da28800-fa44-4dae-9b23-2c8e43788e3d');
  assert.equal(retained.distinctLaterResponse.index, 76);
  assert.equal(retained.distinctLaterResponse.browserRequestId, 'playwright-77');
  assert.equal(retained.distinctLaterResponse.status, 200);
  assert.notEqual(pending.browserRequestId, retained.distinctLaterResponse.browserRequestId);
  assert.notEqual(pending.requestId, retained.distinctLaterResponse.requestId);

  assert.match(retained.builderReadProjection.projectionNote, /truncated/u,
    'The focused retained fixture does not claim the unavailable oversized Builder response body.');
  const facts = validateTemporalPendingReadScope(pending, scope, builderReads);
  assert.equal(facts.outputId, scope.outputId);
  assert.equal(facts.draftVersion, 6);
  assert.deepEqual(facts.matchingBuilderReadIds, [retained.builderReadProjection.readId]);
  assert.equal(pending.response, null);
  assert.equal(pending.status, null,
    'The later successful capability response cannot settle or add status to physical request 70.');
});

test('pending temporal reads reject foreign project/Explorer, observed headers, and response credit', () => {
  const wrongProject = structuredClone(pending);
  wrongProject.path = wrongProject.path.replace('/loom_dev_verify_mvo4s9f6%252F9ed2356/', '/foreign-project/');
  assert.throws(() => validateTemporalPendingReadScope(wrongProject, scope, builderReads), /supported read route under the owned Explorer/u);

  const wrongExplorer = structuredClone(pending);
  wrongExplorer.path = wrongExplorer.path.replace('/verify-f6-9ed2356-patient-birthdate-filter/', '/unowned-explorer/');
  assert.throws(() => validateTemporalPendingReadScope(wrongExplorer, scope, builderReads), /supported read route under the owned Explorer/u);

  const headerOnly = structuredClone(pending);
  headerOnly.responseReceivedAt = pending.startedAt + 1;
  assert.throws(() => validateTemporalPendingReadScope(headerOnly, scope, builderReads), /observed response headers/u);

  const partialBody = structuredClone(pending);
  partialBody.responseCapture = 'pending';
  partialBody.responseCaptureComplete = false;
  assert.throws(() => validateTemporalPendingReadScope(partialBody, scope, builderReads), /partial response-body read/u);

  const creditedFromLater = structuredClone(pending);
  creditedFromLater.response = retained.distinctLaterResponse.responseFacts;
  assert.throws(() => validateTemporalPendingReadScope(creditedFromLater, scope, builderReads), /cannot contain response facts/u);

  const pendingMutation = structuredClone(pending);
  pendingMutation.isMutation = true;
  assert.throws(() => validateTemporalPendingReadScope(pendingMutation, scope, builderReads), /cannot be observer-classified as a mutation/u);
});

test('pending temporal reads reject mismatched saved CAS and output', () => {
  const wrongCASRead = [{ ...retained.builderReadProjection, draftDigest: 'other-digest' }];
  assert.throws(() => validateTemporalPendingReadScope(pending, scope, wrongCASRead), /match an actual successful saved Builder read/u);

  const wrongOutput = structuredClone(pending);
  wrongOutput.request.outputId = 'other-output';
  assert.throws(() => validateTemporalPendingReadScope(wrongOutput, scope, builderReads), /bound to the actual output/u);
});

test('response-header body aborts stay scoped to an owned read and retain no body facts', () => {
  const capture = makeTemporalResponseBodyAbortFixture('date');
  const request = capture.requestEvents.at(-1);
  const facts = validateTemporalResponseBodyFailureScope(request, capture.pendingReadScope, capture.builderReads);
  assert.equal(facts.outputId, capture.outputId);
  assert.equal(facts.snapshotToken, capture.builderReads.at(-1).snapshotToken);
  assert.equal(request.status, 200);
  assert.equal(request.response, null);

  const wrongProject = structuredClone(request);
  wrongProject.path = wrongProject.path.replace('loom_dev_verify_temporal', 'loom_dev_verify_foreign');
  assert.throws(() => validateTemporalResponseBodyFailureScope(wrongProject, capture.pendingReadScope,
    capture.builderReads), /supported read route under the owned Explorer/u);

  const failedMutation = structuredClone(request);
  failedMutation.isMutation = true;
  assert.throws(() => validateTemporalResponseBodyFailureScope(failedMutation, capture.pendingReadScope,
    capture.builderReads), /cannot be observer-classified as a mutation/u);

  const failedErrorResponse = structuredClone(request);
  failedErrorResponse.status = 503;
  assert.throws(() => validateTemporalResponseBodyFailureScope(failedErrorResponse, capture.pendingReadScope,
    capture.builderReads), /successful HTTP response header/u);

  const missingBodyError = structuredClone(request);
  delete missingBodyError.responseCaptureError;
  assert.throws(() => validateTemporalResponseBodyFailureScope(missingBodyError, capture.pendingReadScope,
    capture.builderReads), /actual body-capture error/u);
});

test('an unregistered read POST and an unconsumed mutation cannot disappear from a Temporal trace', () => {
  const makeCaptureWithExtraRequest = isMutation => {
    const capture = makeTemporalFilterJourneyCaptureFixture('date');
    const startedAt = Math.max(...capture.requestEvents.map(request => request.startedAt)) + 10;
    const request = {
      index: capture.requestEvents.length,
      browserRequestId: `extra-${isMutation ? 'write' : 'read'}`,
      requestId: `extra-${isMutation ? 'write' : 'read'}-wire`,
      method: 'POST',
      origin: capture.uiOrigin,
      path: '/api/v1/projects/loom_dev_verify_temporal/unregistered-endpoint',
      status: 200,
      request: isMutation ? { commands: [{ type: 'UNKNOWN_COMMAND' }] } : {},
      response: {},
      startedAt,
      responseReceivedAt: startedAt + 1,
      completedAt: startedAt + 2,
      responseMountEpoch: 5,
      failureMountEpoch: null,
      isMutation,
      failure: null,
      readCancellationDiagnostic: false,
      responseCapture: 'captured',
      responseCaptureComplete: true,
      responseBodyCaptured: true,
      networkComplete: true,
      settlement: 'response',
      atMonotonicMs: startedAt - 1_700_000_000_000,
      atEpochMs: startedAt,
      mountEpoch: 5,
    };
    capture.requestEvents.push(request);
    capture.captureExit.requestCount = capture.requestEvents.length;
    capture.captureExit.afterObserverIndex = request.index;
    capture.captureExit.observedAtEpochMs = Math.max(capture.captureExit.observedAtEpochMs, request.completedAt);
    return capture;
  };

  const unknownRead = adaptTemporalFilterJourney(makeCaptureWithExtraRequest(false));
  assert.throws(() => compileTemporalFilterJourneyTrace(unknownRead), /no temporal-filter path allowlist entry/u);

  assert.throws(() => adaptTemporalFilterJourney(makeCaptureWithExtraRequest(true)),
    /Every observer-classified write must be consumed exactly once/u);
});
