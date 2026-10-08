import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  captureCDARequests,
  findCompletedNativeResponse,
  matchesExpectedEmptyCollectionValidation,
  matchesExpectedEmptyCollectionValidationConsole,
  matchesNativeConstructionRemovalProposal,
} from '../cda-playwright-requests.mjs';
import { sanitizePayload, sanitizeReportPayload } from '../playwright-browser.mjs';
import { createPendingResponseReads } from '../pending-response-reads.mjs';
import { expectedRelatedSourceOneValidation } from '../related-source-capture.mjs';
import { classifyExpectedCdaCancellation } from '../cda-fixtures.mjs';

test('completed proposal selection skips an unrelated removal and binds the exact step, output, and draft', () => {
  const expected = {
    stepId: 'related_expand_patient_observation', outputId: 'out-owned', snapshotToken: 'snapshot-owned',
    draftVersion: 9, draftDigest: 'digest-owned',
  };
  const makeEntry = (stepId) => ({
    complete: true,
    method: 'POST',
    path: '/api/v1/projects/owned/explorers/owned/authoring/v2/construction-proposals',
    request: {
      removeStepIds: [stepId], outputId: expected.outputId, snapshotToken: expected.snapshotToken,
      expectedDraftVersion: expected.draftVersion, expectedDraftDigest: expected.draftDigest,
    },
  });
  const unrelated = makeEntry('step-unrelated');
  const exact = makeEntry(expected.stepId);
  const responseFor = (entry) => ({
    proposalId: entry === exact ? 'proposal-exact' : 'proposal-unrelated',
    outputId: expected.outputId, snapshotToken: expected.snapshotToken,
    draftVersion: expected.draftVersion, draftDigest: expected.draftDigest,
  });
  const matches = (entry, response) => matchesNativeConstructionRemovalProposal(entry, response, expected);

  assert.equal(matchesNativeConstructionRemovalProposal(exact, responseFor(exact), expected), true);
  assert.equal(matchesNativeConstructionRemovalProposal(unrelated, responseFor(unrelated), expected), false);
  assert.equal(findCompletedNativeResponse([unrelated, exact], responseFor, matches), exact);
  assert.equal(findCompletedNativeResponse([unrelated], responseFor, matches), undefined);
  assert.equal(matchesNativeConstructionRemovalProposal(exact, {
    ...responseFor(exact), draftDigest: 'digest-stale',
  }, expected), false);
});

test('empty-collection validation classification binds the exact draft, row choice, output, policy, and diagnostic', () => {
  const expected = {
    path: '/api/v1/projects/owned/explorers/owned/authoring/v2/row-definition-proposals',
    snapshotToken: 'snapshot-owned', expectedDraftVersion: 8, expectedDraftDigest: 'digest-owned',
    outputId: 'out-owned', rowChoiceId: 'choice-owned', code: 'EMPTY_COLLECTION_ERROR',
    stage: 'row-definition-proposal',
  };
  const exact = {
    method: 'POST', path: expected.path, origin: 'http://127.0.0.1:30102',
    browserRequestId: 'playwright-expected', status: 422,
    body: {
      snapshotToken: expected.snapshotToken, expectedDraftVersion: expected.expectedDraftVersion,
      expectedDraftDigest: expected.expectedDraftDigest, outputId: expected.outputId,
      selection: { kind: 'EXPANDED', expanded: { rowChoiceId: expected.rowChoiceId, emptyCollectionPolicy: 'ERROR' } },
    },
    response: {
      error: { code: expected.code, diagnostic: { code: expected.code, stage: expected.stage } },
    },
  };
  assert.equal(matchesExpectedEmptyCollectionValidation(exact, expected), true);
  const consoleError = {
    kind: 'console', location: exact.origin + exact.path,
    message: 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)',
  };
  assert.equal(matchesExpectedEmptyCollectionValidationConsole(consoleError, exact, [exact], expected), true);
  const competingDraft = structuredClone(exact);
  competingDraft.browserRequestId = 'playwright-other-draft';
  competingDraft.body.expectedDraftVersion -= 1;
  assert.equal(matchesExpectedEmptyCollectionValidationConsole(consoleError, exact, [exact, competingDraft], expected), false,
    'A same-path/status console line cannot be assigned when another draft response has the same URL and status');
  const unfinishedRequest = { origin: exact.origin, path: exact.path, status: undefined };
  assert.equal(matchesExpectedEmptyCollectionValidationConsole(consoleError, exact, [exact, unfinishedRequest], expected), false,
    'A same-path request with no terminal response makes console ownership ambiguous');

  const mutations = [
    entry => { entry.body.expectedDraftVersion += 1; },
    entry => { entry.body.expectedDraftDigest = 'digest-stale'; },
    entry => { entry.body.selection.expanded.emptyCollectionPolicy = 'PRESERVE_PARENT'; },
    entry => { entry.response.error.code = 'INTERNAL_ERROR'; },
    entry => { entry.response.error.diagnostic.code = 'OTHER_VALIDATION'; },
    entry => { entry.body.outputId = 'out-other'; },
    entry => { entry.body.selection.expanded.rowChoiceId = 'choice-other'; },
    entry => { entry.status = 400; },
  ];
  for (const mutate of mutations) {
    const unrelated = structuredClone(exact);
    mutate(unrelated);
    assert.equal(matchesExpectedEmptyCollectionValidation(unrelated, expected), false);
  }
});

test('owned requests correlate sanitized responses and reject sibling explorer prefixes', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8188',
    ownedPathPrefix: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned',
    report,
  });
  const request = {
    url: () => 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/commands',
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'owned-request-1' }),
    postData: () => '{"commands":[]}',
    failure: () => null,
  };
  const sibling = {
    ...request,
    url: () => 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned-copy/authoring/v2/commands',
  };
  page.emit('request', request);
  page.emit('request', sibling);
  assert.equal(report.nativeRequests.length, 1);
  assert.equal(report.nativeRequests[0].requestId, 'owned-request-1');
  assert.equal(report.nativeRequests[0].body.commands.length, 0);

  const response = {
    request: () => request,
    status: () => 503,
    headers: () => ({ 'x-request-id': 'owned-response-1' }),
    text: async () => '{"error":"stale draft","token":"do-not-retain"}',
  };
  const waitingForResponse = capture.waitFor(entry => entry.path.endsWith('/commands') && entry.status === 503, { timeout: 1000 });
  page.emit('response', response);
  const matched = await waitingForResponse;
  await capture.flush();

  assert.equal(matched, report.nativeRequests[0]);
  assert.equal(report.nativeRequests.length, 1);
  assert.equal(report.nativeRequests[0].status, 503);
  assert.equal(report.nativeRequests[0].serverRequestId, 'owned-response-1');
  assert.deepEqual(report.nativeRequests[0].response, { error: 'stale draft', token: '[REDACTED]' });
  assert.equal(report.errors.length, 1);
  assert.equal(report.errors[0].kind, 'http');
  assert.equal(report.errors[0].status, 503);
  assert.equal(report.errors[0].requestId, 'owned-request-1');
  assert.equal(report.errors[0].browserRequestId, report.nativeRequests[0].browserRequestId);
  assert.equal(report.errors[0].method, 'POST');
  assert.equal(report.errors[0].path, report.nativeRequests[0].path);
  assert.deepEqual(report.errors[0].response, report.nativeRequests[0].response);
});

