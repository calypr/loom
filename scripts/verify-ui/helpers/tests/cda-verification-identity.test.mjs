import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { chmod, mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';
import { startVerificationIdentity } from '../cda-verification-identity.mjs';
import { sourceFingerprintWithManifest } from '../source-fingerprint.mjs';
import { SourceFreezeError } from '../source-freeze.mjs';

const execFileAsync = promisify(execFile);
const stamp = (char) => `${char.repeat(64)} ${char.repeat(64)} ${char.repeat(64)}\n`;

async function createFixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'cda-verification-identity-'));
  const originalPath = process.env.PATH;
  const originalStampFile = process.env.FAKE_DOCKER_STAMP_FILE;
  t.after(async () => {
    if (originalPath === undefined) delete process.env.PATH;
    else process.env.PATH = originalPath;
    if (originalStampFile === undefined) delete process.env.FAKE_DOCKER_STAMP_FILE;
    else process.env.FAKE_DOCKER_STAMP_FILE = originalStampFile;
    await rm(root, { recursive: true, force: true });
  });

  await execFileAsync('git', ['init', '--quiet'], { cwd: root });
  await execFileAsync('git', ['config', 'user.name', 'Verification Identity Test'], { cwd: root });
  await execFileAsync('git', ['config', 'user.email', 'verification-identity@example.invalid'], { cwd: root });
  await mkdir(path.join(root, 'internal'), { recursive: true });
  await mkdir(path.join(root, 'bin'), { recursive: true });
  await writeFile(path.join(root, 'internal', 'watched.go'), 'package watched\n');
  await writeFile(path.join(root, 'go.mod'), 'module example.com/loom-test\n\ngo 1.22\n');
  const stampPath = path.join(root, 'api-stamp.txt');
  await writeFile(stampPath, stamp('a'));
  const dockerPath = path.join(root, 'bin', 'docker');
  await writeFile(dockerPath, '#!/bin/sh\ncat "$FAKE_DOCKER_STAMP_FILE"\n');
  await chmod(dockerPath, 0o755);
  await execFileAsync('git', ['add', 'internal/watched.go', 'go.mod'], { cwd: root });

  process.env.PATH = `${path.join(root, 'bin')}${path.delimiter}${originalPath ?? ''}`;
  process.env.FAKE_DOCKER_STAMP_FILE = stampPath;
  return {
    root,
    stampPath,
    setStamp: async (char) => writeFile(stampPath, stamp(char)),
  };
}

test('stable source and API identities pass verification finish', async (t) => {
  const { root } = await createFixture(t);
  const identity = await startVerificationIdentity(root, 'fake-api');

  const result = await identity.finish();

  assert.equal(result.sourceFreeze.unchanged, true);
  assert.deepEqual(result.sourceFingerprint.before, result.sourceFingerprint.after);
  assert.deepEqual(result.apiBuildIdentity.before, result.apiBuildIdentity.after);
});

test('a watched internal source edit fails through source-freeze', async (t) => {
  const { root } = await createFixture(t);
  const identity = await startVerificationIdentity(root, 'fake-api');
  await writeFile(path.join(root, 'internal', 'watched.go'), 'package watched\n// changed during verification\n');

  await assert.rejects(identity.finish(), (error) => {
    assert(error instanceof SourceFreezeError);
    assert.deepEqual(error.changedPaths, [{ path: 'internal/watched.go', change: 'modified' }]);
    assert.equal(error.invalidatesRun, true);
    return true;
  });
});

test('a sourceFingerprint-only go.mod edit invalidates the verification identity', async (t) => {
  const { root } = await createFixture(t);
  const identity = await startVerificationIdentity(root, 'fake-api');
  await writeFile(path.join(root, 'go.mod'), 'module example.com/changed\n\ngo 1.22\n');

  await assert.rejects(identity.finish(), /Source manifest changed during CDA browser verification/);
});

test('a changed API build stamp invalidates the verification identity', async (t) => {
  const { root, setStamp } = await createFixture(t);
  const identity = await startVerificationIdentity(root, 'fake-api');
  await setStamp('b');

  await assert.rejects(identity.finish(), /API build identity changed during CDA browser verification/);
});

test('source manifest capture supplies the fingerprint baseline', async (t) => {
  const { root } = await createFixture(t);
  const identity = await startVerificationIdentity(root, 'fake-api');

  assert.deepEqual(identity.sourceCapture, sourceFingerprintWithManifest(root));
  assert.deepEqual(identity.sourceCapture.fingerprint, identity.sourceFingerprint);
  assert.equal(identity.sourceCapture.manifest['go.mod'], (await sourceFingerprintWithManifest(root)).manifest['go.mod']);
  const result = await identity.finish();
  assert.equal(result.sourceFreeze.unchanged, true);
  assert.deepEqual(result.sourceFingerprint.before, identity.sourceFingerprint);
  assert.deepEqual(result.sourceFingerprint.after, identity.sourceFingerprint);
});
