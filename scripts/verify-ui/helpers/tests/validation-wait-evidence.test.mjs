import assert from 'node:assert/strict';
import test from 'node:test';
import { captureValidationWaitFailure, refreshValidationWaitFailureRequests } from '../validation-wait-evidence.mjs';

const proposalPath = '/api/v1/projects/cda/explorers/pivot/authoring/v2/construction-proposals';

test('a slow expected-validation wait is recorded as failure with current UI and pending request', () => {
  const requests = [{
    requestId: 'request-1', endpoint: 'construction-proposals', pathname: proposalPath,
    requestStartedAtMs: 100, body: { outputId: 'out-1' },
  }];
  const evidence = captureValidationWaitFailure({
    requests, requestOffset: 0, pathname: proposalPath, name: 'Initial Pivot validation', timeoutMs: 5000,
    error: new Error('timed out waiting for browser condition'),
    visibleState: { proposalStatus: 'validating', alert: null, policy: 'ERROR' },
    capturedAt: '2026-10-04T12:00:00.000Z',
  });
  assert.equal(evidence.classification, 'unexpected-validation-wait-failure');
  assert.equal(evidence.expectedValidation, false);
  assert.equal(evidence.timeoutMs, 5000);
  assert.deepEqual(evidence.visibleState, { proposalStatus: 'validating', alert: null, policy: 'ERROR' });
  assert.deepEqual(evidence.requestIDs, ['request-1']);
  assert.deepEqual(evidence.requests, [{
    requestId: 'request-1', endpoint: 'construction-proposals', pathname: proposalPath,
    status: null, requestStartedAtMs: 100, responseStartedAtMs: null, responseFinishedAtMs: null,
    durationMs: null, response: null, responseReadError: null, loadingFailure: null,
  }]);
});

test('a response arriving before report finalization is retained exactly but never reclassified as expected', () => {
  const response = { error: { code: 'PREVIEW_TIMEOUT', requestId: 'api-request-2' }, diagnostics: [] };
  const requests = [{
    requestId: 'request-2', endpoint: 'construction-proposals', pathname: proposalPath,
    requestStartedAtMs: 100, body: { outputId: 'out-1' },
  }];
  const evidence = captureValidationWaitFailure({
    requests, requestOffset: 0, pathname: proposalPath, name: 'Initial Pivot validation', timeoutMs: 5000,
    error: new Error('timed out waiting for browser condition'), visibleState: { proposalStatus: 'validating' },
  });
  Object.assign(requests[0], { status: 504, responseStartedAtMs: 10100, responseFinishedAtMs: 10102, durationMs: 10002, response });
  refreshValidationWaitFailureRequests(evidence, requests);
  assert.equal(evidence.expectedValidation, false);
  assert.equal(evidence.requests[0].status, 504);
  assert.deepEqual(evidence.requests[0].response, response);
  assert.equal(evidence.requests[0].durationMs, 10002);
});