test('native request chronology records exact Playwright events and diagnoses response correlation misses', async () => {
  const draft6Body = {
    stageId: 'source_projection',
    expectedDraftVersion: 6,
    expectedDraftDigest: 'sha256:317a5915b25e30e75ef906a2727a18b7d7e409efbbe344e16129aeacd9d9b37f',
    outputId: 'out_56995fbd74c5cc10579a1fd2',
  };

  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8188',
    ownedPathPrefix: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned',
    report,
  });
  const path = 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/construction-capabilities';
  const body = JSON.stringify(draft6Body);
  const makeRequest = (requestId) => ({
    url: () => path,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': requestId, authorization: 'Bearer private-header' }),
    postData: () => body,
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  });

  const successfulRequest = makeRequest('draft6-success');
  page.emit('request', successfulRequest);
  page.emit('response', {
    request: () => successfulRequest,
    status: () => 200,
    headers: () => ({ 'x-request-id': 'server-private-id', 'set-cookie': 'private-cookie' }),
    text: async () => '{"draftVersion":6,"private":"response-payload"}',
  });
  page.emit('requestfinished', successfulRequest);

  const failedRequest = makeRequest('draft6-aborted');
  page.emit('request', failedRequest);
  page.emit('requestfailed', failedRequest);

  const mismatchedRequest = makeRequest('draft6-mismatch');
  page.emit('request', mismatchedRequest);
  const responseTwin = makeRequest('response-object-twin');
  page.emit('response', {
    request: () => responseTwin,
    status: () => 200,
    headers: () => ({ 'x-request-id': 'unmatched-private-id' }),
    text: async () => '{"private":"unmatched-response-payload"}',
  });

  await capture.flush();

  const [successfulEntry, failedEntry, mismatchedEntry] = report.nativeRequests;
  assert.equal(successfulEntry.status, 200);
  assert(Number.isFinite(successfulEntry.completedAt), 'a matched successful response must reach the completion path');
  assert.deepEqual(successfulEntry.nativeEventChronology.map(({ event, browserRequestId, objectMatch }) => ({ event, browserRequestId, objectMatch })), [
    { event: 'request', browserRequestId: 'playwright-1', objectMatch: true },
    { event: 'response', browserRequestId: 'playwright-1', objectMatch: true },
    { event: 'requestfinished', browserRequestId: 'playwright-1', objectMatch: true },
  ]);

  assert.equal(failedEntry.failure, 'net::ERR_ABORTED');
  assert(Number.isFinite(failedEntry.completedAt), 'requestfailed must retire a captured request');
  assert.deepEqual(failedEntry.nativeEventChronology.map(({ event, browserRequestId, objectMatch }) => ({ event, browserRequestId, objectMatch })), [
    { event: 'request', browserRequestId: 'playwright-2', objectMatch: true },
    { event: 'requestfailed', browserRequestId: 'playwright-2', objectMatch: true },
  ]);

  assert.equal(mismatchedEntry.status, undefined, 'a response from a different Request object must not be attached by URL alone');
  const correlationMiss = report.errors.find(error => error.kind === 'request-capture-correlation' && error.event === 'response');
  assert.deepEqual({
    kind: correlationMiss?.kind,
    event: correlationMiss?.event,
    browserRequestId: correlationMiss?.browserRequestId,
    method: correlationMiss?.method,
    path: correlationMiss?.path,
    objectMatch: correlationMiss?.objectMatch,
  }, {
    kind: 'request-capture-correlation',
    event: 'response',
    browserRequestId: null,
    method: 'POST',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/construction-capabilities',
    objectMatch: false,
  });

  const diagnostics = JSON.stringify({
    chronology: report.nativeRequests.flatMap(entry => entry.nativeEventChronology ?? []),
    correlationMiss,
  });
  for (const privateValue of ['private-header', 'private-cookie', 'private-response-payload', 'unmatched-response-payload', 'draft6-success', 'server-private-id']) {
    assert(!diagnostics.includes(privateValue), `native event diagnostics must not expose ${privateValue}`);
  }
});

test('related expand choice responses are retained as sanitized diagnostics', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
  });
  const request = {
    url: () => 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/related-expand-choices',
    method: () => 'POST',
    headers: () => ({}),
    postData: () => '{"selectionToken":"secret"}',
  };
  page.emit('request', request);
  const completed = capture.waitFor(entry => entry.path.endsWith('/related-expand-choices') && entry.status === 422);
  page.emit('response', {
    request: () => request,
    status: () => 422,
    headers: () => ({}),
    text: async () => '{"error":"selection is stale","authorization":"secret"}',
  });
  await completed;
  assert.deepEqual(report.nativeRequests[0].body, { selectionToken: '[REDACTED]' });
  assert.deepEqual(report.nativeRequests[0].response, { error: 'selection is stale', authorization: '[REDACTED]' });
  assert.deepEqual(report.errors[0].response, report.nativeRequests[0].response);
});

test('successful owned responses without diagnostic bodies flush without a request-tracker crash', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
  });
  const request = {
    url: () => 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/builder',
    method: () => 'POST', headers: () => ({}), postData: () => '{}',
  };
  page.emit('request', request);
  page.emit('response', {
    request: () => request, status: () => 200, headers: () => ({}),
    text: () => { throw new Error('successful command body should not be read'); },
  });
  await capture.flush();
  assert.deepEqual(report.errors, []);
  assert.deepEqual(report.nativeRequests[0].response, { bodyNotRead: true });
});

test('UI proxy requests retain private exact bodies while reports stay redacted', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8282', browserRequestOrigin: 'http://127.0.0.1:30102',
    appOrigins: ['http://127.0.0.1:30102', 'http://127.0.0.1:8282'],
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned', report,
  });
  const request = {
    url: () => 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/preview',
    method: () => 'POST', headers: () => ({}), postData: () => '{"snapshotToken":"exact-private-token"}',
  };
  page.emit('request', request);
  page.emit('response', { request: () => request, status: () => 200, headers: () => ({}), text: async () => '{"draftToken":"exact-private-response"}' });
  await capture.flush();
  const entry = report.nativeRequests[0];
  assert.deepEqual(capture.rawRequestBody(entry), { snapshotToken: 'exact-private-token' });
  assert.deepEqual(capture.rawResponseBody(entry), { draftToken: 'exact-private-response' });
  assert(!JSON.stringify(report).includes('exact-private-'));
});

