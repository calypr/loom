import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { createTemporalFilterCapture } from '../temporal-filter-capture.mjs';
import { fixtureProjectExplorerCollectionPath } from '../fixture-context.mjs';

test('capture checkpoints raw state before its one bounded drain and retains only eligible pending reads', async () => {
  const page = new EventEmitter();
  const mainFrame = {};
  page.mainFrame = () => mainFrame;
  const project = 'loom_dev_verify_mvo4s9f6-9ed2356';
  const explorerId = 'verify-f6-9ed2356-patient-birthdate-filter';
  const outputId = 'out_569d20ac294617a85bde9018';
  const uiOrigin = 'http://127.0.0.1:30000';
  const browserExplorerRoot = `${fixtureProjectExplorerCollectionPath(project)}/${encodeURIComponent(explorerId)}`;
  const scope = { uiOrigin, project, explorerId, browserExplorerRoot, outputId };
  const request = {};
  const now = Date.now();
  const entry = {
    index: 70,
    browserRequestId: 'playwright-71',
    requestId: 'construction-capabilities-6da28800-fa44-4dae-9b23-2c8e43788e3d',
    method: 'POST',
    origin: uiOrigin,
    path: `${browserExplorerRoot}/authoring/v2/construction-capabilities`,
    status: undefined,
    request: {
      snapshotToken: 'sha256:4bbc822872738fe0a3f2d118615ab35d6db6a48797642a3b10d78ebd807babf1',
      expectedDraftVersion: 6,
      expectedDraftDigest: 'sha256:61c7833c95409f45ee599459ece42438eab64cecc688f0c230b2b4ccb339891f',
      outputId,
      stageId: 'source_projection',
    },
    response: undefined,
    startedAt: now,
    responseReceivedAt: undefined,
    completedAt: undefined,
    responseMountEpoch: undefined,
    failureMountEpoch: undefined,
    isMutation: false,
    failure: undefined,
    readCancellationDiagnostic: false,
    responseCapture: 'not-configured',
    responseCaptureComplete: true,
    responseBodyCaptured: false,
    networkComplete: false,
  };
  const networkObserver = {
    requests: [],
    byRequest: new Map(),
    requestBody: observed => observed.request,
    responseBody: observed => observed.response,
    waitForResponse: (_predicate, { timeoutMs }) => new Promise((_resolve, reject) => {
      setTimeout(() => reject(new Error(`Timed out waiting for the scoped response after ${timeoutMs}ms.`)), timeoutMs);
    }),
  };
  const capture = createTemporalFilterCapture({
    page,
    networkObserver,
    initialMountEpoch: 5,
    finishDrainTimeoutMs: 25,
  });
  networkObserver.requests.push(entry);
  networkObserver.byRequest.set(request, entry);
  page.emit('request', request);
  const builderRead = capture.beginBuilderRead();
  capture.finishBuilderRead(builderRead, { status: 200, headers: { get: () => null } }, {
    catalog: { generation: 'fixture-v1', snapshotToken: entry.request.snapshotToken },
    draftVersion: entry.request.expectedDraftVersion,
    draftDigest: entry.request.expectedDraftDigest,
    workspace: { documents: [{ output: { id: outputId } }] },
  });
  capture.recordStep({ label: 'capture-checkpoint-test-step', beforeReadIndex: 0 });
  const metadata = { pendingReadScope: scope, resourceType: 'Patient' };
  const checkpoint = capture.checkpoint(metadata);
  const report = { temporalFilterCaptureCheckpoint: checkpoint };
  const reportRoundTrip = JSON.parse(JSON.stringify(report));
  assert.deepEqual(reportRoundTrip.temporalFilterCaptureCheckpoint.pendingObserverIndexes, [70]);
  assert.deepEqual(reportRoundTrip.temporalFilterCaptureCheckpoint.pendingBrowserRequestIds, ['playwright-71']);
  assert.equal(reportRoundTrip.temporalFilterCaptureCheckpoint.builderReads[0].draftVersion, 6);
  assert.equal(reportRoundTrip.temporalFilterCaptureCheckpoint.steps[0].label, 'capture-checkpoint-test-step');

  const raw = await capture.finish({ ...metadata, checkpointId: checkpoint.checkpointId });
  assert.equal(raw.captureExit.checkpointId, checkpoint.checkpointId);
  assert.equal(raw.captureExit.drainTimeoutMs, 25,
    'The injected short deadline is only a focused test seam; production uses the fixed 5000ms default.');
  assert.deepEqual(raw.captureExit.pendingObserverIndexes, [70]);
  assert.deepEqual(raw.captureExit.pendingBrowserRequestIds, ['playwright-71']);
  assert.deepEqual(raw.captureExit.pendingResponseReadObserverIndexes, []);
  assert.equal(raw.requestEvents[0].status, null);
  assert.equal(raw.requestEvents[0].response, null);
  assert.equal(raw.requestEvents[0].responseReceivedAt, null);
  assert.equal(raw.requestEvents[0].completedAt, null);
  assert.equal(raw.requestEvents[0].settlement, 'pending');
});

