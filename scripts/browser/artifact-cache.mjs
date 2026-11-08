import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, readdir, rename, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const digest = bytes => createHash('sha256').update(bytes).digest('hex');
const validName = name => typeof name === 'string' && /^[\w.-]+\.(js|css)$/.test(name);

export async function restoreArtifacts({ directory, key, destination }) {
  assert.match(key, /^[a-f0-9]{64}$/);
  let verified;
  try {
    const manifest = JSON.parse(await readFile(join(directory, key, 'manifest.json'), 'utf8'));
    assert.equal(manifest.version, 1);
    assert.equal(manifest.key, key);
    assert.ok(Array.isArray(manifest.files));
    const names = manifest.files.map(file => file.name);
    assert.ok(names.includes('app.js') && names.includes('app.css'));
    assert.equal(new Set(names).size, names.length);
    assert.ok(names.every(validName));
    verified = await Promise.all(manifest.files.map(async file => {
      const bytes = await readFile(join(directory, key, file.name));
      assert.equal(digest(bytes), file.sha256, `Corrupt cached asset ${file.name}`);
      return [file.name, bytes];
    }));
  } catch (error) {
    return { status: error.code === 'ENOENT' ? 'miss' : 'invalid', reason: error.message };
  }
  // Copy the bytes we hashed, rather than reopening potentially changed files.
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
    try { await rename(staging, join(directory, key)); }
    catch (error) { if (!['EEXIST', 'ENOTEMPTY'].includes(error.code)) throw error; }
  } finally { await rm(staging, { recursive: true, force: true }); }
}
