import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
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

test('fingerprint includes verifier source and fixture inputs but excludes installed dependencies', () => {
  const root = mkdtempSync(join(tmpdir(), 'loom-verifier-fingerprint-'));
  try {
    mkdirSync(join(root, 'scripts', 'node_modules'), { recursive: true });
    mkdirSync(join(root, 'testdata', 'devloop-fixture'), { recursive: true });
    mkdirSync(join(root, 'testdata', 'verify-combine'), { recursive: true });
    mkdirSync(join(root, 'testdata', 'verify-combine-nullable-duplicates'), { recursive: true });
    writeFileSync(join(root, 'scripts', 'verify.mjs'), 'original verifier');
    writeFileSync(join(root, 'scripts', 'node_modules', 'dependency.mjs'), 'original dependency');
    writeFileSync(join(root, 'testdata', 'devloop-fixture', 'Patient.ndjson'), '{"id":"one"}\n');
    writeFileSync(join(root, 'testdata', 'verify-combine', 'Patient.ndjson'), '{"id":"combine-one"}\n');
    writeFileSync(join(root, 'testdata', 'verify-combine-nullable-duplicates', 'Observation.ndjson'), '{"id":"nullable-one"}\n');
    const before = sourceFingerprintWithManifest(root);
    writeFileSync(join(root, 'scripts', 'verify.mjs'), 'changed verifier');
    const changedVerifier = sourceFingerprintWithManifest(root);
    assert.notEqual(changedVerifier.fingerprint.sha256, before.fingerprint.sha256);
    assert.deepEqual(sourceFingerprintChangedPaths(before.manifest, changedVerifier.manifest), [
      { path: 'scripts/verify.mjs', change: 'modified' },
    ]);
    writeFileSync(join(root, 'testdata', 'devloop-fixture', 'Patient.ndjson'), '{"id":"two"}\n');
    const changedFixture = sourceFingerprintWithManifest(root);
    assert.notEqual(changedFixture.fingerprint.sha256, changedVerifier.fingerprint.sha256);
    writeFileSync(join(root, 'testdata', 'verify-combine', 'Patient.ndjson'), '{"id":"combine-two"}\n');
    const changedCombineFixture = sourceFingerprintWithManifest(root);
    assert.notEqual(changedCombineFixture.fingerprint.sha256, changedFixture.fingerprint.sha256);
    assert.deepEqual(sourceFingerprintChangedPaths(changedFixture.manifest, changedCombineFixture.manifest), [
      { path: 'testdata/verify-combine/Patient.ndjson', change: 'modified' },
    ]);
    writeFileSync(join(root, 'testdata', 'verify-combine-nullable-duplicates', 'Observation.ndjson'), '{"id":"nullable-two"}\n');
    const changedNullableFixture = sourceFingerprintWithManifest(root);
    assert.deepEqual(sourceFingerprintChangedPaths(changedCombineFixture.manifest, changedNullableFixture.manifest), [
      { path: 'testdata/verify-combine-nullable-duplicates/Observation.ndjson', change: 'modified' },
    ]);
    writeFileSync(join(root, 'scripts', 'node_modules', 'dependency.mjs'), 'changed dependency');
    assert.deepEqual(sourceFingerprintWithManifest(root).fingerprint, changedNullableFixture.fingerprint);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
