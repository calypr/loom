import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  assertNativeBuilderJourneyReplayReceipt,
  assertNativeBuilderJourneySelfTestReceipt,
  builderJourneyTraceReceiptIdentity,
  captureBuilderJourneyTraceProvenance,
  finalizeNativeBuilderJourneyReceipt,
} from '../builder-journey-trace.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..');

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
  const identity = {
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
    actionLabels: ['Preview', 'RelatedSourcePreview', 'Reload'],
  };
  const replayReceipt = {
    ...identity,
    tracePath: '/evidence/cda-trace.json',
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
  const selfTestReportPath = '/evidence/cda-self-test.json';
  const selfTestReceipt = {
    ...replayReceipt,
    status: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath,
    mutationControls: [{ name: 'reject-owner-mismatch', status: 'pass' },
      { name: 'reject-snapshot-mismatch', status: 'pass' }],
  };
  const validatedSelfTest = assertNativeBuilderJourneySelfTestReceipt(selfTestReceipt, replayReceipt, {
    ...identity,
    selfTestReportPath,
  });
  const finalReceipt = finalizeNativeBuilderJourneyReceipt(replayReceipt, validatedSelfTest, {
    ...identity,
    selfTestReportPath,
  });
  assert.deepEqual(finalReceipt.requestCoverage, replayReceipt.requestCoverage);
  assert.deepEqual(finalReceipt.unmodeledEvents, replayReceipt.unmodeledEvents);
  assert.deepEqual(finalReceipt.mutationControls, selfTestReceipt.mutationControls);
  assert.equal(finalReceipt.status, 'pass');
  assert.equal(Object.hasOwn(finalReceipt, 'caseKey'), false);
});

test('native BuilderJourney receipts reject replay drift and failed or missing mutation controls', () => {
  const identity = {
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
    actionLabels: ['Preview'],
  };
  const replayReceipt = {
    ...identity,
    tracePath: '/evidence/cda-trace.json',
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
    requestCoverage: { ...replayReceipt.requestCoverage, unmapped: 1 },
  }, identity), /no unmapped requests/);
  assert.throws(() => assertNativeBuilderJourneyReplayReceipt({ ...replayReceipt, caseKey: identity.traceName }, identity),
    /must not include a legacy caseKey alias/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    ...replayReceipt,
    status: 'fail',
    selfTestStatus: 'fail',
    selfTestReportPath: '/evidence/cda-self-test.json',
    mutationControls: [{ name: 'reject-owner-mismatch', status: 'fail' }],
  }, replayReceipt, { ...identity, selfTestReportPath: '/evidence/cda-self-test.json' }), /status must be pass/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    ...replayReceipt,
    status: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath: '/evidence/cda-self-test.json',
    mutationControls: [{ name: 'reject-owner-mismatch', status: 'fail' }],
  }, replayReceipt, { ...identity, selfTestReportPath: '/evidence/cda-self-test.json' }),
  /must have a name and pass status/);
  assert.throws(() => assertNativeBuilderJourneySelfTestReceipt({
    ...replayReceipt,
    status: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath: '/evidence/cda-self-test.json',
    mutationControls: [],
  }, replayReceipt, { ...identity, selfTestReportPath: '/evidence/cda-self-test.json' }),
  /must include mutation control results/);
});
