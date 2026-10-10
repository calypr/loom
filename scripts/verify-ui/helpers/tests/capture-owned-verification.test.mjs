import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const captureScript = fileURLToPath(new URL('../../../capture-owned-verification.mjs', import.meta.url));
const sourceRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..');

test('before capture rejects a missing API precheck before capture setup starts', () => {
  const env = { ...process.env };
  for (const name of Object.keys(env)) {
    if (name.startsWith('LOOM_CDA_')) delete env[name];
  }
  const result = spawnSync(process.execPath, [captureScript, '--phase', 'before'], {
    encoding: 'utf8',
    env,
  });

  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Before capture requires --precheck-input/);
  assert.doesNotMatch(result.stderr, /LOOM_CDA_SOURCE_ROOT|Provide --source-output/);
});

test('before capture rejects null, false, and zero precheck artifacts before target capture', () => {
  const env = {
    ...process.env,
    LOOM_CDA_SOURCE_ROOT: sourceRoot,
    LOOM_CDA_PROJECT: 'owned-project',
    LOOM_CDA_API_ORIGIN: 'http://127.0.0.1:8188',
    LOOM_CDA_UI_ORIGIN: 'http://127.0.0.1:30008',
    LOOM_CDA_API_CONTAINER: 'owned-api',
    LOOM_CDA_COMPOSE_PROJECT: 'owned-compose',
    LOOM_CDA_ARANGO_CONTAINER: 'owned-arango',
    LOOM_CDA_CLICKHOUSE_CONTAINER: 'owned-clickhouse',
  };
  const root = mkdtempSync(join(tmpdir(), 'capture-owned-precheck-falsy-'));
  try {
    for (const artifact of ['null', 'false', '0']) {
      const precheck = join(root, `precheck-${artifact}.json`);
      const outputs = ['source', 'docs', 'api', 'mounts'].map(name => join(root, `${name}-${artifact}.json`));
      writeFileSync(precheck, artifact);
      const result = spawnSync(process.execPath, [
        captureScript, '--phase', 'before', '--precheck-input', precheck,
        '--source-output', outputs[0], '--docs-output', outputs[1],
        '--api-output', outputs[2], '--mount-output', outputs[3],
      ], { encoding: 'utf8', env });

      assert.notEqual(result.status, 0, `${artifact} must not bypass API precheck validation`);
      assert.match(result.stderr, /API build precheck must be an object/);
      assert(outputs.every(path => !existsSync(path)), 'Invalid precheck must fail before capture outputs are written.');
    }
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('before capture rejects output aliases that would overwrite the precheck artifact', () => {
  const root = mkdtempSync(join(tmpdir(), 'capture-owned-precheck-alias-'));
  const precheck = join(root, 'api-build-precheck.json');
  const sourceOutput = join(root, 'source-before.json');
  const precheckContents = JSON.stringify({ producer: 'owned-stack-verification' });
  try {
    writeFileSync(precheck, precheckContents);
    symlinkSync(precheck, sourceOutput);
    const result = spawnSync(process.execPath, [
      captureScript, '--phase', 'before', '--precheck-input', precheck,
      '--source-output', sourceOutput,
      '--docs-output', join(root, 'docs-before.json'),
      '--api-output', join(root, 'api-before.json'),
      '--mount-output', join(root, 'mounts-before.json'),
    ], { encoding: 'utf8', env: process.env });

    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /Precheck input must not alias a capture output path/);
    assert.equal(readFileSync(precheck, 'utf8'), precheckContents, 'Rejected alias must preserve the precheck artifact.');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
