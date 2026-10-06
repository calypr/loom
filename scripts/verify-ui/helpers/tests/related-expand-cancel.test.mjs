import assert from 'node:assert/strict';
import test from 'node:test';
import {
  classifyPendingRelatedExpandChoicesAfterProposalCancel,
  proposalCancelActionSelector,
  snapshotPendingRelatedExpandChoices,
} from '../related-expand-cancel.mjs';

const origin = 'http://127.0.0.1:30008';
const path = '/api/v1/projects/loom_dev_cda_fhir/explorers/qa-owned/authoring/v2/related-expand-choices';
const identity = {
  origin, path, outputId: 'out-owned', draftVersion: 7, draftDigest: 'sha256:draft-7', stageId: 'source_projection',
};

const entry = (overrides = {}) => ({
  origin,
  path,
  method: 'POST',
  requestId: 'related-expand-choices-request-1',
  browserRequestId: 'playwright-1',
  startedAt: 1000,
  body: {
    outputId: identity.outputId,
    expectedDraftVersion: identity.draftVersion,
    expectedDraftDigest: identity.draftDigest,
    stageId: identity.stageId,
    anchorColumnId: '_key',
    targetResourceType: 'Patient',
  },
  ...overrides,
});

const action = { label: proposalCancelActionSelector, startedAt: 1100, completedAt: 1120 };

test('snapshot keeps only exact pending owned choices pages for the saved output, draft, and stage', () => {
  const pending = entry();
  const requests = [
    pending,
    entry({ browserRequestId: 'playwright-other-output', requestId: 'related-expand-choices-other-output', body: { ...entry().body, outputId: 'out-other' } }),
    entry({ browserRequestId: 'playwright-other-draft', requestId: 'related-expand-choices-other-draft', body: { ...entry().body, expectedDraftVersion: 6 } }),
    entry({ browserRequestId: 'playwright-other-digest', requestId: 'related-expand-choices-other-digest', body: { ...entry().body, expectedDraftDigest: 'sha256:other-draft' } }),
    entry({ browserRequestId: 'playwright-other-stage', requestId: 'related-expand-choices-other-stage', body: { ...entry().body, stageId: 'related_expand_other' } }),
    entry({ browserRequestId: 'playwright-other-path', requestId: 'related-expand-choices-other-path', path: `${path}/other` }),
    entry({ browserRequestId: 'playwright-started-after-snapshot', requestId: 'related-expand-choices-future', startedAt: 1100 }),
    entry({ browserRequestId: 'playwright-completed', requestId: 'related-expand-choices-completed', status: 200, completedAt: 1090 }),
    entry({ browserRequestId: 'playwright-preexisting-failure', requestId: 'related-expand-choices-preexisting', failure: 'net::ERR_ABORTED', completedAt: 1095 }),
  ];

  const snapshot = snapshotPendingRelatedExpandChoices(requests, { ...identity, capturedAt: 1099 });
  assert.equal(snapshot.entries.length, 1);
  assert.equal(snapshot.entries[0].entry, pending);
  assert.equal(snapshot.entries[0].requestId, pending.requestId);
  assert.deepEqual(snapshot.identity, identity);
});

test('only a snapshotted request aborted during explicit Cancel-to-editor-close is classified', () => {
  const expected = entry();
  const unrelated = entry({
    requestId: 'related-expand-choices-unrelated', browserRequestId: 'playwright-unrelated',
    body: { ...entry().body, outputId: 'out-unrelated' },
  });
  const requests = [expected, unrelated];
  const snapshot = snapshotPendingRelatedExpandChoices(requests, { ...identity, capturedAt: 1099 });
  expected.failure = 'net::ERR_ABORTED';
  expected.completedAt = 1115;
  unrelated.failure = 'net::ERR_ABORTED';
  unrelated.completedAt = 1116;
  const classified = [];
  const cda = {
    nativeRequests: requests,
    expectCapturedCancellation(captured, reason, proof) {
      classified.push({ captured, reason, proof });
    },
  };

  assert.deepEqual(classifyPendingRelatedExpandChoicesAfterProposalCancel({
    cda, snapshot, action, editorClosed: true, editorClosedAt: 1130,
    reason: 'Explicitly canceled the saved Related edit; its route choices are no longer needed.',
  }), ['playwright-1']);
  assert.equal(classified.length, 1);
  assert.equal(classified[0].captured, expected);
  assert.equal(classified[0].proof.action, proposalCancelActionSelector);
  assert.deepEqual(classified[0].proof.identity, identity);
  assert.deepEqual(classified[0].proof.requestId, expected.requestId);
});

