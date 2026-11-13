import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { backendArchitectureConformance } from './run-arango-integration.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const CAPTURE_PATH = join(here, 'backend-conformance-skipset-capture.json');
const CAPTURE_SHA256 = '04075d92b7f9bd1edad1e200045ea0d79fd8e8cece299ce9f5ae93c3a05c9126';

const raw = readFileSync(CAPTURE_PATH);
const observed = createHash('sha256').update(raw).digest('hex');
assert.equal(observed, CAPTURE_SHA256, `capture drifted from its pinned sha256: ${observed}`);
const capture = JSON.parse(raw);
assert.match(capture.goVersion, /^go version go1\.26\.5\b/, `capture pinned an unexpected toolchain: ${capture.goVersion}`);

const captureSet = new Set(capture.skips);
assert.ok(capture.skips.length > 0, 'capture holds no skip identities');

for (const name of captureSet) assert.ok(backendArchitectureConformance.has(name), `captured skip not in allowlist: ${name}`);
for (const name of backendArchitectureConformance) assert.ok(captureSet.has(name), `allowlist entry with no captured skip: ${name}`);

const stdout = execFileSync('go', ['test', capture.package, '-count=1', '-json', '-timeout', '300s', '-run', '^TestBackendArchitectureConformance$'], {
    cwd: join(here, '..'),
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    env: { ...process.env, LOOM_TEST_POSTGRES_URL: '', LOOM_TEST_ARANGO_URL: '' },
});
const fresh = new Set();
for (const line of stdout.trim().split('\n').filter(Boolean)) {
    const event = JSON.parse(line);
    if (event.Test && event.Action === 'skip' && event.Test.startsWith('TestBackendArchitectureConformance')) fresh.add(event.Test);
    assert.ok(!(event.Action === 'fail'), `offline go test reported a failure: ${event.Test ?? capture.package}`);
}
for (const name of fresh) assert.ok(captureSet.has(name), `fresh offline skip missing from the capture: ${name}`);
for (const name of captureSet) assert.ok(fresh.has(name), `captured skip no longer skips offline: ${name}`);

console.log(`backend-conformance skipset verified: ${captureSet.size} identities, capture sha256 ${observed}`);