test('direct workflow report payloads redact small values and cap oversized values', () => {
  const small = { snapshotToken: 'private-token', patientName: 'Sensitive Name' };
  const sanitizedSmall = sanitizeReportPayload(small);
  assert.deepEqual(sanitizedSmall, {
    snapshotToken: '[REDACTED]', patientName: 'Sensitive Name',
  });

  const large = { rows: [{ patientName: 'Sensitive Name', value: 'x'.repeat(12_100) }] };
  const serializedLength = JSON.stringify(large).length;
  const sanitizedLarge = sanitizeReportPayload(large);
  assert.deepEqual(sanitizedLarge, { truncated: true, length: serializedLength });
  assert(!JSON.stringify(sanitizedLarge).includes('Sensitive Name'));

  const smallCandidateConstruction = { steps: [{ operation: { snapshotToken: 'private-candidate-token', outputId: 'output-safe' } }] };
  const persistedSmallCandidateConstruction = sanitizeReportPayload(smallCandidateConstruction);
  assert.deepEqual(persistedSmallCandidateConstruction, { steps: [{ operation: { snapshotToken: '[REDACTED]', outputId: 'output-safe' } }] });
  assert(!JSON.stringify(persistedSmallCandidateConstruction).includes('private-candidate-token'));

  const candidateConstruction = { steps: [{ operation: { snapshotToken: 'private-candidate-token', rows: [{ value: 'x'.repeat(12_100) }] } }] };
  const persistedCandidateConstruction = sanitizeReportPayload(candidateConstruction);
  assert.deepEqual(persistedCandidateConstruction, { truncated: true, length: JSON.stringify(candidateConstruction).length });
  assert(!JSON.stringify(persistedCandidateConstruction).includes('private-candidate-token'));
});

test('Membership capability wait resolves only on the exact terminal source-projection response', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
    responsePaths: /construction-capabilities|builder/,
  });
  const origin = 'http://127.0.0.1:30102';
  const explorerPath = '/api/v1/projects/isolated/explorers/owned';
  const path = `${explorerPath}/authoring/v2/construction-capabilities`;
  const snapshotToken = `sha256:${'a'.repeat(64)}`;
  const draftDigest = `sha256:${'b'.repeat(64)}`;
  const expectedRequest = { snapshotToken, expectedDraftVersion: 13,
    expectedDraftDigest: draftDigest, outputId: 'output-1', stageId: 'source_projection' };
  const expectedResponse = { snapshotToken, draftVersion: 13, draftDigest,
    outputId: 'output-1', stageId: 'source_projection' };
  const requestFor = (url, body = expectedRequest) => ({
    url: () => url,
    method: () => 'POST',
    headers: () => ({}),
    postData: () => JSON.stringify(body),
    failure: () => null,
  });
  const responseFor = (request, status, text) => ({
    request: () => request,
    status: () => status,
    headers: () => ({ 'x-request-id': 'capability-response-1' }),
    text,
  });
  const exactTerminalResponse = entry => {
    const response = capture.rawResponseBody(entry);
    return entry.origin === origin && entry.path === path && entry.method === 'POST' &&
      Number.isFinite(entry.completedAt) && entry.status === 200 && entry.failure === undefined &&
      entry.responseReadError === undefined && response !== null && typeof response === 'object' &&
      entry.body?.snapshotToken === snapshotToken && entry.body?.expectedDraftVersion === 13 &&
      entry.body?.expectedDraftDigest === draftDigest && entry.body?.outputId === 'output-1' &&
      entry.body?.stageId === 'source_projection' &&
      response.snapshotToken === snapshotToken && response.draftVersion === 13 &&
      response.draftDigest === draftDigest && response.outputId === 'output-1' &&
      response.stageId === 'source_projection';
  };
  let resolved = false;
  const waiting = capture.waitFor(exactTerminalResponse, { timeoutMs: 1500 }).then(entry => {
    resolved = true;
    return entry;
  });

  const wrongOrigin = requestFor(`http://127.0.0.1:30103${path}`);
  page.emit('request', wrongOrigin);
  page.emit('response', responseFor(wrongOrigin, 200, async () => JSON.stringify(expectedResponse)));
  const wrongPath = requestFor(`${origin}${explorerPath}/authoring/v2/builder`);
  page.emit('request', wrongPath);
  page.emit('response', responseFor(wrongPath, 200, async () => JSON.stringify(expectedResponse)));
  await capture.flush();
  assert.equal(resolved, false, 'wrong origin and path must not satisfy the capability wait');

  const wrongRequest = requestFor(`${origin}${path}`, {
    ...expectedRequest,
    expectedDraftVersion: 12,
    expectedDraftDigest: `sha256:${'c'.repeat(64)}`,
    outputId: 'output-other',
  });
  page.emit('request', wrongRequest);
  await Promise.resolve();
  assert.equal(resolved, false, 'request start alone must not close the lifecycle wait');
  page.emit('response', responseFor(wrongRequest, 200, async () => JSON.stringify({
    ...expectedResponse, draftVersion: 12, draftDigest: `sha256:${'c'.repeat(64)}`, outputId: 'output-other',
  })));
  await capture.flush();
  assert.equal(resolved, false, 'wrong CAS and output must not satisfy the capability wait');

  const request = requestFor(`${origin}${path}`);
  page.emit('request', request);
  await Promise.resolve();
  assert.equal(resolved, false, 'the exact request must remain pending until response-body capture finishes');
  let resolveResponseText;
  const deferredText = new Promise(resolve => { resolveResponseText = resolve; });
  page.emit('response', responseFor(request, 200, () => deferredText));
  await new Promise(resolve => setImmediate(resolve));
  const pendingEntry = report.nativeRequests.at(-1);
  assert.equal(pendingEntry.status, 200, 'response headers arrive before its body is decoded');
  assert.equal(pendingEntry.completedAt, undefined);
  assert.equal(capture.rawResponseBody(pendingEntry), undefined);
  assert.equal(resolved, false, 'response headers without a captured body must not close the wait');

  resolveResponseText(JSON.stringify(expectedResponse));
  const event = await waiting;
  await capture.flush();
  assert.equal(event, pendingEntry);
  assert.equal(event.origin, origin);
  assert.equal(event.path, path);
  assert.equal(event.status, 200);
  assert(Number.isFinite(event.completedAt));
  assert.equal(event.failure, undefined);
  assert.equal(event.responseReadError, undefined);
  assert.deepEqual(capture.rawResponseBody(event), expectedResponse);
  assert.equal(exactTerminalResponse(event), true);
});

