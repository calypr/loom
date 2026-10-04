import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { buildMigrationHelperInvocation, verifyApiSourceMount } from './lib/local-preview-index-migration.mjs';

async function createSourceRoot(path) {
  await mkdir(join(path, 'internal'), { recursive: true });
  await writeFile(join(path, 'go.mod'), 'module fixture\n');
}

function mountedAPI(sourceRoot) {
  return {
    Mounts: [
      { Destination: '/workspace/internal', Source: `/host_mnt${sourceRoot}/internal` },
      { Destination: '/workspace/go.mod', Source: `/host_mnt${sourceRoot}/go.mod` },
    ],
  };
}

test('source mount verification normalizes Docker Desktop paths and still resolves the exact checkout', async (t) => {
  const base = await mkdtemp(join(tmpdir(), 'loom-preview-index-mount-'));
  t.after(async () => rm(base, { recursive: true, force: true }));
  const checkout = join(base, 'checkout');
  const otherCheckout = join(base, 'other-checkout');
  await createSourceRoot(checkout);
  await createSourceRoot(otherCheckout);

  await assert.doesNotReject(verifyApiSourceMount(mountedAPI(checkout), checkout));
  await assert.rejects(
    verifyApiSourceMount(mountedAPI(otherCheckout), checkout),
    /API internal source mount differs from this checkout/,
  );
});

test('migration helper inherits the container PATH without starting a login shell', () => {
  const invocation = buildMigrationHelperInvocation('owned-api', '/tmp/migration with spaces', [
    'go',
    'run',
    '-overlay=/tmp/migration with spaces/overlay.json',
    './scripts/local-preview-index-migration.go',
  ]);
  assert.deepEqual(invocation.dockerArgs.slice(0, 4), ['exec', 'owned-api', 'sh', '-c']);
  assert.match(invocation.command, /^cd \/workspace && \{/);
  assert.match(invocation.command, /command -v go/);
  assert.match(invocation.command, /GOTOOLCHAIN=local GOCACHE='\/tmp\/migration with spaces\/gocache'/);
  assert.match(invocation.command, /'-overlay=\/tmp\/migration with spaces\/overlay\.json'/);
  assert.doesNotMatch(invocation.command, /sh -l/);
});
