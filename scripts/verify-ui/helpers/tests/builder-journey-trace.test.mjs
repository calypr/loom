import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  assertNativeBuilderJourneyReplayReceipt,
  assertNativeBuilderJourneyMutationControls,
  assertNativeBuilderJourneySelfTestReceipt,
  builderJourneyTraceReceiptIdentity,
  captureBuilderJourneyTraceProvenance,
  createBuilderReadMetadataRegistry,
  finalizeNativeBuilderJourneyReceipt,
  nativeRequestLedgerFrom,
} from '../builder-journey-trace.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..');

const nativeTraceReceiptFixture = () => {
  const directory = mkdtempSync(join(tmpdir(), 'native-builder-journey-receipt-'));
  const tracePath = join(directory, 'trace.json');
  const traceDocument = {
    traces: [{
      name: 'cda-builder-group-related-source',
      nativeRequests: [
        { index: 0, browserRequestId: 'proposal-browser-0', requestId: 'wire-0', method: 'POST',
          path: '/api/v1/projects/p1/explorers/e1/authoring/v2/construction-proposals',
          startedAt: 100, isMutation: true },
        { index: 1, browserRequestId: 'proposal-browser-1', requestId: 'wire-1', method: 'POST',
          path: '/api/v1/projects/p1/explorers/e1/authoring/v2/construction-proposals',
          startedAt: 200, isMutation: true },
      ],
      actions: [
        { owner: { kind: 'proposal', browserRequestId: 'proposal-browser-0', mountEpoch: 0 } },
        { relatedSourceLifecycle: { owner: {
          kind: 'proposal', browserRequestId: 'proposal-browser-1', mountEpoch: 2,
        } } },
      ],
    }],
  };
  const bytes = Buffer.from(`${JSON.stringify(traceDocument, null, 2)}\n`);
  writeFileSync(tracePath, bytes);
  const traceSha256 = createHash('sha256').update(readFileSync(tracePath)).digest('hex');
  const contract = { journeyCase: {
    requiredMutationControlNames: ['positive-observed-projection', 'reject-related-input'],
    observedOwnerEpochControlPrefix: 'request-owner-mount-epoch-',
  } };
  const mutationControlNames = [
    ...contract.journeyCase.requiredMutationControlNames,
    'request-owner-mount-epoch-0',
    'request-owner-mount-epoch-2',
  ];
  const mutationControls = mutationControlNames.map(name => ({ name, status: 'pass' }));
  return {
    directory,
    tracePath,
    traceDocument,
    traceSha256,
    contract,
    mutationControls,
    selfTestReportPath: join(directory, 'self-test.json'),
  };
};

test('journey trace provenance binds reviewed source hashes and selected contract identity', () => {
  const manifestDirectory = mkdtempSync(join(tmpdir(), 'builder-journey-trace-'));
  const manifestPath = join(manifestDirectory, 'sources.json');
  const sourcePaths = [
    'scripts/verify-ui/workflows/builder-group-entry.mjs',
    'scripts/verify-ui/helpers/builder-journey-trace.mjs',
  ];
  const sources = sourcePaths.map(path => ({
    path,
    sha256: createHash('sha256').update(readFileSync(resolve(repositoryRoot, path))).digest('hex'),
  }));
  writeFileSync(manifestPath, JSON.stringify({ sources }));

  try {
    const provenance = captureBuilderJourneyTraceProvenance({
      repositoryRoot,
      contractPath: 'verification/builder-interactions/builder-edit-remove-contract.json',
      reviewedSourceManifestPath: manifestPath,
    });

    assert.equal(provenance.sourceProfile, 'journey');
    assert.match(provenance.sourceCommit, /^[0-9a-f]{40}$/);
    assert.equal(provenance.contractSourceCommit, '4a2a165fbf92f56cce85a143364368734215c538');
    assert.match(provenance.sourceFingerprint, /^[0-9a-f]{64}$/);
  } finally {
    rmSync(manifestDirectory, { recursive: true, force: true });
  }
});

test('journey trace provenance rejects a source pin that does not match the current file', () => {
  const manifestDirectory = mkdtempSync(join(tmpdir(), 'builder-journey-trace-drift-'));
  const manifestPath = join(manifestDirectory, 'sources.json');
  writeFileSync(manifestPath, JSON.stringify({ sources: [{
    path: 'scripts/verify-ui/helpers/builder-journey-trace.mjs',
    sha256: '0'.repeat(64),
  }] }));

  try {
    assert.throws(() => captureBuilderJourneyTraceProvenance({
      repositoryRoot,
      contractPath: 'verification/builder-interactions/builder-edit-remove-contract.json',
      reviewedSourceManifestPath: manifestPath,
    }), /Builder trace source pin drift/);
  } finally {
    rmSync(manifestDirectory, { recursive: true, force: true });
  }
});

