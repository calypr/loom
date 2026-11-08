import assert from 'node:assert/strict';
import { copyFileSync, mkdtempSync, mkdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { NodeTestInventoryError, nodeTestInventory } from './list-node-tests.mjs';
import { verificationInventory } from '../lib/verification-inventory.mjs';

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const selector = path.join(repositoryRoot, 'scripts/ci/list-node-tests.mjs');

function project(files) {
  const root = mkdtempSync(path.join(tmpdir(), 'loom-node-test-failure-'));
  for (const [file, source] of Object.entries(files)) {
    const output = path.join(root, file);
    mkdirSync(path.dirname(output), { recursive: true });
    writeFileSync(output, source);
  }
  mkdirSync(path.join(root, '.github/workflows'), { recursive: true });
  for (const file of ['tests.yaml', 'builder-formal.yml'])
    copyFileSync(path.join(repositoryRoot, '.github/workflows', file), path.join(root, '.github/workflows', file));
  copyFileSync(path.join(repositoryRoot, 'Makefile'), path.join(root, 'Makefile'));
  return root;
}

function runSelector(root, args) {
  const run = spawnSync(process.execPath, [selector, ...args], { cwd: root, encoding: 'utf8', timeout: 20000 });
  assert.equal(run.error, undefined, String(run.error));
  return run;
}

const HEALTHY = { 'scripts/healthy.test.mjs': "import test from 'node:test'; test('healthy', () => {});" };

function assertBroken(run, rootFile, dependency) {
  assert.notEqual(run.status, 0, `selector must fail, stdout: ${run.stdout}`);
  assert.equal(run.stdout, '', 'a broken closure must never emit a reduced selection');
  assert.match(run.stderr, new RegExp(rootFile.replaceAll('.', '\\.')));
  assert.match(run.stderr, new RegExp(dependency.replaceAll('.', '\\.')));
  assert.match(run.stderr, /chain=/);
}

test('CLI fails on a missing direct import instead of listing only the healthy test', () => {
  const root = project({ 'scripts/root.test.mjs': "import './missing.mjs';", ...HEALTHY });
  try {
    assertBroken(runSelector(root, []), 'root.test.mjs', 'missing.mjs');
    assertBroken(runSelector(root, ['--explain']), 'root.test.mjs', 'missing.mjs');
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('CLI fails on a missing transitive import reachable only through a cycle', () => {
  const root = project({
    'scripts/root.test.mjs': "import './a.mjs';",
    'scripts/a.mjs': "import './b.mjs';",
    'scripts/b.mjs': "import './a.mjs'; import './missing.mjs';",
    ...HEALTHY,
  });
  try {
    const run = runSelector(root, ['--explain']);
    assertBroken(run, 'root.test.mjs', 'missing.mjs');
    assert.match(run.stderr, /-> .*a\.mjs -> .*b\.mjs -> .*missing\.mjs/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('CLI fails on a missing test-owned URL fixture with its filesystem code', () => {
  const root = project({ 'scripts/root.test.mjs': "readFile(new URL('./missing.json', import.meta.url));", ...HEALTHY });
  try {
    const run = runSelector(root, []);
    assertBroken(run, 'root.test.mjs', 'missing.json');
    assert.match(run.stderr, /reason=missing-source-fixture/);
    assert.match(run.stderr, /code=ENOENT/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('an unreadable test-owned URL fixture fails the inventory with its filesystem code', async () => {
  // A symlink loop is unopenable for every user, unlike a mode only root can read.
  const root = project({ 'scripts/root.test.mjs': "readFile(new URL('./blocked.json', import.meta.url));", ...HEALTHY });
  try {
    symlinkSync(path.join(root, 'scripts/blocked.json'), path.join(root, 'scripts/blocked.json'));
    await assert.rejects(() => nodeTestInventory(root), error => {
      assert.ok(error instanceof NodeTestInventoryError);
      const [problem] = error.problems;
      assert.equal(problem.file, 'scripts/root.test.mjs');
      assert.equal(problem.dependency, 'scripts/blocked.json');
      assert.equal(problem.reason, 'missing-source-fixture');
      assert.equal(problem.code, 'ELOOP');
      assert.deepEqual(problem.chain, ['scripts/root.test.mjs', 'scripts/blocked.json']);
      return true;
    });
    assertBroken(runSelector(root, ['--explain']), 'root.test.mjs', 'blocked.json');
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('an unreadable retained dependency fails the inventory with its filesystem code', async () => {
  // A directory where a module is expected yields EISDIR for any user, unlike
  // a mode that only the current user cannot read.
  const root = project({ 'scripts/root.test.mjs': "import './blocked.mjs';", ...HEALTHY });
  try {
    mkdirSync(path.join(root, 'scripts/blocked.mjs'), { recursive: true });
    await assert.rejects(() => nodeTestInventory(root), error => {
      assert.ok(error instanceof NodeTestInventoryError);
      const [problem] = error.problems;
      assert.equal(problem.file, 'scripts/root.test.mjs');
      assert.equal(problem.dependency, 'scripts/blocked.mjs');
      assert.equal(problem.reason, 'missing-or-unreadable-import');
      assert.equal(problem.code, 'EISDIR');
      assert.deepEqual(problem.chain, ['scripts/root.test.mjs', 'scripts/blocked.mjs']);
      return true;
    });
    assertBroken(runSelector(root, []), 'root.test.mjs', 'blocked.mjs');
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('an intentional Playwright exclusion cannot mask a missing dependency in the same root', () => {
  const root = project({ 'scripts/root.test.mjs': "import './missing.mjs'; import 'playwright';", ...HEALTHY });
  try {
    assertBroken(runSelector(root, ['--explain']), 'root.test.mjs', 'missing.mjs');
    assert.match(runSelector(root, []).stderr, /reason=missing-or-unreadable-import/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('a missing dependency in a shared helper fails every root and one healthy test cannot hide it', async () => {
  const files = {
    'scripts/alpha.test.mjs': "import './shared.mjs';",
    'scripts/beta.test.mjs': "import './shared.mjs';",
    'scripts/shared.mjs': "import './missing.mjs';",
    ...HEALTHY,
  };
  const root = project(files);
  try {
    assertBroken(runSelector(root, []), 'alpha.test.mjs', 'missing.mjs');
    const run = runSelector(root, ['--explain']);
    assert.match(run.stderr, /beta\.test\.mjs/);
    await assert.rejects(() => verificationInventory(root), error => {
      assert.ok(error instanceof NodeTestInventoryError);
      assert.deepEqual(error.problems.map(problem => problem.file), ['scripts/alpha.test.mjs', 'scripts/beta.test.mjs']);
      return true;
    });
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('verification inventory fails nonzero for a broken closure and emits nothing for --out', () => {
  const root = project({ 'scripts/root.test.mjs': "import './missing.mjs';", ...HEALTHY });
  const out = path.join(root, 'inventory-out');
  try {
    const run = spawnSync(process.execPath, [path.join(repositoryRoot, 'scripts/verify-builder-formal-core.mjs'), '--phase', 'inventory', '--out', out, '--project-root', root],
      { encoding: 'utf8', timeout: 30000 });
    assert.equal(run.error, undefined, String(run.error));
    assert.notEqual(run.status, 0, `inventory phase must fail, stdout: ${run.stdout}`);
    assert.match(run.stderr, /root\.test\.mjs/);
    assert.match(run.stderr, /missing/);
    assert.equal(run.stdout, '', 'a broken closure must not emit an inventory document');
    assert.equal(run.status, 1, 'a broken closure must fail with status 1');
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('intentional exclusions and incidental browser words remain ordinary nonfatal results', async () => {
  const root = project({
    'scripts/browser.test.mjs': "const { chromium } = require('playwright');",
    'scripts/core.test.mjs': "await import('playwright-core');",
    'scripts/maintenance/playwright/retired.test.mjs': "import 'playwright';",
    'scripts/incidental.test.mjs': "const note = 'playwright-request-1'; const missing = 'no such file';",
    ...HEALTHY,
  });
  try {
    const list = runSelector(root, []);
    assert.equal(list.status, 0, list.stderr);
    assert.deepEqual(list.stdout.trim().split('\n').sort(),
      ['scripts/incidental.test.mjs', 'scripts/healthy.test.mjs'].sort());
    const explained = runSelector(root, ['--explain']);
    assert.equal(explained.status, 0, explained.stderr);
    const result = JSON.parse(explained.stdout);
    assert.deepEqual(result.excluded.map(row => [row.file, row.reason]).sort(), [
      ['scripts/browser.test.mjs', 'browser-package-import'],
      ['scripts/core.test.mjs', 'browser-package-import'],
      ['scripts/maintenance/playwright/retired.test.mjs', 'retired-browser-directory'],
    ].sort((a, b) => a[0].localeCompare(b[0])));
    await verificationInventory(root);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
