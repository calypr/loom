import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  builderJourneyTraceReceiptIdentity,
  captureBuilderJourneyTraceProvenance,
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
