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
    await publishArtifacts({ directory, key, source });
    const manifestPath = join(directory, key, 'manifest.json');
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
    manifest.files.push({ name: '../escaped.js', sha256: 'bad' });
    await writeFile(manifestPath, JSON.stringify(manifest));
    assert.equal((await restoreArtifacts(args)).status, 'invalid');
    assert.deepEqual((await readdir(directory)).filter(name => name === key), [], 'An invalid entry is removed so the cache can heal');
    await publishArtifacts({ directory, key, source });
    assert.equal((await restoreArtifacts(args)).status, 'hit', 'A corrupted entry heals on the following run');
    assert.equal(await readFile(join(destination, 'app.js'), 'utf8'), 'production fixture');
    assert.deepEqual((await readdir(directory)).filter(name => name.startsWith('.building-')), [], 'Concurrent publishers remove their staging directories');
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test('publish replaces a stale partial entry instead of swallowing the collision forever', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-artifact-cache-partial-'));
  const key = 'c'.repeat(64), source = join(directory, 'source'), destination = join(directory, 'destination');
  try {
    await mkdir(source);
    await writeFile(join(source, 'app.js'), 'healed js');
    await writeFile(join(source, 'app.css'), '.healed { color: blue }');
    await publishArtifacts({ directory, key, source });
    await rm(join(directory, key, 'app.css')); // Asset removed after a valid publish.
    assert.equal((await restoreArtifacts({ directory, key, destination })).status, 'miss');
    await publishArtifacts({ directory, key, source }); // Must replace the stale directory, not no-op on ENOTEMPTY.
    assert.equal((await restoreArtifacts({ directory, key, destination })).status, 'hit', 'The next publish commits over a stale entry');
    assert.equal(await readFile(join(destination, 'app.css'), 'utf8'), '.healed { color: blue }');
  } finally { await rm(directory, { recursive: true, force: true }); }
});
