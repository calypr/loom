import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureOwnedPreviewLifecycle } from '../../workflows/builder-combine-draft.mjs';
import { classifyNetworkRecord } from '../report.mjs';

const request = (url, body, errorText = 'net::ERR_ABORTED') => ({
  method: () => 'POST',
  url: () => url,
  postDataJSON: () => body,
  headers: () => body.requestId ? { 'x-request-id': body.requestId } : {},
  failure: () => ({ errorText }),
});
const response = (forRequest, status, body) => ({
  request: () => forRequest,
  status: () => status,
  json: async () => body,
});
const tick = () => new Promise((resolve) => setTimeout(resolve, 5));

test('owned preview capture records exact abort, receipt binding, and visible state without inventing a successor', async () => {
  const base = 'http://127.0.0.1:30008/api/v1/projects/loom_dev_verify_owned/explorers/verify-owned/authoring/v2';
  const target = {
    uiUrl: 'http://127.0.0.1:30008',
    apiUrl: 'http://127.0.0.1:8188',
    fixtureProject: 'loom_dev_verify_owned',
    fixtureGeneration: 'group-pivot-append-wave138',
  };
  const page = new EventEmitter();
  let visible = {
    selectedOutputId: 'out-observation-group',
    preview: { status: 'ready', outputId: 'out-observation-group', receiptId: 'receipt-source', stale: false },
    proposal: null,
    userVisiblePreviewError: false,
  };
  page.evaluate = async () => structuredClone(visible);
  const capture = captureOwnedPreviewLifecycle(page, target, 'verify-owned', { diagnostic: true });

  const sourceReconcile = request(base + '/reconcile', {
    snapshotToken: 'snapshot-source', draftVersion: 5, draftDigest: 'digest-source', requestId: 'builder-source',
  });
  page.emit('request', sourceReconcile);
  page.emit('response', response(sourceReconcile, 200, {
    receiptId: 'receipt-source', snapshotToken: 'snapshot-source',
    outputs: [{ outputId: 'out-observation-group' }],
  }));
  page.emit('requestfinished', sourceReconcile);

  const aborted = request(base + '/preview', {
    requestId: 'request-preview-source', receiptId: 'receipt-source', outputId: 'out-observation-group', limit: 25,
  });
  page.emit('request', aborted);
  await tick();
  visible = {
    selectedOutputId: 'out-combine-target',
    preview: { status: 'loading', outputId: 'out-combine-target', receiptId: 'receipt-target', stale: false },
    proposal: null,
    userVisiblePreviewError: false,
  };
  page.emit('requestfailed', aborted);
  await tick();

  const targetReconcile = request(base + '/reconcile', {
    snapshotToken: 'snapshot-target', draftVersion: 6, draftDigest: 'digest-target', requestId: 'builder-target',
  });
  page.emit('request', targetReconcile);
  page.emit('response', response(targetReconcile, 200, {
    receiptId: 'receipt-target', snapshotToken: 'snapshot-target',
    outputs: [{ outputId: 'out-combine-target' }],
  }));
  page.emit('requestfinished', targetReconcile);

  const successor = request(base + '/preview', {
    requestId: 'request-preview-target', receiptId: 'receipt-target', outputId: 'out-combine-target', limit: 25,
  });
  page.emit('request', successor);
  page.emit('response', response(successor, 200, {
    receiptId: 'receipt-target', outputId: 'out-combine-target', rowCount: 7,
  }));
  visible = {
    selectedOutputId: 'out-combine-target',
    preview: { status: 'ready', outputId: 'out-combine-target', receiptId: 'receipt-target', stale: false },
    proposal: null,
    userVisiblePreviewError: false,
  };
  page.emit('requestfinished', successor);
  await tick();

  const secondAbort = request(base + '/preview', {
    requestId: 'request-preview-second-abort', receiptId: 'receipt-target', outputId: 'out-combine-target', limit: 25,
  });
  page.emit('request', secondAbort);
  page.emit('requestfailed', secondAbort);
  await tick();
  const wrongVisibleSuccessor = request(base + '/preview', {
    requestId: 'request-preview-wrong-visible', receiptId: 'receipt-target', outputId: 'out-combine-target', limit: 25,
  });
  page.emit('request', wrongVisibleSuccessor);
  page.emit('response', response(wrongVisibleSuccessor, 200, {
    receiptId: 'receipt-target', outputId: 'out-combine-target', rowCount: 7,
  }));
  visible = {
    selectedOutputId: 'out-another-table',
    preview: { status: 'ready', outputId: 'out-another-table', receiptId: 'receipt-other', stale: false },
    proposal: null,
    userVisiblePreviewError: false,
  };
  page.emit('requestfinished', wrongVisibleSuccessor);

  const futureReceiptPreview = request(base + '/preview', {
    requestId: 'request-preview-future-receipt', receiptId: 'receipt-reconciled-later', outputId: 'out-future', limit: 25,
  });
  page.emit('request', futureReceiptPreview);
  page.emit('response', response(futureReceiptPreview, 200, {
    receiptId: 'receipt-reconciled-later', outputId: 'out-future', rowCount: 1,
  }));
  page.emit('requestfinished', futureReceiptPreview);
  const laterReconcile = request(base + '/reconcile', {
    snapshotToken: 'snapshot-later', draftVersion: 7, draftDigest: 'digest-later', requestId: 'builder-later',
  });
  page.emit('request', laterReconcile);
  page.emit('response', response(laterReconcile, 200, {
    receiptId: 'receipt-reconciled-later', snapshotToken: 'snapshot-later', outputs: [{ outputId: 'out-future' }],
  }));
  page.emit('requestfinished', laterReconcile);

  const otherExplorer = request(base.replace('/verify-owned/', '/verify-other/') + '/preview', {
    receiptId: 'receipt-other', outputId: 'out-other', limit: 25,
  });
  page.emit('request', otherExplorer);
  await tick();
  const evidence = await capture.stop();

  assert.deepEqual(evidence.scope, {
    projectId: 'loom_dev_verify_owned',
    fixtureGeneration: 'group-pivot-append-wave138',
    explorerId: 'verify-owned',
    origins: ['http://127.0.0.1:30008', 'http://127.0.0.1:8188'],
    paths: {
      reconcile: base.slice(new URL(base).origin.length) + '/reconcile',
      preview: base.slice(new URL(base).origin.length) + '/preview',
    },
  });
  assert.equal(evidence.requests.length, 8, 'the collector ignores a different Explorer path');
  const observedAbort = evidence.requests.find((entry) => entry.outputId === 'out-observation-group' && entry.kind === 'preview');
  assert.equal(observedAbort.kind, 'preview');
  assert.equal(observedAbort.errorText, 'net::ERR_ABORTED');
  assert.equal(observedAbort.networkRequestId, 'request-preview-source');
  assert.ok(observedAbort.failedAtMonotonicMs >= observedAbort.startedAtMonotonicMs);
  assert.deepEqual({ ...observedAbort.receiptBinding, reconciledAtMonotonicMs: undefined }, {
    snapshotToken: 'snapshot-source',
    draftVersion: 5,
    draftDigest: 'digest-source',
    responseSnapshotToken: 'snapshot-source',
    outputIds: ['out-observation-group'],
    reconciledAtMonotonicMs: undefined,
  });
  assert.ok(observedAbort.receiptBinding.reconciledAtMonotonicMs <= observedAbort.startedAtMonotonicMs);
  assert.equal(observedAbort.receiptBindingMatchesOutput, true);
  assert.equal(observedAbort.visibleAfterFailure.selectedOutputId, 'out-combine-target');
  assert.equal(evidence.abortedPreviews.length, 2);
  assert.equal(Object.hasOwn(observedAbort, 'successors'), false,
    'the collector does not invent an immediate successor for an empty CREATE_TABLE target');
  assert.equal(evidence.abortedPreviews[1].outputId, 'out-combine-target');
  assert.equal(evidence.abortedPreviews[1].visibleAfterFailure.selectedOutputId, 'out-combine-target');
  const futureReceipt = evidence.requests.find((entry) => entry.outputId === 'out-future' && entry.kind === 'preview');
  assert.equal(futureReceipt.receiptBinding, null, 'a later reconcile response cannot retroactively own a preview request');
  assert.equal(classifyNetworkRecord({ kind: 'network', errorText: 'net::ERR_ABORTED' }), 'unexpected-error',
    'the diagnostic capture does not relax global network classification');
});