test('receipt identity keeps legacy and native lifecycle case identities distinct', () => {
  assert.deepEqual(builderJourneyTraceReceiptIdentity({ captureKind: 'builder-group-legacy' },
    'builder-authoring:group-entry', 'builder-authoring', 'group-entry'), {
    caseKey: 'builder-authoring:group-entry',
  });
  assert.deepEqual(builderJourneyTraceReceiptIdentity({ captureKind: 'builder-cda-native-lifecycle' },
    'cda-builder-group-related-source', 'standalone-reshape-group-edit-before-related-column',
    'group-edit-before-related-column'), {
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
  });
});

test('native replay and mutation receipts preserve verifier coverage and require every mutation control to pass', () => {
  const fixture = nativeTraceReceiptFixture();
  try {
  const identity = {
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
    actionLabels: ['Preview', 'RelatedSourcePreview', 'Reload'],
    tracePath: fixture.tracePath,
  };
  const replayReceipt = {
    ...identity,
    traceSha256: fixture.traceSha256,
    replayReportPath: '/evidence/cda-tla.json',
    selfTestReportPath: null,
    status: 'pending-self-test',
    replayStatus: 'pass',
    selfTestStatus: 'not-run',
    requestCoverage: { observed: 11, modeled: 11, unmapped: 0, duplicates: 0 },
    requiredActionCountsByTrace: { mismatches: {} },
    requiredActionMinimumsByTrace: { mismatches: {} },
    unmodeledEvents: { total: 3, unclassified: { total: 0 } },
  };
  assert.strictEqual(assertNativeBuilderJourneyReplayReceipt(replayReceipt, identity), replayReceipt);
  const selfTestReportPath = fixture.selfTestReportPath;
  const selfTestReceipt = {
    traceName: identity.traceName,
    nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath,
    traceSha256: fixture.traceSha256,
    status: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath,
    mutationControls: fixture.mutationControls,
  };
  const selfTestIdentity = {
    traceName: identity.traceName,
    nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath,
    traceSha256: fixture.traceSha256,
    selfTestReportPath,
    contract: fixture.contract,
    traceDocument: fixture.traceDocument,
  };
  const validatedSelfTest = assertNativeBuilderJourneySelfTestReceipt(selfTestReceipt, selfTestIdentity);
  const finalReceipt = finalizeNativeBuilderJourneyReceipt(replayReceipt, validatedSelfTest, {
    ...identity,
    traceSha256: fixture.traceSha256,
    selfTestReportPath,
    contract: fixture.contract,
    traceDocument: fixture.traceDocument,
  });
  assert.deepEqual(finalReceipt.requestCoverage, replayReceipt.requestCoverage);
  assert.deepEqual(finalReceipt.unmodeledEvents, replayReceipt.unmodeledEvents);
  assert.deepEqual(finalReceipt.mutationControls, selfTestReceipt.mutationControls);
  assert.equal(finalReceipt.status, 'pass');
  assert.equal(finalReceipt.traceSha256, fixture.traceSha256);
  assert.equal(Object.hasOwn(finalReceipt, 'caseKey'), false);
  assert.equal(Object.hasOwn(selfTestReceipt, 'replayStatus'), false);
  assert.equal(Object.hasOwn(selfTestReceipt, 'replayReportPath'), false);
  assert.equal(Object.hasOwn(selfTestReceipt, 'requestCoverage'), false);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    ...selfTestReceipt,
    replayReportPath: replayReceipt.replayReportPath,
  }, selfTestIdentity), /must not echo verifier field replayReportPath/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    ...selfTestReceipt,
    traceSha256: '0'.repeat(64),
  }, selfTestIdentity), /exact raw trace file bytes/);
  } finally {
    rmSync(fixture.directory, { recursive: true, force: true });
  }
});

