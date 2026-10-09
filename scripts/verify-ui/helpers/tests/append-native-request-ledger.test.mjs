import assert from 'node:assert/strict';
import test from 'node:test';
import { createFixtureNativeRequestLedger } from '../native-request-ledger.mjs';
import {
  flushAppendNativeRequestScope,
  openAppendNativeRequestScope,
} from '../../workflows/builder-combine.mjs';

const origin = 'http://127.0.0.1:30008';
const project = 'append-ledger-project';
const explorer = 'fresh-append-explorer';
const projectPath = `/api/v1/projects/${project}/explorers`;
const explorerPath = `${projectPath}/${explorer}`;

const request = (path, method = 'GET', requestOrigin = origin) => ({
  url: () => `${requestOrigin}${path}`,
  method: () => method,
  resourceType: () => 'fetch',
});

const recordFinished = (ledger, capturedRequest, requestId, status = 200) => {
  ledger.recordRequest(capturedRequest, {
    requestId,
    browserRequestId: `browser-${requestId}`,
    method: capturedRequest.method(),
    resourceType: capturedRequest.resourceType(),
    url: capturedRequest.url(),
    startedAt: 1,
  });
  ledger.recordResponse(capturedRequest, { status, observedAt: 2 });
  ledger.recordFinished(capturedRequest, { observedAt: 3 });
};

const target = { fixtureProject: project, uiUrl: `${origin}/builder` };

test('Append request scope captures the fresh Explorer routes and flushes terminal events', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = openAppendNativeRequestScope(ledger, target);
  const create = request(projectPath, 'POST');
  const builder = request(`${explorerPath}/authoring/v2/builder`);
  recordFinished(ledger, create, 'append-explorer-create', 201);
  recordFinished(ledger, builder, 'append-builder-read');
  const siblingExplorer = request(`${projectPath}/other-explorer/authoring/v2/builder`);
  recordFinished(ledger, siblingExplorer, 'sibling-explorer-builder');
  assert.equal(ledger.recordRequest(request('/api/v1/projects/another-project/explorers/foreign/authoring/v2/builder'), {
    requestId: 'wrong-project', browserRequestId: 'browser-wrong-project', method: 'GET',
  }), undefined);
  assert.equal(ledger.recordRequest(request(`${explorerPath}/authoring/v2/builder`, 'GET', 'http://127.0.0.1:8188'), {
    requestId: 'wrong-origin', browserRequestId: 'browser-wrong-origin', method: 'GET',
  }), undefined);

  const snapshot = await flushAppendNativeRequestScope({
    nativeRequestLedger: ledger,
    scope,
    explorer,
    report: {},
    timeoutMs: 20,
  });

  assert.equal(snapshot.nativeRequestTerminalLedger.project, project);
  assert.equal(snapshot.nativeRequestTerminalLedger.explorer, explorer);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, true);
  assert.deepEqual(snapshot.nativeRequestTerminalLedger.requests.map(entry => entry.requestId), [
    'append-explorer-create', 'append-builder-read',
  ]);
  assert.deepEqual(snapshot.excludedNativeRequests.map(entry => entry.requestId), ['sibling-explorer-builder']);
});

test('Append request scope fails when a fresh Explorer request remains unfinished', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = openAppendNativeRequestScope(ledger, target);
  const create = request(projectPath, 'POST');
  const builder = request(`${explorerPath}/authoring/v2/builder`);
  const pendingPreview = request(`${explorerPath}/construction/preview`, 'POST');
  recordFinished(ledger, create, 'append-explorer-create', 201);
  recordFinished(ledger, builder, 'append-builder-read');
  ledger.recordRequest(pendingPreview, {
    requestId: 'append-preview-pending',
    browserRequestId: 'browser-append-preview-pending',
    method: 'POST',
    resourceType: 'fetch',
    url: pendingPreview.url(),
    startedAt: 4,
  });
  const report = {};

  await assert.rejects(flushAppendNativeRequestScope({
    nativeRequestLedger: ledger,
    scope,
    explorer,
    report,
    timeoutMs: 10,
  }), /APPEND native request ledger did not reach a complete terminal state/);

  assert.equal(report.nativeRequestTerminalLedger.complete, false);
  assert.equal(report.nativeRequestTerminalLedger.counts.pending, 1);
  assert.equal(report.nativeRequestTerminalLedger.requests.find(entry =>
    entry.requestId === 'append-preview-pending')?.state, 'pending');
});