test('existing preview-event consumers retain their original bounded event report', () => {
  const target = {
    uiUrl: 'http://127.0.0.1:30008',
    apiUrl: 'http://127.0.0.1:8188',
    fixtureProject: 'loom_dev_verify_owned',
  };
  const explorer = 'verify-owned';
  const page = new EventEmitter();
  const capture = captureOwnedPreviewLifecycle(page, target, explorer);
  const preview = request('http://127.0.0.1:30008/api/v1/projects/loom_dev_verify_owned/explorers/verify-owned/authoring/v2/preview', {
    receiptId: 'receipt-1', outputId: 'out-1', limit: 25,
  });
  page.emit('request', preview);
  page.emit('response', response(preview, 200, { receiptId: 'receipt-1', outputId: 'out-1' }));
  page.emit('requestfinished', preview);
  const evidence = capture.stop();
  assert.equal(evidence.path.endsWith('/preview'), true);
  assert.equal(evidence.maxEvents, 256);
  assert.equal(evidence.droppedEvents, 0);
  assert.deepEqual(evidence.events.map((event) => event.event), ['request', 'response', 'finished']);
  assert.equal(evidence.events[0].outputId, 'out-1');
});

test('diagnostic stop bounds never-resolving response and page reads and marks evidence incomplete', async () => {
  const base = 'http://127.0.0.1:30008/api/v1/projects/loom_dev_verify_owned/explorers/verify-owned/authoring/v2';
  const target = {
    uiUrl: 'http://127.0.0.1:30008',
    apiUrl: 'http://127.0.0.1:8188',
    fixtureProject: 'loom_dev_verify_owned',
    fixtureGeneration: 'group-pivot-append-timeout',
  };
  const page = new EventEmitter();
  let resolveLatePageRead;
  page.evaluate = () => new Promise((resolve) => { resolveLatePageRead = resolve; });
  const capture = captureOwnedPreviewLifecycle(page, target, 'verify-owned', { diagnostic: true });
  let resolveLateBodyRead;
  const unresolved = new Promise((resolve) => { resolveLateBodyRead = resolve; });
  const reconcile = request(base + '/reconcile', {
    snapshotToken: 'snapshot-timeout', draftVersion: 1, draftDigest: 'digest-timeout', requestId: 'builder-timeout',
  });
  page.emit('request', reconcile);
  page.emit('response', { request: () => reconcile, status: () => 200, json: () => unresolved });
  page.emit('requestfinished', reconcile);
  const preview = request(base + '/preview', {
    receiptId: 'receipt-timeout', outputId: 'out-timeout', limit: 25,
  });
  page.emit('request', preview);
  page.emit('requestfailed', preview);

  let deadline;
  const stopResult = await Promise.race([
    capture.stop().then((evidence) => ({ evidence })),
    new Promise((resolve) => { deadline = setTimeout(() => resolve(null), 1800); }),
  ]);
  if (deadline) clearTimeout(deadline);
  assert.ok(stopResult, 'diagnostic stop returns within its bounded read-flush interval');
  const evidence = stopResult.evidence;
  assert.equal(evidence.incompleteEvidence, true);
  assert.equal(evidence.diagnosticReadFlushTimeoutMs, 1000);
  assert.deepEqual(evidence.timedOutReads.map((read) => read.kind).sort(), [
    'failure-visible-state', 'reconcile-response-body',
  ]);
  assert.equal(evidence.abortedPreviews[0].evidenceReadTimedOut, true);
  assert.equal(evidence.abortedPreviews[0].receiptBinding, null);
  resolveLateBodyRead({ receiptId: 'receipt-timeout', snapshotToken: 'snapshot-timeout', outputs: [{ outputId: 'out-timeout' }] });
  resolveLatePageRead({ selectedOutputId: 'out-timeout', preview: { status: 'READY' }, userVisiblePreviewError: false });
  await tick();
  assert.equal(evidence.requests.find((entry) => entry.kind === 'reconcile').response, undefined,
    'a response body that finishes after the diagnostic deadline cannot mutate returned evidence');
  assert.equal(evidence.abortedPreviews[0].visibleAfterFailure, undefined,
    'a page read that finishes after the diagnostic deadline cannot mutate returned evidence');
  assert.equal(evidence.incompleteEvidence, true, 'late reads do not restore classification eligibility');
});
