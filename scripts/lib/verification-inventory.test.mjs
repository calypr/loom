import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { spawnSync } from 'node:child_process';
import { nodeTestInventory } from '../ci/list-node-tests.mjs';
import { verificationInventory } from './verification-inventory.mjs';

function fixture(files) {
  const directory = mkdtempSync(path.join(tmpdir(), 'loom-test-inventory-'));
  for (const [file, source] of Object.entries(files)) {
    const output = path.join(directory, file);
    mkdirSync(path.dirname(output), { recursive: true });
    writeFileSync(output, source);
  }
  return directory;
}

test('native test routing retains incidental browser words and reports executable dependencies', async () => {
  const directory = fixture({
    'scripts/native.test.mjs': "const requestId = 'playwright-request-1';",
    'scripts/required.test.mjs': "const { chromium } = require('playwright');",
    'scripts/dynamic.test.mjs': "await import('playwright-core');",
    'scripts/missing.test.mjs': "import './gone.mjs';",
    'scripts/fixture.test.mjs': "readFile(new URL('./gone.json', import.meta.url));",
    '.codex/skills/verify-app/scripts/verify.test.mjs': "import assert from 'node:assert/strict';",
  });
  try {
    const result = await nodeTestInventory(directory);
    assert.deepEqual(result.included, ['.codex/skills/verify-app/scripts/verify.test.mjs', 'scripts/native.test.mjs'].sort());
    assert.equal(result.excluded.length, 4);
    assert.equal(result.excluded.find(row => row.file.endsWith('required.test.mjs')).reason, 'browser-package-import');
    assert.equal(result.excluded.find(row => row.file.endsWith('dynamic.test.mjs')).reason, 'browser-package-import');
    assert.equal(result.excluded.find(row => row.file.endsWith('missing.test.mjs')).dependency, 'scripts/gone.mjs');
    assert.equal(result.excluded.find(row => row.file.endsWith('fixture.test.mjs')).reason, 'missing-source-fixture');
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test('a missing dependency propagates through an import cycle to every test root', async () => {
  const directory = fixture({
    'scripts/a.test.mjs': "import './a.mjs';",
    'scripts/b.test.mjs': "import './b.mjs';",
    'scripts/a.mjs': "import './b.mjs'; import './gone.mjs';",
    'scripts/b.mjs': "import './a.mjs';",
  });
  try {
    const result = await nodeTestInventory(directory);
    assert.deepEqual(result.included, []);
    assert.equal(result.excluded.length, 2);
    for (const row of result.excluded) {
      assert.equal(row.dependency, 'scripts/gone.mjs');
      assert.equal(row.chain.at(-1), 'scripts/gone.mjs');
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test('repository inventory names actual execution commands and preserves uncovered checks', async () => {
  const result = await verificationInventory();
  assert.equal(result.status, 'inventory-only');
  assert.match(result.policy, /not evidence/);
  assert.ok(result.jobs.find(job => job.id === 'ui-unit').commands.some(run => run.command.includes('--phase behavior --shard')));
  const node = result.groups.find(group => group.id === 'node');
  assert.ok(node.included.includes('.codex/skills/verify-loom-ui/scripts/verify.test.mjs'));
  assert.ok(node.included.includes('scripts/lib/verification-inventory.test.mjs'));
  for (const row of node.excluded) assert.ok(row.reason && row.chain.length);
  const jobIds = new Set(result.jobs.map(job => job.id));
  for (const row of result.groups.find(group => group.id === 'go').files)
    if (row.route) assert.ok(jobIds.has(row.route));
  for (const [file, hash] of Object.entries(result.sources)) assert.equal(hash.length, 64, file);
});

test('import-shaped regex literals remain native while division preserves real imports', async () => {
  const prefix = "import test from 'node:test'; import assert from 'node:assert/strict';\n";
  const directory = fixture({
    'scripts/native.test.mjs': prefix + String.raw`const pattern = /import subject from "playwright"/;
      test('native assertion must execute', () => assert.fail('REAL_NATIVE_FAILURE'));`,
    'scripts/classes.test.mjs': prefix + String.raw`const pattern = /[/"']import subject from "playwright"\/suffix/gi;
      test('character class', () => assert.equal(typeof pattern.test, 'function'));`,
    'scripts/control.test.mjs': prefix + String.raw`if (true) /import subject from "playwright"/.test('x');
      function pattern() { return /require("playwright")/; }
      const ratio = 8 / 2;
      test('control and division', () => assert.equal(ratio, 4));`,
    'scripts/required.test.mjs': String.raw`const value = Math.abs(8) / require('playwright');`,
    'scripts/dynamic.test.mjs': String.raw`const value = [8][0] / (await import('playwright-core')).value;`,
    'scripts/static.test.mjs': String.raw`const pattern = /import subject from "playwright"/;
      import browser from 'playwright';`,
  });
  try {
    for (const file of ['native', 'classes', 'control', 'required', 'dynamic', 'static']) {
      const check = spawnSync(process.execPath, ['--check', path.join(directory, `scripts/${file}.test.mjs`)], { encoding: 'utf8' });
      assert.equal(check.status, 0, check.stderr);
    }
    const result = await nodeTestInventory(directory);
    assert.deepEqual(result.included, ['scripts/classes.test.mjs', 'scripts/control.test.mjs', 'scripts/native.test.mjs']);
    assert.equal(result.excluded.length, 3);
    for (const row of result.excluded) assert.equal(row.reason, 'browser-package-import');
    const env = { ...process.env };
    delete env.NODE_TEST_CONTEXT;
    const native = spawnSync(process.execPath, ['--test', path.join(directory, 'scripts/native.test.mjs')], { encoding: 'utf8', env });
    assert.equal(native.status, 1);
    assert.match(native.stdout + native.stderr, /REAL_NATIVE_FAILURE/);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});
