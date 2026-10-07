import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { createPendingResponseReads } from '../pending-response-reads.mjs';

const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const emitProposalResponse = (text, status = 422) => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const path = '/api/v1/projects/p/explorers/e/authoring/v2/construction-proposals';
  const request = {
    url: () => `http://127.0.0.1:8188${path}`,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'proposal-request-1' }),
    postData: () => '{"outputId":"out-1"}',
    failure: () => null,
  };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8188',
    ownedPathPrefix: '/api/v1/projects/p/explorers/e',
    report,
  });
  page.emit('request', request);
  page.emit('response', {
    request: () => request,
    status: () => status,
    headers: () => ({}),
    text,
  });
  return { capture, report, path };
};

test('bounded response drain waits for delayed bodies and clears its deadline timer', async () => {
  const body = deferred();
  const { capture, report } = emitProposalResponse(() => body.promise);
  let finished = false;
  const draining = capture.flush({ timeoutMs: 500 }).then(() => { finished = true; });
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(finished, false, 'flush must remain pending while an owned response body is being read');
  assert(capture.pendingReads.size > 0);

  body.resolve('{"error":{"code":"VALIDATION_ERROR"}}');
  await draining;
  assert.equal(capture.pendingReads.size, 0);
  assert.equal(report.nativeRequests[0].response.error.code, 'VALIDATION_ERROR');
  assert.equal(report.nativeRequests[0].completedAt > 0, true);
  assert.equal(report.errors[0].response.error.code, 'VALIDATION_ERROR');
});

test('bounded response drain times out with exact outstanding request identity', async () => {
  const body = deferred();
  const { capture, path } = emitProposalResponse(() => body.promise);
  await assert.rejects(capture.flush({ timeoutMs: 20 }), error => {
    assert.match(error.message, /Timed out flushing owned CDA response reads/);
    assert.match(error.message, /playwright-1/);
    assert.match(error.message, /proposal-request-1/);
    assert.match(error.message, new RegExp(path.replaceAll('/', '\\/')));
    return true;
  });

  body.resolve('{"error":"late body"}');
  await capture.flush({ timeoutMs: 500 });
});

test('unexpected response-body failure remains fatal and keeps its exact HTTP diagnostic', async () => {
  const { capture, report } = emitProposalResponse(async () => { throw new Error('body pipe broke'); });
  await assert.rejects(capture.flush({ timeoutMs: 500 }), error => {
    assert.match(error.message, /Failed owned CDA response reads/);
    assert.match(error.message, /playwright-1/);
    assert.match(error.message, /body pipe broke/);
    return true;
  });
  assert.equal(report.nativeRequests[0].responseReadError, 'body pipe broke');
  assert.equal(report.errors.length, 1);
  assert.equal(report.errors[0].kind, 'http');
  assert.equal(report.errors[0].expected, undefined);
  assert.equal(report.errors[0].responseReadError, 'body pipe broke');
});

test('exact diagnostic drain ignores unrelated pending bodies and exposes final timeout details', async () => {
  const reads = createPendingResponseReads();
  const expected = deferred();
  const unrelated = deferred();
  reads.track(expected.promise, { browserRequestId: 'playwright-expected', path: '/owned/422', status: 422 });
  reads.track(unrelated.promise, { browserRequestId: 'playwright-other', path: '/owned/other', status: 500 });

  const exactDrain = reads.flush({ browserRequestId: 'playwright-expected', timeoutMs: 500,
    filter: details => details.browserRequestId === 'playwright-expected', label: 'fixture HTTP diagnostic bodies' });
  expected.resolve();
  await exactDrain;
  assert.equal(reads.pendingReads.size, 1, 'the exact drain must not wait for a different browser request');
  await assert.rejects(reads.flush({ timeoutMs: 20, label: 'fixture HTTP diagnostic bodies' }), error => {
    assert.match(error.message, /playwright-other/);
    assert.match(error.message, /\/owned\/other/);
    return true;
  });
  unrelated.resolve();
  await reads.flush({ timeoutMs: 500 });
});

test('final drain settles other tracked bodies before surfacing a read failure', async () => {
  const reads = createPendingResponseReads();
  const delayed = deferred();
  reads.track(Promise.reject(new Error('first body failed')),
    { browserRequestId: 'playwright-failed', path: '/owned/failed', status: 422 });
  reads.track(delayed.promise,
    { browserRequestId: 'playwright-delayed', path: '/owned/delayed', status: 500 });

  let finished = false;
  const draining = reads.flush({ timeoutMs: 500, label: 'fixture HTTP diagnostic bodies' })
    .then(() => { finished = true; }, error => { finished = true; throw error; });
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(finished, false, 'a failure must not stop final completion from draining another response');
  assert.equal(reads.pendingReads.size, 1);
  delayed.resolve();
  await assert.rejects(draining, /first body failed/);
  assert.equal(reads.pendingReads.size, 0);
});

