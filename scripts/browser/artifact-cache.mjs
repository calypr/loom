import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, readdir, rename, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const digest = bytes => createHash('sha256').update(bytes).digest('hex');
const validName = name => typeof name === 'string' && /^[\w.-]+\.(js|css)$/.test(name);

async function verifyEntry(directory, key) {
  const manifest = JSON.parse(await readFile(join(directory, key, 'manifest.json'), 'utf8'));
  assert.equal(manifest.version, 1);
  assert.equal(manifest.key, key);
  assert.ok(Array.isArray(manifest.files));
  const names = manifest.files.map(file => file.name);
  assert.ok(names.includes('app.js') && names.includes('app.css'));
  assert.equal(new Set(names).size, names.length);
  assert.ok(names.every(validName));
  // Read the bytes we hash, so later writes copy exactly what was verified.
  return Promise.all(manifest.files.map(async file => {
    const bytes = await readFile(join(directory, key, file.name));
    assert.equal(digest(bytes), file.sha256, `Corrupt cached asset ${file.name}`);
    return [file.name, bytes];
  }));
}

export async function restoreArtifacts({ directory, key, destination }) {
  assert.match(key, /^[a-f0-9]{64}$/);
  const entry = join(directory, key);
  let verified;
  try {
    verified = await verifyEntry(directory, key);
  } catch (error) {
    const status = error.code === 'ENOENT' ? 'miss' : 'invalid';
    // A key is content-addressed, so a non-verifying entry is never legitimate.
    // Remove it so the rebuild's publish can commit instead of looping on
    // ENOTEMPTY forever. Best effort: publish re-verifies and repairs too.
    if (status === 'invalid') await rm(entry, { recursive: true, force: true }).catch(() => {});
    return { status, reason: error.message };
  }
  await mkdir(destination, { recursive: true });
  await Promise.all(verified.map(([name, bytes]) => writeFile(join(destination, name), bytes)));
  return { status: 'hit' };
}

export async function publishArtifacts({ directory, key, source }) {
  assert.match(key, /^[a-f0-9]{64}$/);
  await mkdir(directory, { recursive: true });
  const staging = await mkdtemp(join(directory, '.building-'));
  try {
    const names = (await readdir(source)).sort();
    assert.ok(names.includes('app.js') && names.includes('app.css') && names.every(validName));
    const files = await Promise.all(names.map(async name => {
      const bytes = await readFile(join(source, name));
      await writeFile(join(staging, name), bytes);
      return { name, sha256: digest(bytes) };
    }));
    await writeFile(join(staging, 'manifest.json'), JSON.stringify({ version: 1, key, files }));
    const entry = join(directory, key);
    for (let attempt = 1; ; attempt++) {
      try { await rename(staging, entry); break; }
      catch (error) {
        if (!['EEXIST', 'ENOTEMPTY'].includes(error.code)) throw error;
        try { await verifyEntry(directory, key); break; } // A concurrent publisher committed a valid entry.
        catch { if (attempt >= 3) throw error; }
        await rm(entry, { recursive: true, force: true }); // Stale or corrupt: the fresh staging replaces it.
      }
    }
  } finally { await rm(staging, { recursive: true, force: true }); }
}