test('native BuilderJourney receipts reject replay drift and failed or missing mutation controls', () => {
  const fixture = nativeTraceReceiptFixture();
  try {
  const identity = {
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
    actionLabels: ['Preview'],
    tracePath: fixture.tracePath,
  };
  const replayReceipt = {
    ...identity,
    traceSha256: fixture.traceSha256,
    replayReportPath: '/evidence/cda-tla.json',
    selfTestReportPath: null,
    status: 'pending-self-test',
    replayStatus: 'pass',
    selfTestStatus: 'not-run',
    requestCoverage: { observed: 1, modeled: 1, unmapped: 0, duplicates: 0 },
    requiredActionCountsByTrace: { mismatches: {} },
    requiredActionMinimumsByTrace: { mismatches: {} },
    unmodeledEvents: { total: 0, unclassified: { total: 0 } },
  };
  assert.throws(() => assertNativeBuilderJourneyReplayReceipt({ ...replayReceipt, status: 'pass' }, identity),
    /must await the mutation self-test/);
  assert.throws(() => assertNativeBuilderJourneyReplayReceipt({
    ...replayReceipt,
    traceSha256: '0'.repeat(64),
  }, identity), /exact raw trace file bytes/);
  const traceBytes = readFileSync(fixture.tracePath);
  try {
    writeFileSync(fixture.tracePath, Buffer.concat([traceBytes, Buffer.from('\n')]));
    assert.throws(() => assertNativeBuilderJourneyReplayReceipt(replayReceipt, identity),
      /exact raw trace file bytes/);
  } finally {
    writeFileSync(fixture.tracePath, traceBytes);
  }
  assert.throws(() => assertNativeBuilderJourneyReplayReceipt({
    ...replayReceipt,
    requestCoverage: { ...replayReceipt.requestCoverage, unmapped: 1 },
  }, identity), /no unmapped requests/);
  assert.throws(() => assertNativeBuilderJourneyReplayReceipt({ ...replayReceipt, caseKey: identity.traceName }, identity),
    /must not include a legacy caseKey alias/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    traceName: identity.traceName,
    nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath,
    traceSha256: fixture.traceSha256,
    status: 'fail',
    selfTestStatus: 'fail',
    selfTestReportPath: fixture.selfTestReportPath,
    mutationControls: [{ name: 'reject-owner-mismatch', status: 'fail' }],
  }, { traceName: identity.traceName, nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath, traceSha256: fixture.traceSha256,
    selfTestReportPath: fixture.selfTestReportPath,
    contract: fixture.contract, traceDocument: fixture.traceDocument }), /status must be pass/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    traceName: identity.traceName,
    nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath,
    traceSha256: fixture.traceSha256,
    status: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath: fixture.selfTestReportPath,
    mutationControls: [{ name: 'reject-owner-mismatch', status: 'fail' }],
  }, { traceName: identity.traceName, nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath, traceSha256: fixture.traceSha256,
    selfTestReportPath: fixture.selfTestReportPath,
    contract: fixture.contract, traceDocument: fixture.traceDocument }),
  /exactly match the contract/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    traceName: identity.traceName,
    nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath,
    traceSha256: fixture.traceSha256,
    status: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath: fixture.selfTestReportPath,
    mutationControls: [],
  }, { traceName: identity.traceName, nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath, traceSha256: fixture.traceSha256,
    selfTestReportPath: fixture.selfTestReportPath,
    contract: fixture.contract, traceDocument: fixture.traceDocument }),
  /must contain mutation control results/);
  const controlOptions = { contract: fixture.contract, traceDocument: fixture.traceDocument };
  assert.deepEqual(assertNativeBuilderJourneyMutationControls(fixture.mutationControls, controlOptions),
    fixture.mutationControls);
  const failedControl = fixture.mutationControls.map((control, index) =>
    index === 0 ? { ...control, status: 'fail' } : control);
  assert.throws(() => assertNativeBuilderJourneyMutationControls(failedControl, controlOptions),
    /must pass/);
  assert.throws(() => assertNativeBuilderJourneyMutationControls(fixture.mutationControls.slice(1), controlOptions),
    /exactly match/);
  assert.throws(() => assertNativeBuilderJourneyMutationControls([
    ...fixture.mutationControls.slice(0, -1),
    { name: 'request-owner-mount-epoch-99', status: 'pass' },
  ], controlOptions), /exactly match/);
  assert.throws(() => assertNativeBuilderJourneyMutationControls([
    ...fixture.mutationControls,
    fixture.mutationControls[0],
  ], controlOptions), /must not repeat mutation-control names/);
  } finally {
    rmSync(fixture.directory, { recursive: true, force: true });
  }
});

test('Builder read metadata stays bound to the exact response object across overlapping reads', () => {
  const reads = createBuilderReadMetadataRegistry();
  const beforeBuilder = { draftVersion: 2, draftDigest: 'before-digest' };
  const afterBuilder = { draftVersion: 3, draftDigest: 'after-digest' };
  const beforeMetadata = { path: '/builder', requestId: 'builder-before', status: 200,
    draftVersion: 2, draftDigest: 'before-digest' };
  const afterMetadata = { path: '/builder', requestId: 'builder-after', status: 200,
    draftVersion: 3, draftDigest: 'after-digest' };

  reads.record(beforeBuilder, beforeMetadata);
  reads.record(afterBuilder, afterMetadata);

  assert.deepEqual(reads.forBuilder(beforeBuilder), beforeMetadata);
  assert.deepEqual(reads.forBuilder(afterBuilder), afterMetadata);
  assert.throws(() => reads.forBuilder({ draftVersion: 3 }), /must retain metadata/);
});

test('native request ledger preserves every observer request identity, raw path, flag, and chronology', () => {
  const observed = [
    { index: 0, browserRequestId: 'browser-0', requestId: 'wire-0', method: 'GET',
      path: '/api/v1/projects/p1/explorers/e1/authoring/v2/capability', startedAt: 100, isMutation: false },
    { index: 4, browserRequestId: 'browser-4', requestId: undefined, method: 'POST',
      path: '/api/v1/projects/p1/explorers/e1/authoring/v2/unclassified-command', startedAt: 160, isMutation: false },
  ];

  assert.deepEqual(nativeRequestLedgerFrom(observed), [
    { index: 0, browserRequestId: 'browser-0', requestId: 'wire-0', method: 'GET',
      path: '/api/v1/projects/p1/explorers/e1/authoring/v2/capability', startedAt: 100, isMutation: false },
    { index: 4, browserRequestId: 'browser-4', requestId: null, method: 'POST',
      path: '/api/v1/projects/p1/explorers/e1/authoring/v2/unclassified-command', startedAt: 160, isMutation: false },
  ]);
});