test('ONE/ALL response drain times out with request identity when a response body never resolves', async () => {
  const workflowSource = await readFile(new URL('../../workflows/verify-cda-related-one-all-browser.mjs', import.meta.url), 'utf8');
  const drainSource = workflowSource.match(/const settleNativeResponses = async \(timeoutMs = 5000\) => \{[\s\S]*?\n\};(?=\nconst waitNative)/)?.[0];
  assert(drainSource, 'workflow must expose its native response settle boundary');
  assert.match(workflowSource, /nativeResponseReads\.track\(read, \{[\s\S]*?browserRequestId: entry\.browserRequestId,[\s\S]*?path: entry\.path/,
    'each response-body read must retain exact request metadata');
  assert.doesNotMatch(workflowSource, /Promise\.allSettled\(\[\.\.\.nativeResponseReads\]\)/,
    'the settle deadline must not await an unbounded allSettled drain');

  const makeSettle = new Function('assert', 'nativeResponseReads', 'report', 'annotateExpectedOwnerCancellation',
    'pendingNativeEvidence', 'sanitizeText', 'waitForNativeRequestChange', `${drainSource}; return settleNativeResponses;`);
  const reads = createPendingResponseReads();
  const request = {
    browserRequestId: 'browser-never-body', requestId: 'request-never-body', requestCorrelationId: 'correlation-never-body',
    method: 'POST', path: '/authoring/v2/construction-proposals', status: 200,
    networkTerminal: true, bodyReadStatus: 'reading',
  };
  reads.track(new Promise(() => {}), {
    phase: 'native-api-response-body', browserRequestId: request.browserRequestId,
    requestId: request.requestCorrelationId, requestCorrelationId: request.requestCorrelationId,
    method: request.method, path: request.path, status: request.status,
  });
  const report = { nativeRequests: [request], expectedOwnerCancellations: [] };
  const settle = makeSettle(assert, reads, report, () => {}, entry => ({
    browserRequestId: entry.browserRequestId, requestId: entry.requestId,
    requestCorrelationId: entry.requestCorrelationId, method: entry.method, path: entry.path,
    status: entry.status, bodyReadStatus: entry.bodyReadStatus,
  }), value => String(value), async () => {});
  const started = Date.now();
  await assert.rejects(settle(30), /Timed out draining native API responses/);
  assert(Date.now() - started < 500, 'the unresolved body must not defeat the settle deadline');
  assert.equal(report.nativeResponseDrain.status, 'timed-out');
  assert.deepEqual(report.nativeResponseDrain.pending, [{
    browserRequestId: 'browser-never-body', requestId: 'request-never-body',
    requestCorrelationId: 'correlation-never-body', method: 'POST',
    path: '/authoring/v2/construction-proposals', status: 200, bodyReadStatus: 'reading',
  }]);
  assert.match(report.nativeResponseDrain.responseReadError, /correlation-never-body/);
});

test('ONE/ALL response drain keeps one absolute deadline across newly arriving body reads', async () => {
  const workflowSource = await readFile(new URL('../../workflows/verify-cda-related-one-all-browser.mjs', import.meta.url), 'utf8');
  const drainSource = workflowSource.match(/const settleNativeResponses = async \(timeoutMs = 5000\) => \{[\s\S]*?\n\};(?=\nconst waitNative)/)?.[0];
  assert(drainSource);
  const makeSettle = new Function('assert', 'nativeResponseReads', 'report', 'annotateExpectedOwnerCancellation',
    'pendingNativeEvidence', 'sanitizeText', 'waitForNativeRequestChange', `${drainSource}; return settleNativeResponses;`);
  const deferred = () => {
    let resolve;
    const promise = new Promise(resolvePromise => { resolve = resolvePromise; });
    return { promise, resolve };
  };
  const reads = createPendingResponseReads();
  const firstBody = deferred();
  const first = { browserRequestId: 'browser-first', requestId: 'request-first', requestCorrelationId: 'correlation-first',
    method: 'POST', path: '/authoring/v2/preview', status: 200, networkTerminal: true, bodyReadStatus: 'reading' };
  const second = { browserRequestId: 'browser-second', requestId: 'request-second', requestCorrelationId: 'correlation-second',
    method: 'POST', path: '/authoring/v2/construction-proposals', status: 200, networkTerminal: true, bodyReadStatus: 'pending' };
  const detailsFor = entry => ({ phase: 'native-api-response-body', browserRequestId: entry.browserRequestId,
    requestId: entry.requestCorrelationId, requestCorrelationId: entry.requestCorrelationId,
    method: entry.method, path: entry.path, status: entry.status });
  reads.track(firstBody.promise, detailsFor(first));
  firstBody.promise.then(() => {
    first.bodyReadStatus = 'decoded';
    second.bodyReadStatus = 'reading';
    reads.track(new Promise(() => {}), detailsFor(second));
  });
  const report = { nativeRequests: [first, second], expectedOwnerCancellations: [] };
  const settle = makeSettle(assert, reads, report, () => {}, entry => ({
    browserRequestId: entry.browserRequestId, requestId: entry.requestId,
    requestCorrelationId: entry.requestCorrelationId, method: entry.method, path: entry.path,
    status: entry.status, bodyReadStatus: entry.bodyReadStatus,
  }), value => String(value), async () => {});
  setTimeout(() => firstBody.resolve(), 35);
  const started = Date.now();
  await assert.rejects(settle(70), /Timed out draining native API responses/);
  const elapsed = Date.now() - started;
  assert(elapsed >= 60 && elapsed < 100, `the overall 70ms settle deadline must span both read batches (elapsed ${elapsed}ms)`);
  assert.equal(report.nativeResponseDrain.status, 'timed-out');
  assert.deepEqual(report.nativeResponseDrain.pending.map(entry => entry.requestCorrelationId), ['correlation-second']);
});

test('ONE/ALL response drain still rejects unexpected failed bodies with request evidence', async () => {
  const workflowSource = await readFile(new URL('../../workflows/verify-cda-related-one-all-browser.mjs', import.meta.url), 'utf8');
  const drainSource = workflowSource.match(/const settleNativeResponses = async \(timeoutMs = 5000\) => \{[\s\S]*?\n\};(?=\nconst waitNative)/)?.[0];
  assert(drainSource);
  const makeSettle = new Function('assert', 'nativeResponseReads', 'report', 'annotateExpectedOwnerCancellation',
    'pendingNativeEvidence', 'sanitizeText', 'waitForNativeRequestChange', `${drainSource}; return settleNativeResponses;`);
  const request = { requestId: 'failed-request', requestCorrelationId: 'failed-correlation', method: 'POST',
    path: '/authoring/v2/commands', status: 200, networkTerminal: true, bodyReadStatus: 'failed', bodyError: 'body read failed' };
  const report = { nativeRequests: [request], expectedOwnerCancellations: [] };
  const settle = makeSettle(assert, createPendingResponseReads(), report, () => {}, entry => ({
    requestId: entry.requestId, requestCorrelationId: entry.requestCorrelationId, method: entry.method,
    path: entry.path, status: entry.status, bodyReadStatus: entry.bodyReadStatus, bodyError: entry.bodyError,
  }), value => String(value), async () => {});
  await assert.rejects(settle(100), /Every captured native API response must decode/);
  assert.equal(report.nativeResponseDrain.status, 'failed');
  assert.deepEqual(report.nativeResponseDrain.failedBodies, [{ requestId: 'failed-request',
    requestCorrelationId: 'failed-correlation', method: 'POST', path: '/authoring/v2/commands',
    status: 200, bodyReadStatus: 'failed', bodyError: 'body read failed' }]);
});

test('ONE/ALL label edit checkpoint follows exact current-table rendering and precedes reload', async () => {
  const workflowSource = await readFile(new URL('../../workflows/verify-cda-related-one-all-browser.mjs', import.meta.url), 'utf8');
  const currentTableCheck = workflowSource.indexOf('const editedCurrentTable = await inspectPage');
  const exactRowsCheck = workflowSource.indexOf('assert.deepEqual(orderedRows(editedCurrentTable.rows), orderedRows(addedRows)');
  const editRecord = workflowSource.indexOf("record('edit-related-field-output-label', editStarted");
  const reload = workflowSource.indexOf("openTable(addedRows, 'reload-edited-related-field-output-label')");
  assert(currentTableCheck >= 0 && exactRowsCheck > currentTableCheck && editRecord > exactRowsCheck && reload > editRecord,
    'the edit timer closes only after exact current-table header and row rendering, with reload kept separate');
});

test('ONE/ALL diagnostics exempt only the exact classified request on a shared endpoint', async () => {
  const workflowSource = await readFile(new URL('../../workflows/verify-cda-related-one-all-browser.mjs', import.meta.url), 'utf8');
  const fixtureSource = await readFile(new URL('../cda-fixtures.mjs', import.meta.url), 'utf8');
  const predicate = workflowSource.match(/const unexpectedPlaywrightNetwork = cda\.diagnostics\.networkFailures\.filter\(\(failure\) =>([\s\S]*?)\);/)?.[1];
  assert(predicate, 'network diagnostics must filter by exact classified request identity');
  assert.match(fixtureSource, /const sameCapturedRequest = entry => entry\.kind === 'network' &&\s*entry\.browserRequestId === capturedEntry\.browserRequestId;[\s\S]*?diagnostic\.expectedCancellation = cancellation/,
    'fixture classification must stamp the expected marker only on the captured request identity');
  const isUnexpected = new Function('failure', `return (${predicate.trim()});`);
  const url = 'http://127.0.0.1:30008/api/v1/projects/project/explorers/explorer/authoring/v2/related-expand-contributors';
  const classified = { url, browserRequestId: 'browser-request-exact', expected: true,
    expectedCancellation: { browserRequestId: 'browser-request-exact' } };
  const unrelatedSamePath = { url, browserRequestId: 'browser-request-unrelated' };
  const mismatchedClassification = { url, browserRequestId: 'browser-request-third', expected: true,
    expectedCancellation: { browserRequestId: 'browser-request-exact' } };
  assert.deepEqual([classified, unrelatedSamePath, mismatchedClassification].filter(isUnexpected),
    [unrelatedSamePath, mismatchedClassification],
    'same-path and mismatched-ID failures remain fatal when only the exact captured request was classified');
});

test('named-cohort proposal report sanitizes only its persisted candidate construction', async () => {
  const workflowSource = await readFile(new URL('../../workflows/verify-cda-named-cohort-related-count-browser.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /candidateConstruction: sanitizeReportPayload\(response\.candidateConstruction\)/,
    'candidate construction report evidence must be sanitized and capped');
  assert.match(workflowSource, /const step = response\.candidateConstruction\.steps\.at\(-1\)/,
    'protocol assertions must continue using the raw proposal response');
});

test('request trackers wait only on entries whose exact bodies they own', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const options = {
    apiOrigin: 'http://127.0.0.1:8282',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
    responsePaths: /construction-proposals/,
  };
  report.nativeRequests.push({ path: '/earlier-shared-report-entry', completedAt: Date.now() });
  const fromIndex = report.nativeRequests.length;
  const earlierTracker = captureCDARequests(page, options);
  const proposalTracker = captureCDARequests(page, options);
  const path = '/api/v1/projects/isolated/explorers/owned/authoring/v2/construction-proposals';
  const requestBody = { snapshotToken: 'exact-private-token', outputId: 'out-owned' };
  const exactResponse = { draftToken: 'exact-private-response', outputId: 'out-owned', previewStatus: 'READY' };
  const request = {
    url: () => `http://127.0.0.1:8282${path}`,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'proposal-owner-test' }),
    postData: () => JSON.stringify(requestBody),
  };
  const predicate = entry => entry.path === path && entry.method === 'POST' && entry.status === 200;
  const earlierWait = earlierTracker.waitFor(predicate, { fromIndex, timeoutMs: 1000 });
  const proposalWait = proposalTracker.waitFor(predicate, { fromIndex, timeoutMs: 1000 });
  page.emit('request', request);
  page.emit('response', {
    request: () => request,
    status: () => 200,
    headers: () => ({ 'x-request-id': 'proposal-owner-response' }),
    text: async () => JSON.stringify(exactResponse),
  });

  const [earlierEntry, proposalEntry] = await Promise.all([earlierWait, proposalWait]);
  assert.notEqual(earlierEntry, proposalEntry, 'Each tracker must return its own entry from the shared report array');
  assert.deepEqual(earlierTracker.rawRequestBody(earlierEntry), requestBody);
  assert.deepEqual(earlierTracker.rawResponseBody(earlierEntry), exactResponse);
  assert.deepEqual(proposalTracker.rawRequestBody(proposalEntry), requestBody);
  assert.deepEqual(proposalTracker.rawResponseBody(proposalEntry), exactResponse);

  assert.equal(await earlierTracker.waitFor(predicate, { fromIndex }), earlierEntry,
    'Completed requests must be found immediately within the owning tracker');
  assert.equal(await proposalTracker.waitFor(predicate, { fromIndex }), proposalEntry,
    'The newer tracker must skip a completed sibling entry that it does not own');
});

