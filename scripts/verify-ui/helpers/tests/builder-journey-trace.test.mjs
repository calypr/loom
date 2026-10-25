import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  assertNativeBuilderJourneyReplayReceipt,
  assertNativeBuilderJourneyMutationControls,
  assertNativeBuilderJourneySelfTestReceipt,
  builderJourneyReportPaths,
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
  assert.deepEqual(builderJourneyTraceReceiptIdentity({
    kind: 'native-lifecycle',
    captureKind: 'builder-cda-native-lifecycle',
    captureAdapter: 'builder-native-journey-v1',
    name: 'cda-builder-group-related-source',
    model: 'BuilderJourney',
  }, 'cda-builder-group-related-source', 'standalone-reshape-group-edit-before-related-column',
  'group-edit-before-related-column'), {
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
  });
});

test('native replay outputs are unique trace siblings, reject existing files, and leave legacy evidence paths alone', (t) => {
  const repositoryRoot = '/repo';
  const evidenceDirectory = '/tmp/run-evidence';
  const artifactStem = 'cda-builder-group-related-source';
  const directory = mkdtempSync(join(tmpdir(), 'builder-journey-report-paths-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const firstTracePath = join(directory, 'run-a-trace.json');
  const firstRunPaths = builderJourneyReportPaths({
    captureKind: 'builder-cda-native-lifecycle',
    tracePath: firstTracePath,
    nativeReportPath: join(directory, 'run-a-report.json'),
    retainedNativeReportPath: join(directory, 'run-a-native-report.json'),
    evidenceDirectory,
    artifactStem,
    repositoryRoot,
  });
  assert.deepEqual(firstRunPaths, {
    replayReportPath: join(directory, 'run-a-trace-tla.json'),
    selfTestReportPath: join(directory, 'run-a-trace-self-test.json'),
    outerTlaReportPath: join(directory, 'run-a-report.tla.json'),
  });
  const secondRunPaths = builderJourneyReportPaths({
    captureKind: 'builder-cda-native-lifecycle',
    tracePath: join(directory, 'run-b-trace.json'),
    evidenceDirectory,
    artifactStem,
    repositoryRoot,
  });
  assert.notEqual(firstRunPaths.replayReportPath, secondRunPaths.replayReportPath);
  assert.notEqual(firstRunPaths.selfTestReportPath, secondRunPaths.selfTestReportPath);
  assert.throws(() => builderJourneyReportPaths({
    captureKind: 'builder-cda-native-lifecycle',
    tracePath: firstTracePath,
    nativeReportPath: firstRunPaths.replayReportPath,
    retainedNativeReportPath: join(directory, 'collision-native-report.json'),
    evidenceDirectory,
    artifactStem,
    repositoryRoot,
  }), /output paths must be unique/);
  assert.throws(() => builderJourneyReportPaths({
    captureKind: 'builder-cda-native-lifecycle',
    tracePath: join(directory, 'invalid-trace.json'),
    nativeReportPath: join(directory, 'invalid-report.txt'),
    retainedNativeReportPath: join(directory, 'invalid-native-report.json'),
    evidenceDirectory,
    artifactStem,
    repositoryRoot,
  }), /outer report path must be a nonempty JSON path/);
  writeFileSync(firstRunPaths.replayReportPath, '{}\n');
  assert.throws(() => builderJourneyReportPaths({
    captureKind: 'builder-cda-native-lifecycle',
    tracePath: firstTracePath,
    evidenceDirectory,
    artifactStem,
    repositoryRoot,
  }), /must be fresh/);

  const legacyTracePath = '/repo/.artifacts/builder-journey/cda-builder-group-related-source-trace.json';
  assert.deepEqual(builderJourneyReportPaths({
    captureKind: 'builder-group-legacy',
    tracePath: legacyTracePath,
    evidenceDirectory,
    artifactStem,
    repositoryRoot,
  }), {
    replayReportPath: join(evidenceDirectory, `${artifactStem}-tla.json`),
    selfTestReportPath: resolve(repositoryRoot, evidenceDirectory, `${artifactStem}-self-test.json`),
  });
});

test('native replay output planning creates a missing trace parent before canonical path validation', (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'builder-journey-missing-output-parent-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const traceDirectory = join(directory, 'nested', 'run');
  const tracePath = join(traceDirectory, 'fresh-trace.json');

  assert.equal(existsSync(traceDirectory), false);
  const paths = builderJourneyReportPaths({
    captureKind: 'builder-cda-native-lifecycle',
    tracePath,
  });

  assert.equal(existsSync(traceDirectory), true);
  assert.deepEqual(paths, {
    replayReportPath: join(traceDirectory, 'fresh-trace-tla.json'),
    selfTestReportPath: join(traceDirectory, 'fresh-trace-self-test.json'),
  });
  assert.equal(existsSync(tracePath), false);
  assert.equal(existsSync(paths.replayReportPath), false);
  assert.equal(existsSync(paths.selfTestReportPath), false);
});

test('native replay and mutation receipts preserve verifier coverage and require every mutation control to pass', () => {
  const fixture = nativeTraceReceiptFixture();
  try {
  const identity = {
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
    actionLabels: ['Preview', 'RelatedSourcePreview', 'Reload'],
    tracePath: fixture.tracePath,
    requestCoverageFields: ['modeledActions', 'reconcileReads', 'supportedReadOnly', 'setupMutations'],
  };
  const replayReceipt = {
    ...identity,
    traceSha256: fixture.traceSha256,
    replayReportPath: '/evidence/cda-tla.json',
    selfTestReportPath: null,
    status: 'pending-self-test',
    replayStatus: 'pass',
    selfTestStatus: 'not-run',
    requestCoverage: {
      observed: 11, modeled: 11, unmapped: 0, duplicates: 0,
      modeledActions: 4, reconcileReads: 3, supportedReadOnly: 3, setupMutations: 1,
    },
    requestCoverageBasis: 'unit fixture separates modeled requests, reconciliation reads, supported reads, and setup mutations',
    requiredActionCountsByTrace: { mismatches: {} },
    requiredActionMinimumsByTrace: { mismatches: {} },
    unmodeledEvents: { total: 3, unclassified: { total: 0 } },
  };
  assert.strictEqual(assertNativeBuilderJourneyReplayReceipt(replayReceipt, identity), replayReceipt);
  assert.throws(() => assertNativeBuilderJourneyReplayReceipt({
    ...replayReceipt,
    requestCoverage: { ...replayReceipt.requestCoverage, setupMutations: undefined },
  }, identity), /setupMutations/u);
  const temporalReceipt = {
    ...replayReceipt,
    requestCoverage: {
      observed: 10, modeled: 10, unmapped: 0, duplicates: 0,
      modeledActions: 4, reconcileReads: 3, supportedReadOnly: 3,
    },
  };
  const temporalIdentity = { ...identity };
  delete temporalIdentity.requestCoverageFields;
  assert.strictEqual(assertNativeBuilderJourneyReplayReceipt(temporalReceipt, temporalIdentity), temporalReceipt,
    'Three-class native coverage remains valid for adapters without setup mutations.');
  const selfTestReportPath = fixture.selfTestReportPath;
  const selfTestReceipt = {
    traceName: identity.traceName,
    nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath,
    traceSha256: fixture.traceSha256,
    status: 'pass',
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
  assert.equal(Object.hasOwn(selfTestReceipt, 'selfTestStatus'), false);
  assert.equal(Object.hasOwn(selfTestReceipt, 'replayStatus'), false);
  assert.equal(Object.hasOwn(selfTestReceipt, 'replayReportPath'), false);
  assert.equal(Object.hasOwn(selfTestReceipt, 'requestCoverage'), false);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    ...selfTestReceipt,
    replayReportPath: replayReceipt.replayReportPath,
  }, selfTestIdentity), /must not echo verifier field replayReportPath/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    ...selfTestReceipt,
    selfTestReportPath: join(fixture.directory, 'other-self-test.json'),
  }, selfTestIdentity), /actual output path/);
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
    selfTestReportPath: fixture.selfTestReportPath,
    mutationControls: [],
  }, { traceName: identity.traceName, nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath, traceSha256: fixture.traceSha256,
    selfTestReportPath: fixture.selfTestReportPath,
    contract: fixture.contract, traceDocument: fixture.traceDocument }),
  /must contain mutation control results/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    traceName: identity.traceName,
    nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath,
    traceSha256: fixture.traceSha256,
    status: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath: fixture.selfTestReportPath,
    mutationControls: fixture.mutationControls,
  }, { traceName: identity.traceName, nativeCaseKey: identity.nativeCaseKey,
    tracePath: fixture.tracePath, traceSha256: fixture.traceSha256,
    selfTestReportPath: fixture.selfTestReportPath,
    contract: fixture.contract, traceDocument: fixture.traceDocument }),
  /one authoritative status field/);
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
