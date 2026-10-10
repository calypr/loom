import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  startingCollectionNativePreviewReceiptEvidence,
  startingCollectionPreviewMatchesExpected,
  startingCollectionPreviewRequestMatchesReceipt,
  startingCollectionReconcileRequestMatchesSavedDraft,
  startingCollectionReconcileReceiptEvidence,
  startingCollectionRenderedPreviewDraftEvidence,
  startingCollectionVisiblePreviewSnapshot,
} from '../../workflows/verify-cda-starting-collection-handoff.mjs';

const expected = {
  path: '/api/v1/projects/p/explorers/e/authoring/v2/reconcile',
  previewPath: '/api/v1/projects/p/explorers/e/authoring/v2/preview',
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  authorizationScopeDigest: 'scope-1',
  draftVersion: 3,
  draftDigest: 'digest-3',
  outputId: 'output-1',
  savedWorkspace: {
    documents: [{ output: { id: 'output-1' }, population: { selectionRevisionId: 'selection-1' }, route: [{ relationship: 'subject_Patient' }] }],
  },
};

const reconcileRequest = { method: 'POST', path: expected.path, status: 200 };
const reconcileBody = {
  snapshotToken: expected.snapshotToken,
  draftVersion: expected.draftVersion,
  draftDigest: expected.draftDigest,
};
const receiptId = 'receipt-3';
const reconcileResponse = {
  kind: 'ExplorerBuilderReceipt',
  receiptId,
  snapshotToken: expected.snapshotToken,
  generation: expected.generation,
  authorizationScopeDigest: expected.authorizationScopeDigest,
  builder: {
    documents: [{ output: { id: 'output-1' }, population: { selectionRevisionId: 'selection-1' }, route: [{ relationship: 'subject_Patient' }] }],
  },
  outputs: [{ outputId: expected.outputId }],
};

test('keeps preview predicate and snapshot callback shapes browser-serializable', async () => {
  assert.match(startingCollectionPreviewMatchesExpected.toString(), /header\.textContent/);
  assert.match(startingCollectionVisiblePreviewSnapshot.toString(), /header\.textContent/);
  assert.doesNotMatch(startingCollectionPreviewMatchesExpected.toString(), /startingCollectionPreview/);
  assert.doesNotMatch(startingCollectionVisiblePreviewSnapshot.toString(), /startingCollectionPreview/);
  const source = await readFile(new URL('../../workflows/verify-cda-starting-collection-handoff.mjs', import.meta.url), 'utf8');
  assert.match(source, /await wait\(startingCollectionPreviewMatchesExpected, \[expected\]\)/);
  assert.match(source, /cda\.inspect\(startingCollectionVisiblePreviewSnapshot\)/);
});

test('binds a reconcile receipt to the exact saved draft CAS and output', () => {
  assert.equal(startingCollectionReconcileRequestMatchesSavedDraft({
    request: reconcileRequest,
    requestBody: reconcileBody,
    expected,
  }), true);
  const evidence = startingCollectionReconcileReceiptEvidence({
    request: reconcileRequest,
    requestBody: reconcileBody,
    response: reconcileResponse,
    expected,
  });
  assert.deepEqual(evidence, { ok: true, failures: [], receiptId });
});

test('rejects stale CAS and a receipt that does not identify the exact output', () => {
  for (const requestBody of [
    { ...reconcileBody, draftVersion: 2 },
    { ...reconcileBody, draftDigest: 'stale-digest' },
  ]) {
    assert.equal(startingCollectionReconcileRequestMatchesSavedDraft({
      request: reconcileRequest,
      requestBody,
      expected,
    }), false);
  }
  for (const [requestBody, response] of [
    [{ ...reconcileBody, draftVersion: 2 }, reconcileResponse],
    [{ ...reconcileBody, draftDigest: 'stale-digest' }, reconcileResponse],
    [reconcileBody, { ...reconcileResponse, outputs: [{ outputId: 'other-output' }] }],
    [reconcileBody, { ...reconcileResponse, outputs: [{ outputId: expected.outputId }, { outputId: expected.outputId }] }],
    [reconcileBody, { ...reconcileResponse, generation: 'other-generation' }],
    [reconcileBody, { ...reconcileResponse, builder: { documents: [{ output: { id: expected.outputId }, population: {}, route: [] }] } }],
  ]) {
    assert.equal(startingCollectionReconcileReceiptEvidence({
      request: reconcileRequest,
      requestBody,
      response,
      expected,
    }).ok, false);
  }
});

test('binds both native preview request and response to the reconcile receipt', () => {
  const request = { method: 'POST', path: expected.previewPath, status: 200 };
  const requestBody = { receiptId, outputId: expected.outputId, limit: 25 };
  const response = { receiptId, outputId: expected.outputId, rowCount: 2 };
  assert.equal(startingCollectionPreviewRequestMatchesReceipt({
    request,
    requestBody,
    expected: { path: expected.previewPath, receiptId, outputId: expected.outputId },
  }), true);
  assert.equal(startingCollectionPreviewRequestMatchesReceipt({
    request,
    requestBody: { ...requestBody, receiptId: 'other-receipt' },
    expected: { path: expected.previewPath, receiptId, outputId: expected.outputId },
  }), false);
  assert.deepEqual(startingCollectionNativePreviewReceiptEvidence({
    request,
    requestBody,
    response,
    expected: { path: expected.previewPath, receiptId, outputId: expected.outputId },
  }), { ok: true, failures: [] });
  assert.equal(startingCollectionNativePreviewReceiptEvidence({
    request,
    requestBody: { ...requestBody, receiptId: 'other-receipt' },
    response,
    expected: { path: expected.previewPath, receiptId, outputId: expected.outputId },
  }).ok, false);
  assert.equal(startingCollectionNativePreviewReceiptEvidence({
    request,
    requestBody,
    response: { ...response, receiptId: 'other-receipt' },
    expected: { path: expected.previewPath, receiptId, outputId: expected.outputId },
  }).ok, false);
});

test('requires the rendered preview receipt and visible draft identity to agree', () => {
  const preview = {
    status: 'ready',
    receiptId,
    outputId: expected.outputId,
    draftVersion: String(expected.draftVersion),
    draftDigest: expected.draftDigest,
  };
  assert.deepEqual(startingCollectionRenderedPreviewDraftEvidence({ preview, expected: { ...expected, receiptId } }), {
    ok: true,
    failures: [],
  });
  assert.equal(startingCollectionRenderedPreviewDraftEvidence({
    preview: { ...preview, draftDigest: 'stale-digest' },
    expected: { ...expected, receiptId },
  }).ok, false);
});

test('indexes the next preview in the harness-owned native request ledger', async () => {
  const source = await readFile(new URL('../../workflows/verify-cda-starting-collection-handoff.mjs', import.meta.url), 'utf8');
  assert.match(source,
    /fromIndex:\s*cda\.report\.nativeRequests\.indexOf\(reconcileRequest\)\s*\+\s*1/);
  assert.doesNotMatch(source,
    /fromIndex:\s*report\.nativeRequests\.indexOf\(reconcileRequest\)/);
});