test('captured public snapshot hashes and no-auth metadata stay inspectable after report sanitization', () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const snapshotToken = `sha256:${'b'.repeat(64)}`;
  captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8282',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
  });
  const request = {
    url: () => 'http://127.0.0.1:8282/api/v1/projects/isolated/explorers/owned/authoring/v2/construction-proposals',
    method: () => 'POST',
    headers: () => ({ 'content-type': 'application/json' }),
    postData: () => JSON.stringify({ snapshotToken, access_token: snapshotToken }),
  };

  page.emit('request', request);
  const safeReport = sanitizePayload(report);

  assert.equal(safeReport.nativeRequests[0].body.snapshotToken, snapshotToken);
  assert.equal(safeReport.nativeRequests[0].body.access_token, '[REDACTED]');
  assert.equal(safeReport.nativeRequests[0].authorizationHeaderPresent, false);
});

test('only the known missing favicon is recorded as an incidental asset failure', () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  captureCDARequests(page, { apiOrigin: 'http://127.0.0.1:30102', ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned', report });
  const emit = (url, value) => page.emit('console', { type: () => 'error', location: () => ({ url }), text: () => value });
  emit('http://127.0.0.1:30102/favicon.ico', 'Failed to load resource: the server responded with a status of 404 (Not Found)');
  emit('http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned', 'Failed to load resource: the server responded with a status of 404 (Not Found)');
  assert.deepEqual(report.assetFailures, [{ kind: 'console', url: 'http://127.0.0.1:30102/favicon.ico', status: 404 }]);
  assert.equal(report.errors.length, 1);
  assert.equal(report.errors[0].kind, 'console');
});

test('expected HTTP and matching console errors are scoped to the exact owned request', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const path = '/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/row-definition-proposals';
  const choiceId = 'choice-owned';
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned',
    report,
    shouldReportHttpError: (requestPath, status, entry) => !(
      requestPath === path && status === 422 && entry.method === 'POST' &&
      entry.body?.outputId === 'out-owned' &&
      entry.body?.selection?.kind === 'EXPANDED' &&
      entry.body?.selection?.expanded?.rowChoiceId === choiceId &&
      entry.body?.selection?.expanded?.emptyCollectionPolicy === 'ERROR'
    ),
  });
  const makeRequest = (requestId, policy) => ({
    url: () => `http://127.0.0.1:30102${path}`,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': requestId }),
    postData: () => JSON.stringify({ outputId: 'out-owned', selection: { kind: 'EXPANDED', expanded: { rowChoiceId: choiceId, emptyCollectionPolicy: policy } } }),
  });
  const emitResponse = async (request, body) => {
    page.emit('request', request);
    const fromIndex = report.nativeRequests.length - 1;
    const complete = capture.waitFor(entry => entry.completedAt && entry.status === 422, { fromIndex, timeout: 1000 });
    page.emit('response', {
      request: () => request,
      status: () => 422,
      headers: () => ({ 'x-request-id': `server-${request.headers()['x-request-id']}` }),
      text: async () => JSON.stringify(body),
    });
    return complete;
  };
  const emitConsole = (text = 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)') => page.emit('console', {
    type: () => 'error',
    location: () => ({ url: `http://127.0.0.1:30102${path}` }),
    text: () => text,
  });
  const diagnostic = { error: { code: 'EMPTY_COLLECTION_ERROR', message: 'Choose PRESERVE_PARENT or EXCLUDE.' } };

  const expectedRequest = makeRequest('expected-error-policy', 'ERROR');
  const expectedDone = emitResponse(expectedRequest, diagnostic);
  const expectedEntry = await expectedDone;
  expectedEntry.expectedHttpFailure = {
    browserRequestId: expectedEntry.browserRequestId,
    requestId: expectedEntry.requestId,
    method: expectedEntry.method,
    path: expectedEntry.path,
    status: expectedEntry.status,
    reason: 'The independent source oracle predicts this validation failure.',
    proof: { oracle: 'empty-only-source-selection' },
  };
  emitConsole();
  assert.notEqual(expectedEntry.expectedHttpFailure, true);
  assert.equal(expectedEntry.expectedHttpConsoleConsumed, true);
  assert.deepEqual(expectedEntry.response, diagnostic);
  assert.deepEqual(report.errors, []);

  emitConsole();
  emitConsole('A UI error mentioned status of 422 but was not a Chromium resource failure');
  assert.deepEqual(report.errors.map(error => error.kind), ['console', 'console']);

  const unexpectedRequest = makeRequest('unexpected-preserve-policy', 'PRESERVE_PARENT');
  const unexpectedDone = emitResponse(unexpectedRequest, { error: { code: 'UNEXPECTED_FAILURE' } });
  const unexpectedEntry = await unexpectedDone;
  assert.equal(unexpectedEntry.expectedHttpFailure, undefined);
  emitConsole();
  assert.equal(report.errors.filter(error => error.kind === 'http').length, 1);
  assert.equal(report.errors.filter(error => error.kind === 'console').length, 3);
  assert.equal(report.errors.find(error => error.kind === 'http').requestId, 'unexpected-preserve-policy');
});


