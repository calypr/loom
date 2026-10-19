import assert from 'node:assert/strict';
import { realpath } from 'node:fs/promises';
import { join } from 'node:path';
import { normalizeDockerHostPath } from '../loom-dev.mjs';

export function buildMigrationHelperInvocation(container, remote, helperArgs) {
  const command = `cd /workspace && { command -v go >/dev/null 2>&1 || { echo 'go executable not found in API container PATH' >&2; exit 127; }; GOTOOLCHAIN=local GOCACHE=${shellQuote(`${remote}/gocache`)} ${helperArgs.map(shellQuote).join(' ')}; }`;
  return { dockerArgs: ['exec', container, 'sh', '-c', command], command };
}

export async function verifyApiSourceMount(api, sourceRoot) {
  const root = await realpath(sourceRoot);
  const mounts = api.Mounts ?? [];
  const internal = mounts.find((mount) => mount.Destination === '/workspace/internal');
  const module = mounts.find((mount) => mount.Destination === '/workspace/go.mod');
  assert(internal && module, 'owned API container must mount internal/ and go.mod from the active source checkout');
  const internalSource = await realpath(normalizeDockerHostPath(String(internal.Source ?? '')));
  const moduleSource = await realpath(normalizeDockerHostPath(String(module.Source ?? '')));
  assert.equal(internalSource, join(root, 'internal'), 'API internal source mount differs from this checkout');
  assert.equal(moduleSource, join(root, 'go.mod'), 'API module source mount differs from this checkout');
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}