test('capture failure checkpoint preserves response headers and an unfinished body read', async () => {
  const page = new EventEmitter();
  const mainFrame = {};
  page.mainFrame = () => mainFrame;
  const request = {};
  const now = Date.now();
  const entry = {
    index: 4,
    browserRequestId: 'partial-capability-response',
    requestId: 'partial-capability-wire-id',
    method: 'POST',
    origin: 'http://127.0.0.1:30000',
    path: '/api/v1/projects/p/explorers/e/authoring/v2/construction-capabilities',
    status: 200,
    request: { outputId: 'out-1' },
    response: undefined,
    startedAt: now,
    responseReceivedAt: now + 1,
    completedAt: undefined,
    responseMountEpoch: undefined,
    failureMountEpoch: undefined,
    isMutation: false,
    failure: undefined,
    readCancellationDiagnostic: false,
    responseCapture: 'pending',
    responseCaptureComplete: false,
    responseBodyCaptured: false,
    networkComplete: true,
  };
  const networkObserver = {
    requests: [],
    byRequest: new Map(),
    requestBody: observed => observed.request,
    responseBody: observed => observed.response,
    waitForResponse: (_predicate, { timeoutMs }) => new Promise((_resolve, reject) => {
      setTimeout(() => reject(new Error(`Timed out waiting for the scoped response after ${timeoutMs}ms.`)), timeoutMs);
    }),
  };
  const capture = createTemporalFilterCapture({ page, networkObserver, finishDrainTimeoutMs: 15 });
  networkObserver.requests.push(entry);
  networkObserver.byRequest.set(request, entry);
  page.emit('request', request);
  page.emit('response', { request: () => request });
  const metadata = { pendingReadScope: {}, resourceType: 'Patient' };
  const preDrain = capture.checkpoint(metadata);
  let failure;
  try {
    await capture.finish({ ...metadata, checkpointId: preDrain.checkpointId });
  } catch (error) {
    failure = error;
  }
  assert.match(failure?.message ?? '', /response-body read is pending/u);
  const failureCheckpoint = capture.checkpoint(metadata);
  const report = JSON.parse(JSON.stringify({
    temporalFilterCaptureCheckpoint: preDrain,
    temporalFilterCaptureFailure: { message: failure.message },
    temporalFilterCaptureFailureCheckpoint: failureCheckpoint,
  }));
  const retained = report.temporalFilterCaptureFailureCheckpoint.requestEvents[0];
  assert.equal(retained.status, 200);
  assert.equal(retained.responseReceivedAt, now + 1);
  assert.equal(retained.responseCaptureComplete, false);
  assert.equal(retained.responseBodyCaptured, false);
  assert.equal(retained.settlement, 'response-body-pending');
});