test('matching aborted preview is skipped until its decoded replacement response arrives', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned', report,
    responsePaths: /preview/,
  });
  const path = '/api/v1/projects/isolated/explorers/owned/authoring/v2/preview';
  const makeRequest = requestId => ({
    url: () => `http://127.0.0.1:30102${path}`,
    method: () => 'POST', headers: () => ({ 'x-request-id': requestId }),
    postData: () => JSON.stringify({ outputId: 'out-owned', snapshotToken: 'sha256:owned-snapshot' }),
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  });
  const fromIndex = report.nativeRequests.length;
  let settled = false;
  const replacementWait = capture.waitFor(entry => entry.path === path && entry.body?.outputId === 'out-owned' &&
    entry.status === 200 && capture.rawResponseBody(entry)?.receiptId, { fromIndex, timeoutMs: 1000 })
    .then(entry => { settled = true; return entry; });

  const abortedRequest = makeRequest('preview-aborted');
  page.emit('request', abortedRequest);
  page.emit('requestfailed', abortedRequest);
  await new Promise(resolve => setImmediate(resolve));
  const abortedEntry = report.nativeRequests[0];
  assert.equal(abortedEntry.completedAt !== undefined, true);
  assert.equal(capture.rawResponseBody(abortedEntry), undefined);
  assert.equal(findCompletedNativeResponse(report.nativeRequests, capture.rawResponseBody,
    (entry, response) => entry.path === path && entry.body?.outputId === 'out-owned' && response.receiptId,
    fromIndex), undefined, 'A terminal aborted request is not a completed protocol response');
  assert.equal(settled, false, 'The matching abort must not satisfy the native preview waiter');

  const replacementRequest = makeRequest('preview-replacement');
  page.emit('request', replacementRequest);
  page.emit('response', {
    request: () => replacementRequest, status: () => 200, headers: () => ({}),
    text: async () => JSON.stringify({ receiptId: 'receipt-replacement', outputId: 'out-owned', rowCount: 1 }),
  });
  const replacementEntry = await replacementWait;
  assert.equal(replacementEntry, report.nativeRequests[1]);
  assert.equal(replacementEntry.requestId, 'preview-replacement');
  assert.equal(findCompletedNativeResponse(report.nativeRequests, capture.rawResponseBody,
    (entry, response) => entry.path === path && entry.body?.outputId === 'out-owned' && response.receiptId,
    fromIndex), replacementEntry);
});

