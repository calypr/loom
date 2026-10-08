import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const entrypoint = fileURLToPath(new URL('../run-assigned-check.mjs', import.meta.url));

function createRepositories(t) {
  const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), 'assigned-worktree-check-'));
  t.after(() => rmSync(temporaryRoot, { recursive: true, force: true }));
  const assignedRoot = path.join(temporaryRoot, 'assigned');
  const forbiddenRoot = path.join(temporaryRoot, 'live');
  mkdirSync(assignedRoot);
  mkdirSync(forbiddenRoot);
  const checkFile = path.join(assignedRoot, 'assigned-check.mjs');
  writeFileSync(checkFile, 'export const assignedCheck = true;\n');
  for (const root of [assignedRoot, forbiddenRoot]) {
    execFileSync('git', ['init', '--quiet'], { cwd: root });
  }
  return { assignedRoot, checkFile, forbiddenRoot, temporaryRoot };
}

function runCheck({ cwd, assignedRoot, forbiddenRoot, ...check }) {
  return spawnSync(process.execPath, [
    entrypoint,
    '--expected-root', assignedRoot,
    '--forbidden-root', forbiddenRoot,
    ...(check.command ? ['--', ...check.command] : []),
  ], { cwd, encoding: 'utf8' });
}

test('checks run from the assigned physical Git worktree', (t) => {
  const { assignedRoot, checkFile, forbiddenRoot } = createRepositories(t);
  const result = runCheck({
    cwd: assignedRoot,
    assignedRoot,
    forbiddenRoot,
    command: ['node', '--check', checkFile],
  });

  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /PASS assigned worktree/);
});

test('the checks entrypoint rejects the forbidden live checkout before running a check', (t) => {
  const { assignedRoot, checkFile, forbiddenRoot } = createRepositories(t);
  const result = runCheck({
    cwd: forbiddenRoot,
    assignedRoot,
    forbiddenRoot,
    command: ['node', '--check', checkFile],
  });

  assert.equal(result.status, 64);
  assert.match(result.stderr, /forbidden live checkout/);
});

test('the checks entrypoint rejects a symlink alias to the forbidden live checkout', (t) => {
  const { assignedRoot, checkFile, forbiddenRoot, temporaryRoot } = createRepositories(t);
  const alias = path.join(temporaryRoot, 'live-alias');
  symlinkSync(forbiddenRoot, alias, 'dir');
  const result = runCheck({
    cwd: alias,
    assignedRoot,
    forbiddenRoot,
    command: ['node', '--check', checkFile],
  });

  assert.equal(result.status, 64);
  assert.match(result.stderr, /forbidden live checkout/);
});

test('the checks entrypoint refuses commands outside its check allowlist', (t) => {
  const { assignedRoot, forbiddenRoot } = createRepositories(t);
  const result = runCheck({
    cwd: assignedRoot,
    assignedRoot,
    forbiddenRoot,
    command: ['node', '--eval', 'process.exit(0)'],
  });

  assert.equal(result.status, 64);
  assert.match(result.stderr, /only node --check, node --test, and git diff --check/);
});
