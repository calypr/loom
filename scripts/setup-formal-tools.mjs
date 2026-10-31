#!/usr/bin/env node
import assert from 'node:assert/strict';
import { copyFileSync, existsSync, mkdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { lock, digest, scriptsDigest } from './lib/formal-tools.mjs';
const [major, minor] = process.versions.node.split('.').map(Number);
assert.ok(major > 23 || major === 23 && minor >= 6 || major === 22 && minor >= 18, 'Use Node 22.18+ (or Node 23.6+) for the pinned statechart TypeScript runtime');
const options = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
    assert.ok(['--out', '--archive', '--jar'].includes(process.argv[i]) && process.argv[i + 1], 'Use --out DIR [--archive FILE] [--jar FILE]');
    assert.ok(!options.has(process.argv[i]), 'Duplicate setup option');
    options.set(process.argv[i], process.argv[i + 1]);
}
const destination = resolve(options.get('--out') || '/tmp/loom-formal-tools');
mkdirSync(destination, { recursive: true });
function obtain(file, provided, url, expected) {
    if (!existsSync(file)) {
        if (provided) {
            assert.equal(digest(provided), expected);
            copyFileSync(provided, file);
        }
        else {
            const result = spawnSync('curl', ['--fail', '--location', '--silent', '--show-error', '--max-time', '30', url, '-o', `${file}.download`], { encoding: 'utf8', timeout: 30000 });
            assert.equal(result.status, 0, result.stderr);
            assert.equal(digest(`${file}.download`), expected, 'Downloaded tool digest mismatch');
            copyFileSync(`${file}.download`, file);
        }
    }
    assert.equal(digest(file), expected, `Tool digest mismatch: ${file}`);
}
const archive = join(destination, 'pstack.tar.gz');
obtain(archive, options.get('--archive'), lock.pstack.url, lock.pstack.archiveSha256);
const scripts = join(destination, 'pstack/skills/statechart/scripts');
if (!existsSync(scripts)) {
    const extract = spawnSync('python3', ['-c', `
import pathlib,tarfile,sys
archive,out,commit=sys.argv[1:]
prefix='plugins-'+commit+'/pstack/'
with tarfile.open(archive) as tar:
 for m in tar.getmembers():
  if not m.isfile() or not (m.name.startswith(prefix+'skills/statechart/') or m.name in [prefix+'LICENSE',prefix+'LICENSE.md']):continue
  relative=pathlib.PurePosixPath(m.name[len(prefix):])
  if '..' in relative.parts:raise ValueError('invalid archive path')
  target=pathlib.Path(out)/'pstack'/relative
  target.parent.mkdir(parents=True,exist_ok=True)
  target.write_bytes(tar.extractfile(m).read())
`, archive, destination, lock.pstack.commit], { encoding: 'utf8', timeout: 30000 });
    assert.equal(extract.status, 0, extract.stderr);
}
assert.equal(scriptsDigest(scripts), lock.pstack.scriptsSha256, 'Extracted pstack digest mismatch');
const jar = join(destination, 'tla2tools.jar');
obtain(jar, options.get('--jar'), lock.tlc.url, lock.tlc.sha256);
console.log(JSON.stringify({ PSTACK_STATECHART_ROOT: scripts, TLA2TOOLS_JAR: jar }));
