import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { classifyExpectedCdaCancellation } from '../cda-fixtures.mjs';
import {
  hasCancellableGroupSourceOptionsResponseState,
  isExactGroupSourceOptionsCancellation,
  matchesExactGroupSourceOptionsRequest,
} from '../cda-group-numeric-filter-oracle.mjs';

const target = {
  uiOrigin: 'http://127.0.0.1:30008',
  project: 'loom_dev_cda_fhir',
  explorer: 'cda-group-numeric-filter-df2d0644-abd8-4ad7-bb9b-080a50bac891',
  outputId: 'out_12ce9c118bd68c92e278d73a',
  snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7',
};
const path = `/api/v1/projects/${target.project}/explorers/${target.explorer}/authoring/v2/frame-source-options`;
const action = 'Group Cancel';
const reason = 'Group Cancel retires its exact pending GroupCodedValuePicker source-options request.';

const makeRequest = ({ origin = target.uiOrigin, project = target.project, explorer = target.explorer,
  outputId = target.outputId, snapshotToken = target.snapshotToken, resourceType = 'Observation', limit = 50,
  method = 'POST', headers = {} } = {}) => {
  const body = { snapshotToken, outputId, resourceType, limit };
  return {
    method: () => method,
    url: () => `${origin}/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/frame-source-options`,
    headers: () => headers,
    postData: () => JSON.stringify(body),
    postDataJSON: () => body,
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  };
};

const expectedRequest = {
  uiOrigin: target.uiOrigin,
  project: target.project,
  explorer: target.explorer,
  outputId: target.outputId,
  snapshotToken: target.snapshotToken,
};

test('the Group Cancel matcher binds the actual headerless Observation source-options wire shape', () => {
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest(), expectedRequest), true);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ origin: 'http://127.0.0.1:8188' }), expectedRequest), false);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ project: 'other-project' }), expectedRequest), false);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ explorer: 'other-explorer' }), expectedRequest), false);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ outputId: 'out_other' }), expectedRequest), false);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ snapshotToken: 'sha256:stale' }), expectedRequest), false);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ resourceType: 'Specimen' }), expectedRequest), false);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ limit: 49 }), expectedRequest), false);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ method: 'GET' }), expectedRequest), false);
  assert.equal(matchesExactGroupSourceOptionsRequest(makeRequest({ headers: { 'x-request-id': 'not-the-retained-request' } }), expectedRequest), false);
});

test('the exact Group Cancel request accepts only terminal aborts before headers or after HTTP 200', () => {
  assert.equal(hasCancellableGroupSourceOptionsResponseState({
    failure: 'net::ERR_ABORTED', completedAt: 1791473523038,
  }), true, 'the retained request was canceled before receiving response headers');
  assert.equal(hasCancellableGroupSourceOptionsResponseState({
    failure: 'net::ERR_ABORTED', status: 200, responseReceivedAt: 1791472326497, completedAt: 1791472326504,
  }), true, 'the prior retained timing reached HTTP 200 before body-read cancellation');
  assert.equal(hasCancellableGroupSourceOptionsResponseState({
    failure: 'net::ERR_ABORTED', status: 503, responseReceivedAt: 100, completedAt: 101,
  }), false, 'a non-200 response must remain fatal');
  assert.equal(hasCancellableGroupSourceOptionsResponseState({
    failure: 'net::ERR_ABORTED', status: 200, completedAt: 101,
  }), false, 'HTTP 200 requires its response event timestamp');
  assert.equal(hasCancellableGroupSourceOptionsResponseState({
    failure: 'net::ERR_ABORTED', responseReceivedAt: 100, completedAt: 101,
  }), false, 'a missing status cannot carry a response event timestamp');
  assert.equal(hasCancellableGroupSourceOptionsResponseState({
    failure: 'net::ERR_FAILED', completedAt: 101,
  }), false, 'non-abort failures remain fatal');
  assert.equal(hasCancellableGroupSourceOptionsResponseState({ failure: 'net::ERR_ABORTED' }), false,
    'an abort without terminal capture evidence remains fatal');
});

