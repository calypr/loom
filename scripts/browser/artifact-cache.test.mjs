import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, readFile, writeFile, rm, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { restoreArtifacts, publishArtifacts } from './artifact-cache.mjs';

test('reuse only exact-source, verified assets; reject corruption and unsafe manifests', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-artifact-cache-test-'));
  const key = 'a'.repeat(64), source = join(directory, 'source'), destination = join(directory, 'destination');
  try {
    await mkdir(source);
    await writeFile(join(source, 'app.js'), 'production fixture');
    await writeFile(join(source, 'app.css'), '.production { color: red }');
    await writeFile(join(source, 'chunk-123.js'), 'dynamic fixture');
    const args = { directory, key, destination };
    assert.equal((await restoreArtifacts(args)).status, 'miss');
    await Promise.all([publishArtifacts({ directory, key, source }), publishArtifacts({ directory, key, source })]);
    assert.equal((await restoreArtifacts(args)).status, 'hit');
    assert.equal(await readFile(join(destination, 'app.js'), 'utf8'), 'production fixture');
    assert.equal(await readFile(join(destination, 'chunk-123.js'), 'utf8'), 'dynamic fixture');
    assert.equal((await restoreArtifacts({ ...args, key: 'b'.repeat(64) })).status, 'miss', 'A source change must not reuse old assets');
    await writeFile(join(directory, key, 'app.js'), 'broken fixture');
    assert.equal((await restoreArtifacts(args)).status, 'invalid', 'Corruption is never a cache hit');
    assert.equal(await readFile(join(destination, 'app.js'), 'utf8'), 'production fixture', 'Do not copy unverified bytes');
    const manifestPath = join(directory, key, 'manifest.json');
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
    manifest.files.push({ name: '../escaped.js', sha256: 'bad' });
    await writeFile(manifestPath, JSON.stringify(manifest));
    assert.equal((await restoreArtifacts(args)).status, 'invalid');
    assert.deepEqual((await readdir(directory)).filter(name => name.startsWith('.building-')), [], 'Concurrent publishers remove their staging directories');
  } finally { await rm(directory, { recursive: true, force: true }); }
});
