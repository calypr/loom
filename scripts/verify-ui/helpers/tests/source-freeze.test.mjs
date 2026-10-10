import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, mkdir, rm, utimes, writeFile, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';
import { captureSourceFreeze, SourceFreezeError } from '../source-freeze.mjs';

const execFileAsync = promisify(execFile);
const initRepo = async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'source-freeze-test-'));
  await execFileAsync('git', ['init', '--quiet'], { cwd: root });
  await execFileAsync('git', ['config', 'user.name', 'Source Freeze Test'], { cwd: root });
  await execFileAsync('git', ['config', 'user.email', 'source-freeze@example.invalid'], { cwd: root });
  await writeFile(path.join(root, '.gitignore'), 'node_modules/\ndatasets/\nevidence/\n.gitnexus/\n');
  await mkdir(path.join(root, 'internal'), { recursive: true });
  await mkdir(path.join(root, 'ui/src'), { recursive: true });
  await mkdir(path.join(root, 'ui/packages/demo/src'), { recursive: true });
  await writeFile(path.join(root, 'internal/a.go'), 'package a\n');
  await writeFile(path.join(root, 'ui/src/App.tsx'), 'export const App = 1;\n');
  await writeFile(path.join(root, 'ui/packages/demo/src/index.ts'), 'export const value = 1;\n');
  await execFileAsync('git', ['add', '.gitignore', 'internal', 'ui'], { cwd: root });
  await execFileAsync('git', ['commit', '--quiet', '-m', 'fixture'], { cwd: root });
  return root;
};

const expectInvalidation = async (root, change, expected) => {
  const freeze = await captureSourceFreeze(root);
  await change();
  await assert.rejects(freeze.assertUnchanged(), (error) => {
    assert(error instanceof SourceFreezeError);
    assert.equal(error.invalidatesRun, true);
    assert.equal(error.productFailure, false);
    assert.deepEqual(error.changedPaths, [expected]);
    return true;
  });
};

test('content freeze ignores timestamps and unrelated or ignored paths', async (t) => {
  const root = await initRepo();
  t.after(() => rm(root, { recursive: true, force: true }));
  const freeze = await captureSourceFreeze(root);
  const file = path.join(root, 'internal/a.go');
  const future = new Date(Date.now() + 60_000);
  await utimes(file, future, future);
  await writeFile(path.join(root, 'README.md'), 'outside watched roots');
  await mkdir(path.join(root, 'internal/node_modules/pkg'), { recursive: true });
  await writeFile(path.join(root, 'internal/node_modules/pkg/index.js'), 'ignored');
  await mkdir(path.join(root, 'internal/datasets'), { recursive: true });
  await writeFile(path.join(root, 'internal/datasets/local.json'), 'ignored');
  await mkdir(path.join(root, 'internal/evidence'), { recursive: true });
  await writeFile(path.join(root, 'internal/evidence/run.json'), 'ignored');
  const result = await freeze.assertUnchanged();
  assert.equal(result.unchanged, true);
  assert.equal(result.invalidatesRun, false);
  assert.equal(result.productFailure, false);
});

test('content edits invalidate the run and report only the changed path', async (t) => {
  const root = await initRepo();
  t.after(() => rm(root, { recursive: true, force: true }));
  await expectInvalidation(root, () => writeFile(path.join(root, 'internal/a.go'), 'package a\n// changed\n'), {
    path: 'internal/a.go', change: 'modified',
  });
});

test('untracked source additions invalidate the run', async (t) => {
  const root = await initRepo();
  t.after(() => rm(root, { recursive: true, force: true }));
  await expectInvalidation(root, () => writeFile(path.join(root, 'internal/new.go'), 'package added\n'), {
    path: 'internal/new.go', change: 'added',
  });
});

test('tracked source removals invalidate the run', async (t) => {
  const root = await initRepo();
  t.after(() => rm(root, { recursive: true, force: true }));
  await expectInvalidation(root, () => unlink(path.join(root, 'ui/src/App.tsx')), {
    path: 'ui/src/App.tsx', change: 'removed',
  });
});