test('oversized expected 422 is classified from the raw protocol body while diagnostics stay bounded', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned', report,
    responsePaths: /construction-proposals/,
  });
  const expected = {
    outputId: 'out-exact', candidateId: 'candidate-exact', nodeId: 'node-exact',
    choiceId: 'signed-choice-exact', snapshotToken: 'sha256:fixture-snapshot',
    resourceType: 'Observation', path: 'id', routeTypes: ['Specimen', 'Patient', 'Observation'],
  };
  const requestBody = {
    outputId: expected.outputId, changedStepId: 'related-step-exact', snapshotToken: expected.snapshotToken,
    candidateConstruction: { steps: [{ id: 'related-step-exact', operation: { kind: 'RELATED_SOURCE', relatedSource: {
      source: { candidateId: expected.candidateId, nodeId: expected.nodeId, resourceType: expected.resourceType, path: expected.path },
      choiceId: expected.choiceId,
      route: [
        { fromResourceType: 'Specimen', toResourceType: 'Patient' },
        { fromResourceType: 'Patient', toResourceType: 'Observation' },
      ],
      form: 'ALL', contributorRule: { policy: 'ALL_MATCHES' }, rowValuePolicy: 'ONE',
    } } }] },
  };
  const request = {
    url: () => 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/construction-proposals',
    method: () => 'POST', headers: () => ({ 'x-request-id': 'oversized-expected-422' }), postData: () => JSON.stringify(requestBody),
  };
  page.emit('request', request);
  const protocolResponse = {
    error: { code: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', message: 'The selected related values disagree.' },
    accessToken: 'private-test-token', detail: 'x'.repeat(33_000),
  };
  const body = JSON.stringify(protocolResponse);
  assert(body.length > 32_768, 'fixture must cross the bounded capture threshold');
  page.emit('response', {
    request: () => request, status: () => 422, headers: () => ({}), text: async () => body,
  });
  await capture.flush();
  const entry = report.nativeRequests[0];
  const exactEntry = {
    ...entry, request: capture.rawRequestBody(entry), response: capture.rawResponseBody(entry),
  };
  assert.deepEqual(exactEntry.response, protocolResponse,
    'workflow protocol decisions must retain the full expected validation response');
  assert.equal(expectedRelatedSourceOneValidation(exactEntry, expected), true,
    'final expected-422 accounting must classify the complete protocol response');
  assert.equal(expectedRelatedSourceOneValidation({ ...exactEntry, response: entry.response }, expected), false,
    'bounded report diagnostics cannot replace the raw response for protocol classification');
  assert.deepEqual(entry.response, { truncated: true, length: body.length },
    'the report copy must stay bounded rather than retaining a truncated JSON string');
  assert(!JSON.stringify(report).includes('x'.repeat(100)), 'the report must not include the oversized body');
  assert(!JSON.stringify(report).includes('private-test-token'), 'the report must not include the sensitive live protocol value');
});

test('Apply and reconcile identity checks use retained protocol values beyond the diagnostic body limit', async () => {
  const workflowSource = await readFile(new URL('../../workflows/verify-cda-related-one-all-browser.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /text\.length > 12_000/, 'the workflow must keep its bounded diagnostic projection');
  assert.match(workflowSource, /const allCommandResponse = protocolResponse\(allCommand\)/);
  assert.match(workflowSource, /const acceptedReconcileResponse = protocolResponse\(acceptedReconcile\)/);
  assert.doesNotMatch(workflowSource, /allCommand\.response\.(?:draftVersion|draftDigest)|acceptedReconcile\.response\.(?:snapshotToken|intentDigest|outputs|receiptId)/,
    'Apply and reconcile protocol assertions must not read the truncated diagnostic copy');

  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
    responsePaths: /commands|reconcile/,
  });
  const identities = [
    {
      path: 'commands', requestId: 'large-apply', response: {
        draftVersion: 23, draftDigest: 'sha256:applied-draft', detail: 'x'.repeat(40_000),
      },
    },
    {
      path: 'reconcile', requestId: 'large-reconcile', response: {
        snapshotToken: 'sha256:accepted-snapshot', intentDigest: 'sha256:applied-draft',
        receiptId: 'receipt-current', outputs: [{ outputId: 'output-current' }], detail: 'y'.repeat(40_000),
      },
    },
  ];
  for (const item of identities) {
    const request = {
      url: () => `http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/${item.path}`,
      method: () => 'POST', headers: () => ({ 'x-request-id': item.requestId }), postData: () => '{}',
    };
    page.emit('request', request);
    page.emit('response', {
      request: () => request, status: () => 200, headers: () => ({}), text: async () => JSON.stringify(item.response),
    });
  }
  await capture.flush();

  const [applyEntry, reconcileEntry] = report.nativeRequests;
  assert(applyEntry.response.length > 12_000);
  assert(reconcileEntry.response.length > 12_000);
  assert.deepEqual(applyEntry.response, { truncated: true, length: JSON.stringify(identities[0].response).length });
  assert.deepEqual(reconcileEntry.response, { truncated: true, length: JSON.stringify(identities[1].response).length });

  const applyResponse = capture.rawResponseBody(applyEntry);
  const reconcileResponse = capture.rawResponseBody(reconcileEntry);
  assert.deepEqual({ draftVersion: applyResponse.draftVersion, draftDigest: applyResponse.draftDigest },
    { draftVersion: 23, draftDigest: 'sha256:applied-draft' });
  assert.deepEqual({ snapshotToken: reconcileResponse.snapshotToken, intentDigest: reconcileResponse.intentDigest,
    receiptId: reconcileResponse.receiptId, outputs: reconcileResponse.outputs }, {
    snapshotToken: 'sha256:accepted-snapshot', intentDigest: 'sha256:applied-draft',
    receiptId: 'receipt-current', outputs: [{ outputId: 'output-current' }],
  });
  assert(!JSON.stringify(report).includes('x'.repeat(100)) && !JSON.stringify(report).includes('y'.repeat(100)),
    'large response payloads must remain absent from serialized diagnostics');
});