const fixtureResponseHandlers = () => {
  const source = readFileSync(new URL('../cda-fixtures.mjs', import.meta.url), 'utf8');
  const onResponseStart = source.indexOf('    const onResponse = response => {');
  const onResponseEnd = source.indexOf("\n    page.on('request', onRequest);", onResponseStart);
  const flushStart = source.indexOf('    const flushHttpDiagnostics = async', onResponseEnd);
  const flushEnd = source.indexOf('\n\n    let customDialogHandler;', flushStart);
  assert(onResponseStart >= 0 && onResponseEnd > onResponseStart && flushStart > onResponseEnd && flushEnd > flushStart);
  const createHandlers = new Function('deps', `
    const { localRequest, safeURL, diagnostics, report, requestDiagnostic, playwrightRequestId,
      capturedEntryFor, addNetworkDiagnostic, safeText, sanitizeBody, httpDiagnosticEntries,
      responseReads } = deps;
    ${source.slice(onResponseStart, onResponseEnd)}
    ${source.slice(flushStart, flushEnd)}
    return { onResponse, flushHttpDiagnostics };
  `);
  const makeFixture = () => {
    const diagnostics = { httpFailures: [], assetFailures: [] };
    const report = { assetFailures: [], network: [] };
    const httpDiagnosticEntries = [];
    const responseReads = createPendingResponseReads();
    const handlers = createHandlers({
      localRequest: () => true,
      safeURL: rawURL => rawURL,
      diagnostics,
      report,
      requestDiagnostic: () => ({ requestId: 'fixture-request' }),
      playwrightRequestId: () => 'fixture-playwright-request',
      capturedEntryFor: request => ({ browserRequestId: request.browserRequestId }),
      addNetworkDiagnostic: entry => report.network.push(entry),
      safeText: value => String(value ?? ''),
      sanitizeBody: value => value,
      httpDiagnosticEntries,
      responseReads,
    });
    const emitResponse = (text, { browserRequestId = 'fixture-playwright-422', status = 422 } = {}) => {
      const request = {
        browserRequestId,
        method: () => 'POST',
        headers: () => ({ 'x-request-id': `request-${browserRequestId}` }),
      };
      handlers.onResponse({
        request: () => request,
        status: () => status,
        url: () => `http://127.0.0.1:8188/owned/${browserRequestId}`,
        text,
      });
      return httpDiagnosticEntries.at(-1);
    };
    return { ...handlers, diagnostics, httpDiagnosticEntries, responseReads, emitResponse };
  };
  return { source, makeFixture };
};

test('fixture exact drain waits for its owned response and final drain waits for remaining diagnostics', async () => {
  const { makeFixture } = fixtureResponseHandlers();
  const fixture = makeFixture();
  const expectedBody = deferred();
  const unrelatedBody = deferred();
  const expectedEntry = fixture.emitResponse(() => expectedBody.promise,
    { browserRequestId: 'fixture-playwright-422', status: 422 });
  const unrelatedEntry = fixture.emitResponse(() => unrelatedBody.promise,
    { browserRequestId: 'fixture-playwright-500', status: 500 });

  let exactFinished = false;
  const exactDrain = fixture.flushHttpDiagnostics({ browserRequestId: expectedEntry.browserRequestId, timeoutMs: 500 })
    .then(() => { exactFinished = true; });
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(exactFinished, false);
  expectedBody.resolve('{"error":"expected validation"}');
  await exactDrain;
  assert.equal(expectedEntry.responseBody.captureState, 'completed');
  assert.equal(fixture.responseReads.pendingReads.size, 1,
    'exact drain must leave the unrelated response read for the final drain');

  await assert.rejects(fixture.flushHttpDiagnostics({ timeoutMs: 20 }), error => {
    assert.match(error.message, /fixture-playwright-500/);
    assert.match(error.message, /\/owned\/fixture-playwright-500/);
    return true;
  });
  unrelatedBody.resolve('{"error":"unrelated"}');
  await fixture.flushHttpDiagnostics({ timeoutMs: 500 });
  assert.equal(unrelatedEntry.responseBody.captureState, 'completed');
});

test('fixture body-read failure stays visible and fails exact completion', async () => {
  const { makeFixture } = fixtureResponseHandlers();
  const fixture = makeFixture();
  const failedEntry = fixture.emitResponse(async () => { throw new Error('fixture body failed'); });
  await assert.rejects(fixture.flushHttpDiagnostics({ browserRequestId: failedEntry.browserRequestId, timeoutMs: 500 }), error => {
    assert.match(error.message, /Failed fixture HTTP diagnostic bodies/);
    assert.match(error.message, /fixture-playwright-422/);
    assert.match(error.message, /fixture body failed/);
    return true;
  });
  assert.equal(failedEntry.responseBody.captureState, 'readfailed');
  assert.equal(fixture.diagnostics.httpFailures[0].body.captureState, 'readfailed');
});

test('fixture drains diagnostic bodies before snapshot and source identity verification', () => {
  const { source } = fixtureResponseHandlers();
  assert.match(source, /const responseReads = createPendingResponseReads\(\)/);
  assert.match(source, /responseReads\.track\(read,\s*\{\s*phase: 'fixture-http-response-body'/);
  assert.match(source, /const flushHttpDiagnostics = async \(\{ browserRequestId, timeoutMs = 5_000 \}/);
  assert.match(source, /await responseReads\.flush\(\{\s*timeoutMs,\s*label: 'fixture HTTP diagnostic bodies'/);
  const drainCall = source.indexOf('flushHttpDiagnostics({ timeoutMs: 5_000 }),');
  const snapshot = source.indexOf('includeBrowserDiagnostics(diagnostics, report);', drainCall);
  const identityFinish = source.indexOf('const result = await identity.finish();', snapshot);
  assert(drainCall >= 0 && snapshot > drainCall && identityFinish > snapshot,
    'fixture drains diagnostic bodies before snapshot, then proves source/API identity separately');
  assert.match(source, /kind: 'browser-diagnostic-drain'/);
  assert.match(source, /if \(diagnosticDrainError\) throw diagnosticDrainError/);
});