test('wrong Cancel identity, missing editor closure, stale snapshot, or outside-window abort cannot be classified', () => {
  const makeState = () => {
    const pending = entry();
    const requests = [pending];
    const snapshot = snapshotPendingRelatedExpandChoices(requests, { ...identity, capturedAt: 1099 });
    const classified = [];
    return {
      pending, requests, snapshot, classified,
      cda: { nativeRequests: requests, expectCapturedCancellation: (...args) => classified.push(args) },
    };
  };

  const wrongAction = makeState();
  wrongAction.pending.failure = 'net::ERR_ABORTED';
  wrongAction.pending.completedAt = 1115;
  assert.throws(() => classifyPendingRelatedExpandChoicesAfterProposalCancel({
    cda: wrongAction.cda, snapshot: wrongAction.snapshot,
    action: { ...action, label: 'test teardown' }, editorClosed: true, editorClosedAt: 1130, reason: 'cancel',
  }), /explicit proposal Cancel/);
  assert.equal(wrongAction.classified.length, 0);

  const notClosed = makeState();
  notClosed.pending.failure = 'net::ERR_ABORTED';
  notClosed.pending.completedAt = 1115;
  assert.throws(() => classifyPendingRelatedExpandChoicesAfterProposalCancel({
    cda: notClosed.cda, snapshot: notClosed.snapshot, action, editorClosed: false, editorClosedAt: 1110, reason: 'cancel',
  }), /after the editor has closed/);
  assert.equal(notClosed.classified.length, 0);

  const stale = makeState();
  stale.pending.failure = 'net::ERR_ABORTED';
  stale.pending.completedAt = 3100;
  assert.throws(() => classifyPendingRelatedExpandChoicesAfterProposalCancel({
    cda: stale.cda, snapshot: stale.snapshot,
    action: { ...action, startedAt: 2200, completedAt: 2210 }, editorClosed: true, editorClosedAt: 2230, reason: 'cancel',
  }), /immediately precede/);
  assert.equal(stale.classified.length, 0);

  const outside = makeState();
  outside.pending.failure = 'net::ERR_ABORTED';
  outside.pending.completedAt = 1131;
  assert.deepEqual(classifyPendingRelatedExpandChoicesAfterProposalCancel({
    cda: outside.cda, snapshot: outside.snapshot, action, editorClosed: true, editorClosedAt: 1130, reason: 'cancel',
  }), []);
  assert.equal(outside.classified.length, 0);
});

test('non-abort failures and requests added after the snapshot stay unexpected', () => {
  const pending = entry();
  const requests = [pending];
  const snapshot = snapshotPendingRelatedExpandChoices(requests, { ...identity, capturedAt: 1099 });
  pending.failure = 'net::ERR_FAILED';
  pending.completedAt = 1115;
  const later = entry({ requestId: 'related-expand-choices-later', browserRequestId: 'playwright-later', startedAt: 1105,
    failure: 'net::ERR_ABORTED', completedAt: 1117 });
  requests.push(later);
  const classified = [];
  const cda = { nativeRequests: requests, expectCapturedCancellation: (...args) => classified.push(args) };

  assert.deepEqual(classifyPendingRelatedExpandChoicesAfterProposalCancel({
    cda, snapshot, action, editorClosed: true, editorClosedAt: 1130, reason: 'cancel',
  }), []);
  assert.equal(classified.length, 0);
});

test('request identity mutation after snapshot is rejected', () => {
  const pending = entry();
  const requests = [pending];
  const snapshot = snapshotPendingRelatedExpandChoices(requests, { ...identity, capturedAt: 1099 });
  pending.body.expectedDraftVersion = 8;
  pending.failure = 'net::ERR_ABORTED';
  pending.completedAt = 1115;
  const cda = { nativeRequests: requests, expectCapturedCancellation() { assert.fail('must not classify changed identity'); } };

  assert.throws(() => classifyPendingRelatedExpandChoicesAfterProposalCancel({
    cda, snapshot, action, editorClosed: true, editorClosedAt: 1130, reason: 'cancel',
  }), /changed output, draft, stage, or owned route identity/);
});

test('ambiguous request IDs cannot enter the pre-Cancel snapshot', () => {
  const first = entry();
  const duplicate = entry({ browserRequestId: 'playwright-duplicate', startedAt: 1001 });
  const requests = [first, duplicate];

  assert.throws(() => snapshotPendingRelatedExpandChoices(requests, { ...identity, capturedAt: 1099 }),
    /unique captured requestId/);
});
