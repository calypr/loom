import assert from 'node:assert/strict';
import test from 'node:test';
import { createFixtureNativeRequestLedger } from '../native-request-ledger.mjs';
import {
  flushDraftCombineNativeRequestScope,
  groupPivotJoinWorkflow,
  openDraftCombineNativeRequestScope,
} from '../../workflows/builder-combine-draft.mjs';

const origin = 'http://127.0.0.1:30008';
const project = 'loom_dev_verify_group_pivot_ledger';
const explorer = 'fresh-group-pivot-ledger-explorer';
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

test('Group/Pivot flush retains only the selected fixture project and Explorer ledger', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = openDraftCombineNativeRequestScope(ledger, target, 'Draft Group/Pivot');
  recordFinished(ledger, request(projectPath, 'POST'), 'group-pivot-explorer-create', 201);
  recordFinished(ledger, request(`${explorerPath}/authoring/v2/builder`), 'group-pivot-builder-read');
  recordFinished(ledger, request(`${explorerPath}/authoring/v2/construction-proposals`, 'POST'), 'group-pivot-preview');
  recordFinished(ledger, request(`${projectPath}/sibling-explorer/authoring/v2/builder`), 'group-pivot-sibling-read');
  const report = {};

  const snapshot = await flushDraftCombineNativeRequestScope({
    nativeRequestLedger: ledger,
    scope,
    explorer,
    report,
    timeoutMs: 20,
    label: 'Draft Group/Pivot',
  });

  assert.equal(snapshot.nativeRequestTerminalLedger.project, project);
  assert.equal(snapshot.nativeRequestTerminalLedger.explorer, explorer);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, true);
  assert.deepEqual(snapshot.nativeRequestTerminalLedger.requests.map(entry => entry.requestId), [
    'group-pivot-explorer-create', 'group-pivot-builder-read', 'group-pivot-preview',
  ]);
  assert.deepEqual(snapshot.excludedNativeRequests.map(entry => entry.requestId), ['group-pivot-sibling-read']);
  assert.equal(report.nativeRequestTerminalLedger, snapshot.nativeRequestTerminalLedger);
});

test('Group/Pivot flush fails when an owned request remains pending', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = openDraftCombineNativeRequestScope(ledger, target, 'Draft Group/Pivot');
  recordFinished(ledger, request(projectPath, 'POST'), 'group-pivot-explorer-create', 201);
  recordFinished(ledger, request(`${explorerPath}/authoring/v2/builder`), 'group-pivot-builder-read');
  const pending = request(`${explorerPath}/authoring/v2/construction-proposals`, 'POST');
  ledger.recordRequest(pending, {
    requestId: 'group-pivot-preview-pending',
    browserRequestId: 'browser-group-pivot-preview-pending',
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
    label: 'Draft Group/Pivot',
  }), /Draft Group\/Pivot native request ledger did not reach a complete terminal state/);

  assert.equal(report.nativeRequestTerminalLedger.complete, false);
  assert.equal(report.nativeRequestTerminalLedger.counts.pending, 1);
  assert.equal(report.nativeRequestTerminalLedger.requests.find(entry =>
    entry.requestId === 'group-pivot-preview-pending')?.state, 'pending');
});

test('Group/Pivot workflow flushes its native scope if setup fails before Explorer creation', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const report = { target: {} };
  const context = {
    custom: false,
    seed: { fresh: true },
    runID: 'group-pivot-setup-failure',
    target: {
      ...target,
      fixtureDir: '/private/tmp/loom-case022-missing-fixture',
      fixtureGeneration: 'draft-combine-fixture-v1',
    },
  };

  await assert.rejects(
    groupPivotJoinWorkflow({ page: {}, report, nativeRequestLedger: ledger }, context),
    /ENOENT/,
  );

  const [scope] = ledger.snapshotAll();
  assert.equal(scope.nativeRequestTerminalLedger.project, project);
  assert.equal(scope.nativeRequestTerminalLedger.explorer, null);
  assert.equal(scope.nativeRequestTerminalLedger.complete, false);
  assert.equal(report.nativeRequestFlushFailure.terminalLedger.complete, false);
});
