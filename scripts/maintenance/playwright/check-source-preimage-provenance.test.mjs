import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { sourcePreimageLedgerIssue } from './source-preimage-provenance.mjs';

const sourcePath = 'scripts/verify-ui/workflows/verify-cda-zero-column-related-medication.mjs';
const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');

function git(root, ...args) {
  return execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trim();
}

function createRepository({ sourceAtParent = false } = {}) {
  const root = mkdtempSync(join(tmpdir(), 'source-preimage-'));
  git(root, 'init', '--quiet');
  git(root, 'config', 'user.name', 'Source preimage test');
  git(root, 'config', 'user.email', 'source-preimage@example.test');
  writeFileSync(join(root, 'README.md'), 'parent\n');
  if (sourceAtParent) {
    const path = join(root, sourcePath);
    mkdirSync(join(path, '..'), { recursive: true });
    writeFileSync(path, 'old source\n');
  }
  git(root, 'add', '.');
  git(root, 'commit', '--quiet', '-m', 'parent');
  const parentCommit = git(root, 'rev-parse', 'HEAD');
  const path = join(root, sourcePath);
  mkdirSync(join(path, '..'), { recursive: true });
  const contents = sourceAtParent ? 'changed source\n' : 'new source\n';
  writeFileSync(path, contents);
  git(root, 'add', sourcePath);
  git(root, 'commit', '--quiet', '-m', 'add source');
  const introducedCommit = git(root, 'rev-parse', 'HEAD');
  const record = {
    kind: 'new-native-source',
    introducedCommit,
    parentCommit,
    introducedBlobSha256: createHash('sha256').update(contents).digest('hex'),
  };
  return { root, record };
}

function sourceRow(record) {
  return {
    sourcePath,
    preimageSha256: null,
    historicalPreimageStatus: 'new-native-source-unverified',
    historicalPreimageProvenance: record,
  };
}

test('accepts a new-native-source record only for a path added at the recorded commit', (t) => {
  const { root, record } = createRepository();
  t.after(() => rmSync(root, { recursive: true, force: true }));

  assert.equal(sourcePreimageLedgerIssue(sourcePath, sourceRow(record), record, root), null);
});

test('accepts the persisted zero-column workflow provenance against repository history', () => {
  const records = JSON.parse(readFileSync(join(repositoryRoot, 'docs/verification/playwright/source-preimages.json'), 'utf8'));
  const record = records[sourcePath];

  assert.equal(sourcePreimageLedgerIssue(sourcePath, sourceRow(record), record, repositoryRoot), null);
});

test('rejects a new-native-source record when the path existed at its recorded parent', (t) => {
  const { root, record } = createRepository({ sourceAtParent: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));

  assert.match(
    sourcePreimageLedgerIssue(sourcePath, sourceRow(record), record, root),
    /path existed at the recorded parent/,
  );
});

test('rejects a modified persistent record and leaves unknown legacy preimages fatal', (t) => {
  const { root, record } = createRepository();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const invalidRecord = { ...record, introducedBlobSha256: '0'.repeat(64) };

  assert.match(
    sourcePreimageLedgerIssue(sourcePath, sourceRow(invalidRecord), invalidRecord, root),
    /introduced blob SHA-256 does not match/,
  );
  assert.match(
    sourcePreimageLedgerIssue(sourcePath, sourceRow(record), invalidRecord, root),
    /does not match the persistent record/,
  );
  assert.match(
    sourcePreimageLedgerIssue('scripts/verify-legacy.mjs', {
      sourcePath: 'scripts/verify-legacy.mjs',
      preimageSha256: null,
      historicalPreimageStatus: 'unknown',
    }, undefined, root),
    /source preimage is not bound to the persistent historical hash ledger/,
  );
});

test('preserves string hash-ledger behavior', (t) => {
  const { root } = createRepository();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const hash = 'a'.repeat(64);

  assert.equal(sourcePreimageLedgerIssue(sourcePath, { sourcePath, preimageSha256: hash }, hash, root), null);
  assert.match(
    sourcePreimageLedgerIssue(sourcePath, { sourcePath, preimageSha256: 'b'.repeat(64) }, hash, root),
    /source preimage is not bound to the persistent historical hash ledger/,
  );
});
