import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { sourceFingerprint } from '../source-fingerprint.mjs';

test('fingerprint changes when watched source changes', () => {
  const root = mkdtempSync(join(tmpdir(), 'loom-source-fingerprint-'));
  try {
    const path = join(root, 'go.mod');
    writeFileSync(path, 'module example.com/loom\n');
    const before = sourceFingerprint(root);
    writeFileSync(path, 'module example.com/changed\n');
    const after = sourceFingerprint(root);
    assert.equal(before.files, 1);
    assert.notEqual(before.sha256, after.sha256);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
