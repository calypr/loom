import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { realpath } from 'node:fs/promises';
import { captureSourceFreeze } from './source-freeze.mjs';
import { sourceFingerprint } from '../verify-ui/source-fingerprint.mjs';

export function readBuildIdentity(apiContainer) {
  const value = execFileSync('docker', ['exec', apiContainer, '/workspace/loom-dev-build-stamp.sh', '--check'], {
    encoding: 'utf8', timeout: 10000,
  }).trim();
  assert.match(value, /^[a-f0-9]{64}\s+[a-f0-9]{64}\s+[a-f0-9]{64}$/i);
  return value.split(/\s+/).join(':').toLowerCase();
}

export async function startVerificationIdentity(sourceRoot, apiContainer) {
  const root = await realpath(sourceRoot);
  const sourceFreeze = await captureSourceFreeze(root);
  return {
    root,
    sourceFreeze,
    sourceFingerprint: sourceFingerprint(root),
    apiBuildIdentity: readBuildIdentity(apiContainer),
    async finish() {
      const freezeResult = await sourceFreeze.assertUnchanged();
      const fingerprintAfter = sourceFingerprint(root);
      assert.deepEqual(fingerprintAfter, this.sourceFingerprint, 'Source fingerprint changed during CDA browser verification');
      const buildAfter = readBuildIdentity(apiContainer);
      assert.equal(buildAfter, this.apiBuildIdentity, 'API build identity changed during CDA browser verification');
      return { sourceFingerprint: { before: this.sourceFingerprint, after: fingerprintAfter }, apiBuildIdentity: { before: this.apiBuildIdentity, after: buildAfter }, sourceFreeze: freezeResult };
    },
  };
}