test('the workflow snapshots the exact request object before Group Cancel and classifies only its terminal abort', () => {
  const workflow = readFileSync(new URL('../../workflows/verify-cda-group-numeric-filter-browser.mjs', import.meta.url), 'utf8');
  const cancelBlock = workflow.slice(workflow.indexOf('const cancelProposal ='), workflow.indexOf('const rawIdRows ='));
  assert.match(cancelBlock, /capture\.byRequest\.entries\(\)/);
  assert.match(cancelBlock, /matchesExactGroupSourceOptionsRequest\(request/);
  assert.match(cancelBlock, /requestTerminalEvents\.has\(request\)/);
  assert.match(cancelBlock, /cda\.expectCapturedCancellation\(entry/);
  assert.match(cancelBlock, /diagnostic\.failureAction\?\.label, 'Cancel'/);
  assert.match(cancelBlock, /hasCancellableGroupSourceOptionsResponseState\(entry\)/);
  assert.match(cancelBlock, /canceledSavedView\(before, expectedRows, name\)/);
  assert.match(workflow, /isExactGroupSourceOptionsCancellation\(entry/,
    'the final terminal-request census must exempt only the exact approved Group Cancel record');
  assert.match(workflow, /entry\.status === 200 && Number\.isFinite\(entry\.completedAt\) && entry\.failure === undefined/,
    'a response with a failed body or transport must not count as successful merely because status was 200');
  assert.match(workflow, /page\.off\('requestfinished', onNativeRequestFinished\)/);
  assert.match(workflow, /page\.off\('requestfailed', onNativeRequestFailed\)/);
  assert.match(workflow, /page\.once\('close', removeNativeRequestTerminalListeners\)/);

  const observations = [
    {
      requestId: 'playwright-22', browserRequestId: 'playwright-22', method: 'POST',
      origin: target.uiOrigin, path, body: {
        snapshotToken: target.snapshotToken, outputId: target.outputId, resourceType: 'Observation', limit: 50,
      },
      startedAt: 1791473522172,
      triggerAction: 'Combine rows into groups Make one row for each combination of matching values in the fields you choose. Example: 3 rows with Status=active and Unit=north → 1 group row for active + north',
      completedAt: 1791473523038, failure: 'net::ERR_ABORTED',
    },
    {
      requestId: 'playwright-23', browserRequestId: 'playwright-23', method: 'POST',
      origin: target.uiOrigin, path, body: {
        snapshotToken: target.snapshotToken, outputId: target.outputId, resourceType: 'Observation', limit: 50,
      },
      startedAt: 1791472325564,
      triggerAction: 'Combine rows into groups Make one row for each combination of matching values in the fields you choose. Example: 3 rows with Status=active and Unit=north → 1 group row for active + north',
      status: 200, responseReceivedAt: 1791472326497, completedAt: 1791472326504, failure: 'net::ERR_ABORTED',
    },
  ];

  for (const [index, entry] of observations.entries()) {
    const request = makeRequest();
    const diagnosticRequestId = index === 0 ? 'cda-request-133' : 'cda-request-134';
    const error = { kind: 'network', browserRequestId: entry.browserRequestId, error: entry.failure };
    const report = { nativeRequests: [entry], network: [error], errors: [error], expectedCancellations: [] };
    const requestFailures = new WeakMap([[request, {
      requestId: diagnosticRequestId, playwrightRequestId: diagnosticRequestId,
      method: entry.method, url: `${entry.origin}${entry.path}`, errorText: entry.failure,
    }]]);
    const tracker = { byRequest: new Map([[request, entry]]) };
    const trackers = new Set([tracker]);
    assert.equal(matchesExactGroupSourceOptionsRequest(request, expectedRequest), true);

    const cancellation = classifyExpectedCdaCancellation({
      request, reason, proof: {
        action, project: target.project, explorer: target.explorer, outputId: target.outputId,
        snapshotToken: target.snapshotToken, request: { method: 'POST', resourceType: 'Observation', limit: 50 },
        owner: 'ConstructionReshapeEditor GroupCodedValuePicker AbortController on Cancel unmount',
        requestAction: { id: 'cda-action-10', label: entry.triggerAction },
        failureAction: { id: 'cda-action-12', label: 'Cancel' },
        responseStatus: entry.status ?? null, responseReceivedAt: entry.responseReceivedAt ?? null,
      }, report, requestFailures, trackers,
    });
    assert.equal(cancellation.browserRequestId, entry.browserRequestId);
    assert.equal(cancellation.requestId, diagnosticRequestId);
    assert.notEqual(cancellation.requestId, entry.requestId,
      'the cancellation diagnostic ID must remain correlated to the exact headerless browser request ID');
    assert.equal(entry.failure, 'net::ERR_ABORTED', 'classification must retain the terminal abort');
    assert.equal(error.expected, true, 'only the same captured request receives the expected marker');
    assert.equal(isExactGroupSourceOptionsCancellation(entry, expectedRequest), true,
      'only the exact action-bound retained record is omitted from non-success census');
  }

  const unclassified = {
    ...observations[0], expected: true, canceled: undefined, expectedCancellation: undefined,
  };
  assert.equal(isExactGroupSourceOptionsCancellation(unclassified, expectedRequest), false,
    'an arbitrary expected boolean cannot hide an unclassified terminal abort');
  const wrongOutput = {
    ...observations[0], expected: true, canceled: true,
    body: { ...observations[0].body, outputId: 'out_other' },
  };
  assert.equal(isExactGroupSourceOptionsCancellation(wrongOutput, expectedRequest), false,
    'a terminal cancellation for a different output must remain in non-success census');
  const wrongBrowserRequest = {
    ...observations[0],
    expectedCancellation: {
      ...observations[0].expectedCancellation,
      browserRequestId: 'playwright-other',
    },
  };
  assert.equal(isExactGroupSourceOptionsCancellation(wrongBrowserRequest, expectedRequest), false,
    'a diagnostic request ID cannot exempt a different browser request from the non-success census');
});