test('a proposal captured during its trigger click remains visible to the waiter', async () => {
  const workflowSource = await readFile(new URL('../../workflows/verify-cda-related-one-all-browser.mjs', import.meta.url), 'utf8');
  const proposalBody = workflowSource.match(/const proposal = async \(name, started, expectedRows, fromIndex(?:, matchesRequest = \(\) => true)?\) => \{([\s\S]*?)\n\};/)?.[1];
  assert(proposalBody, 'proposal must receive its request boundary from the triggering action');
  assert.doesNotMatch(proposalBody, /report\.nativeRequests\.length/, 'proposal must not take its index after the click has already happened');
  const proposalCalls = [...workflowSource.matchAll(/await proposal\(([\s\S]*?)\);/g)].map((match) => match[1]);
  assert.equal(proposalCalls.length, 7, 'all native proposal paths must be checked');
  assert(proposalCalls.every((call) => /fromIndex/i.test(call)),
    'every proposal wait must receive an index captured before its triggering click');

  const entries = [];
  const beforeClickIndex = entries.length;
  const completedProposal = {
    path: '/construction-proposals', startedAt: 100, complete: true,
    response: { proposalId: 'proposal-triggered-by-click' },
  };
  entries.push(completedProposal);
  const matchesProposal = (entry, response) => entry.startedAt >= 100 &&
    entry.path.endsWith('/construction-proposals') && response?.proposalId;
  assert.equal(findCompletedNativeResponse(entries, entry => entry.response, matchesProposal, beforeClickIndex), completedProposal,
    'the request that arrives before the click promise resolves must still satisfy the proposal wait');
  assert.equal(findCompletedNativeResponse(entries, entry => entry.response, matchesProposal, entries.length), undefined,
    'taking the request boundary after the click would skip the already-recorded proposal');
});

const navigationBodyReadError = new Error('response.text: Protocol error (Network.getResponseBody): No data found for resource with given identifier\nResponse body is not available for a response that was navigated away from. Read response.body() before triggering any navigation.');

function makeResponseReadFailureHarness() {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const requestFailures = new WeakMap();
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
    responsePaths: /commands/,
  });
  const trackers = new Set([capture]);
  let nextRequestId = 1;

  const requestFor = () => {
    const id = nextRequestId++;
    let observedFailure = null;
    const request = {
      url: () => 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/commands',
      method: () => 'POST',
      headers: () => ({ 'x-request-id': `body-read-${id}` }),
      postData: () => '{}',
      failure: () => observedFailure,
    };
    page.emit('request', request);
    const entry = capture.byRequest.get(request);
    return {
      request,
      entry,
      respond(status = 200, text = () => Promise.reject(navigationBodyReadError)) {
        page.emit('response', {
          request: () => request,
          status: () => status,
          headers: () => ({}),
          text,
        });
      },
      fail(errorText = 'net::ERR_ABORTED') {
        observedFailure = { errorText };
        const failure = {
          errorText,
          method: request.method(),
          url: request.url(),
          requestId: request.headers()['x-request-id'],
          playwrightRequestId: `cda-request-${entry.browserRequestId}`,
        };
        requestFailures.set(request, failure);
        page.emit('requestfailed', request);
      },
    };
  };

  return { capture, report, requestFailures, trackers, requestFor };
}

const classifyBodyReadCancellation = (harness, request) => classifyExpectedCdaCancellation({
  request,
  reason: 'A replacement navigation canceled this response body read.',
  proof: { action: 'Replace the current page after its command response.' },
  report: harness.report,
  requestFailures: harness.requestFailures,
  trackers: harness.trackers,
});

test('an exact classified net::ERR_ABORTED accepts the HTTP 200 navigation body-read failure', async () => {
  const harness = makeResponseReadFailureHarness();
  const failed = harness.requestFor();
  failed.respond();
  await new Promise(resolve => setImmediate(resolve));
  failed.fail();

  const cancellation = classifyBodyReadCancellation(harness, failed.request);
  assert.equal(failed.entry.status, 200);
  assert.equal(failed.entry.responseReadError, navigationBodyReadError.message,
    'the protocol body-read failure must remain available as request evidence');
  assert.equal(failed.entry.expectedCancellation, cancellation);
  assert.equal(cancellation.browserRequestId, failed.entry.browserRequestId);
  await assert.doesNotReject(harness.capture.flush());

  const internalFailure = harness.requestFor();
  internalFailure.respond(500, async () => JSON.stringify({ error: { code: 'INTERNAL' } }));
  await harness.capture.flush();
  assert(harness.report.errors.some(error => error.kind === 'http' && error.status === 500 &&
    error.browserRequestId === internalFailure.entry.browserRequestId && error.response?.error?.code === 'INTERNAL'),
  'classifying the exact 200 cancellation must leave a distinct HTTP 500 INTERNAL diagnostic fatal');
});

test('an unclassified net::ERR_ABORTED response-body read remains fatal', async () => {
  const harness = makeResponseReadFailureHarness();
  const failed = harness.requestFor();
  failed.respond();
  await new Promise(resolve => setImmediate(resolve));
  failed.fail();

  assert.equal(failed.entry.failure, 'net::ERR_ABORTED');
  assert.equal(failed.entry.responseReadError, navigationBodyReadError.message);
  await assert.rejects(harness.capture.flush(), /Failed owned CDA response reads/);
});

test('classifying a different request does not excuse the failed response-body read', async () => {
  const harness = makeResponseReadFailureHarness();
  const bodyReadFailure = harness.requestFor();
  const classifiedRequest = harness.requestFor();
  bodyReadFailure.respond();
  await new Promise(resolve => setImmediate(resolve));
  bodyReadFailure.fail();
  classifiedRequest.fail();
  classifyBodyReadCancellation(harness, classifiedRequest.request);

  assert.notEqual(bodyReadFailure.entry.browserRequestId, classifiedRequest.entry.browserRequestId);
  assert.equal(bodyReadFailure.entry.expectedCancellation, undefined);
  assert.equal(classifiedRequest.entry.expectedCancellation.browserRequestId, classifiedRequest.entry.browserRequestId);
  await assert.rejects(harness.capture.flush(), /Failed owned CDA response reads/);
});

test('a classified abort with a different response-body error remains fatal', async () => {
  const harness = makeResponseReadFailureHarness();
  const failed = harness.requestFor();
  const differentReadError = new Error('response.text: Protocol error (Network.getResponseBody): response body is unavailable for another reason.');
  failed.respond(200, () => Promise.reject(differentReadError));
  await new Promise(resolve => setImmediate(resolve));
  failed.fail();
  classifyBodyReadCancellation(harness, failed.request);

  assert.equal(failed.entry.expectedCancellation.browserRequestId, failed.entry.browserRequestId);
  assert.equal(failed.entry.responseReadError, differentReadError.message);
  await assert.rejects(harness.capture.flush(), /Failed owned CDA response reads/);
});

test('non-abort response-body failures cannot be classified and remain fatal', async () => {
  const harness = makeResponseReadFailureHarness();
  const failed = harness.requestFor();
  failed.respond();
  await new Promise(resolve => setImmediate(resolve));
  failed.fail('net::ERR_FAILED');

  assert.throws(() => classifyBodyReadCancellation(harness, failed.request), /Only a native net::ERR_ABORTED/);
  await assert.rejects(harness.capture.flush(), /Failed owned CDA response reads/);
});
