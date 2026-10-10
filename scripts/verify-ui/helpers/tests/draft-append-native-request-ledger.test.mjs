import assert from 'node:assert/strict';
import test from 'node:test';
import { createFixtureNativeRequestLedger } from '../native-request-ledger.mjs';
import {
  flushDraftCombineNativeRequestScope,
  draftAppendWorkflow,
  openDraftCombineNativeRequestScope,
} from '../../workflows/builder-combine-draft.mjs';

const origin = 'http://127.0.0.1:30008';
const project = 'draft-append-ledger-project';
const explorer = 'fresh-draft-append-explorer';
const projectPath = `/api/v1/projects/${project}/explorers`;
const explorerPath = `${projectPath}/${explorer}`;
const target = { fixtureProject: project, uiUrl: `${origin}/builder` };

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

test('current-draft Append flush retains only the exact fresh Explorer request ledger', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = openDraftCombineNativeRequestScope(ledger, target, 'Draft APPEND');
  recordFinished(ledger, request(projectPath, 'POST'), 'append-explorer-create', 201);
  recordFinished(ledger, request(`${explorerPath}/authoring/v2/builder`), 'append-builder-read');
  recordFinished(ledger, request(`${explorerPath}/authoring/v2/construction-proposals`, 'POST'), 'append-preview');
  recordFinished(ledger, request(`${projectPath}/bootstrap/authoring/v2/builder`), 'other-explorer-builder');
  assert.equal(ledger.recordRequest(request('/api/v1/projects/another-project/explorers/foreign/authoring/v2/builder'), {
    requestId: 'other-project', browserRequestId: 'browser-other-project', method: 'GET',
  }), undefined);
  assert.equal(ledger.recordRequest(request(`${explorerPath}/authoring/v2/builder`, 'GET', 'http://127.0.0.1:8188'), {
    requestId: 'other-origin', browserRequestId: 'browser-other-origin', method: 'GET',
  }), undefined);
  const report = {};

  const snapshot = await flushDraftCombineNativeRequestScope({
    nativeRequestLedger: ledger,
    scope,
    explorer,
    report,
    timeoutMs: 20,
    label: 'Draft APPEND',
  });

  assert.equal(snapshot.nativeRequestTerminalLedger.project, project);
  assert.equal(snapshot.nativeRequestTerminalLedger.explorer, explorer);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, true);
  assert.deepEqual(snapshot.nativeRequestTerminalLedger.requests.map(entry => entry.requestId), [
    'append-explorer-create', 'append-builder-read', 'append-preview',
  ]);
  assert.deepEqual(snapshot.excludedNativeRequests.map(entry => entry.requestId), ['other-explorer-builder']);
  assert.equal(report.nativeRequestTerminalLedger, snapshot.nativeRequestTerminalLedger);
});

test('current-draft Append fails when an owned Explorer request remains pending', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = openDraftCombineNativeRequestScope(ledger, target, 'Draft APPEND');
  recordFinished(ledger, request(projectPath, 'POST'), 'append-explorer-create', 201);
  recordFinished(ledger, request(`${explorerPath}/authoring/v2/builder`), 'append-builder-read');
  const pending = request(`${explorerPath}/authoring/v2/construction-proposals`, 'POST');
  ledger.recordRequest(pending, {
    requestId: 'append-preview-pending',
    browserRequestId: 'browser-append-preview-pending',
    method: 'POST',
    resourceType: 'fetch',
    url: pending.url(),
    startedAt: 4,
  });
  const report = {};

  await assert.rejects(flushDraftCombineNativeRequestScope({
    nativeRequestLedger: ledger,
    scope,
    explorer,
    report,
    timeoutMs: 10,
    label: 'Draft APPEND',
  }), /Draft APPEND native request ledger did not reach a complete terminal state/);

  assert.equal(report.nativeRequestTerminalLedger.complete, false);
  assert.equal(report.nativeRequestTerminalLedger.counts.pending, 1);
  assert.equal(report.nativeRequestTerminalLedger.requests.find(entry =>
    entry.requestId === 'append-preview-pending')?.state, 'pending');
});

test('current-draft Append opens and flushes its scope even when fixture setup fails before Explorer creation', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const report = { target: {} };
  const context = {
    custom: false,
    seed: { fresh: true },
    runID: 'draft-append-setup-failure',
    target: {
      ...target,
      fixtureDir: '/private/tmp/missing-case020-append-fixture',
      fixtureGeneration: 'draft-append-fixture-v1',
    },
  };

  await assert.rejects(draftAppendWorkflow({ page: {}, report, nativeRequestLedger: ledger }, context), /ENOENT/);

  const [scope] = ledger.snapshotAll();
  assert.equal(scope.nativeRequestTerminalLedger.project, project);
  assert.equal(scope.nativeRequestTerminalLedger.explorer, null);
  assert.equal(scope.nativeRequestTerminalLedger.complete, false);
  assert.deepEqual(report.nativeRequestFlushFailure.terminalLedger, scope.nativeRequestTerminalLedger);
});
