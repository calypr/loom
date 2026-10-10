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
  const wrongRoot = path.join(temporaryRoot, 'wrong');
  mkdirSync(assignedRoot);
  mkdirSync(forbiddenRoot);
  mkdirSync(wrongRoot);
  const checkFile = path.join(assignedRoot, 'assigned-check.mjs');
  writeFileSync(checkFile, 'export const assignedCheck = true;\n');
  for (const root of [assignedRoot, forbiddenRoot, wrongRoot]) {
    execFileSync('git', ['init', '--quiet'], { cwd: root });
  }
  return { assignedRoot, checkFile, forbiddenRoot, temporaryRoot, wrongRoot };
}

function writeRunnerStub(assignedRoot) {
  const entrypoint = path.join(assignedRoot, 'scripts/run-native-verification-bracket.mjs');
  mkdirSync(path.dirname(entrypoint), { recursive: true });
  writeFileSync(entrypoint, 'console.log(JSON.stringify(process.argv.slice(2)));\n');
  return entrypoint;
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

test('the checks entrypoint rejects a different physical Git root', (t) => {
  const { assignedRoot, forbiddenRoot, wrongRoot } = createRepositories(t);
  const result = runCheck({ cwd: wrongRoot, assignedRoot, forbiddenRoot });

  assert.equal(result.status, 64);
  assert.match(result.stderr, /expected physical cwd and Git top-level/);
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
  assert.match(result.stderr, /the registered checks-only entrypoint may run through this entrypoint/);
});

test('the checks entrypoint runs registered checks-only mode from the assigned worktree', (t) => {
  const { assignedRoot, forbiddenRoot } = createRepositories(t);
  writeRunnerStub(assignedRoot);
  const result = runCheck({
    cwd: assignedRoot,
    assignedRoot,
    forbiddenRoot,
    command: [
      'node', 'scripts/run-native-verification-bracket.mjs',
      '--checks-only', '--scenario', 'cda-current-draft-membership', '--case', 'membership',
    ],
  });

  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /PASS assigned worktree/);
  assert.match(result.stdout, /\["--scenario","cda-current-draft-membership","--case","membership","--checks-only"\]/);
});

test('the checks entrypoint rejects full browser mode', (t) => {
  const { assignedRoot, forbiddenRoot } = createRepositories(t);
  writeRunnerStub(assignedRoot);
  const result = runCheck({
    cwd: assignedRoot,
    assignedRoot,
    forbiddenRoot,
    command: [
      'node', 'scripts/run-native-verification-bracket.mjs',
      '--scenario', 'cda-current-draft-membership', '--case', 'membership', '--target', '/tmp/target.json',
    ],
  });

  assert.equal(result.status, 64);
  assert.match(result.stderr, /browser mode is not permitted through the assigned-check guard/);
});

test('the checks entrypoint rejects a symlinked registered entrypoint', (t) => {
  const { assignedRoot, forbiddenRoot, temporaryRoot } = createRepositories(t);
  const entrypoint = path.join(assignedRoot, 'scripts/run-native-verification-bracket.mjs');
  const escapedEntrypoint = path.join(temporaryRoot, 'run-native-verification-bracket.mjs');
  mkdirSync(path.dirname(entrypoint), { recursive: true });
  writeFileSync(escapedEntrypoint, 'console.log("escaped");\n');
  symlinkSync(escapedEntrypoint, entrypoint, 'file');
  const result = runCheck({
    cwd: assignedRoot,
    assignedRoot,
    forbiddenRoot,
    command: [
      'node', 'scripts/run-native-verification-bracket.mjs',
      '--checks-only', '--scenario', 'cda-current-draft-membership', '--case', 'membership',
    ],
  });

  assert.equal(result.status, 64);
  assert.match(result.stderr, /not a symlink/);
  assert.doesNotMatch(result.stdout, /escaped/);
});

test('the checks entrypoint rejects an escaped registered entrypoint path', (t) => {
  const { assignedRoot, forbiddenRoot, temporaryRoot } = createRepositories(t);
  const escapedEntrypoint = path.join(temporaryRoot, 'run-native-verification-bracket.mjs');
  writeFileSync(escapedEntrypoint, 'console.log("escaped");\n');
  const result = runCheck({
    cwd: assignedRoot,
    assignedRoot,
    forbiddenRoot,
    command: [
      'node', escapedEntrypoint,
      '--checks-only', '--scenario', 'cda-current-draft-membership', '--case', 'membership',
    ],
  });

  assert.equal(result.status, 64);
  assert.match(result.stderr, /must be the relative path scripts\/run-native-verification-bracket\.mjs/);
  assert.doesNotMatch(result.stdout, /escaped/);
});
