import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { sourceFingerprint, sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from '../source-fingerprint.mjs';

test('fingerprint changes when watched source changes', () => {
  const root = mkdtempSync(join(tmpdir(), 'loom-source-fingerprint-'));
  try {
    const path = join(root, 'go.mod');
    writeFileSync(path, 'module example.com/loom\n');
    const before = sourceFingerprintWithManifest(root);
    writeFileSync(path, 'module example.com/changed\n');
    const after = sourceFingerprintWithManifest(root);
    assert.equal(before.fingerprint.files, 1);
    assert.equal(after.fingerprint.files, before.fingerprint.files, 'content-only edits keep the watched file count stable');
    assert.notEqual(before.fingerprint.sha256, after.fingerprint.sha256);
    assert.deepEqual(sourceFingerprintChangedPaths(before.manifest, after.manifest), [
      { path: 'go.mod', change: 'modified' },
    ]);
    assert.deepEqual(sourceFingerprint(root), after.fingerprint, 'the legacy aggregate fingerprint shape remains unchanged');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
