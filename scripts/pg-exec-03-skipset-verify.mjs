// pg-exec-03 §7 byte-exactness checker for the Arango-lane skip allowlist.
//
// The Arango integration lane runs ./internal/dataframe/execution without
// LOOM_TEST_POSTGRES_URL, so the pg-exec-03 PostgreSQL post-query live
// battery must skip with exactly the identities registered in
// pgExec03PostQuery (run-arango-integration.mjs) — no more, no less. This
// checker pins that membership three ways:
//
//  1. the committed capture scripts/pg-exec-03-skipset-capture.json must
//     match its embedded sha256 (taken offline with `go version` go1.26.5
//     and LOOM_TEST_POSTGRES_URL unset);
//  2. the capture must equal the allowlist set in both directions;
//  3. a fresh offline `go test -json` skip capture must equal both.
//
// Usage: node scripts/pg-exec-03-skipset-verify.mjs
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { pgExec03PostQuery } from './run-arango-integration.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const CAPTURE_PATH = join(here, 'pg-exec-03-skipset-capture.json');
const CAPTURE_SHA256 = '455f85769836468a7e016cdf8ad20b179ce5e34a55ecd7ed7c6afa9ba407f6db';

const raw = readFileSync(CAPTURE_PATH);
const observed = createHash('sha256').update(raw).digest('hex');
assert.equal(observed, CAPTURE_SHA256, `capture drifted from its pinned sha256: ${observed}`);
const capture = JSON.parse(raw);
assert.match(capture.goVersion, /^go version go1\.26\.5\b/, `capture pinned an unexpected toolchain: ${capture.goVersion}`);

const captureSet = new Set(capture.skips);
assert.ok(capture.skips.length > 0, 'capture holds no skip identities');

// Direction 1: every captured skip is allowed.
for (const name of captureSet) assert.ok(pgExec03PostQuery.has(name), `captured skip not in allowlist: ${name}`);
// Direction 2: nothing is allowlisted without a capture behind it.
for (const name of pgExec03PostQuery) assert.ok(captureSet.has(name), `allowlist entry with no captured skip: ${name}`);

// Direction 3: a fresh offline run reproduces the capture exactly.
const stdout = execFileSync('go', ['test', capture.package, '-count=1', '-json', '-timeout', '300s'], {
    cwd: join(here, '..'),
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    env: { ...process.env, LOOM_TEST_POSTGRES_URL: '' },
});
const fresh = new Set();
for (const line of stdout.trim().split('\n').filter(Boolean)) {
    const event = JSON.parse(line);
    if (event.Test && event.Action === 'skip' && /^Test(?:ZZ)?PostgresPostQuery/.test(event.Test)) fresh.add(event.Test);
    assert.ok(!(event.Action === 'fail'), `offline go test reported a failure: ${event.Test ?? capture.package}`);
}
for (const name of fresh) assert.ok(captureSet.has(name), `fresh offline skip missing from the capture: ${name}`);
for (const name of captureSet) assert.ok(fresh.has(name), `captured skip no longer skips offline: ${name}`);

console.log(`pg-exec-03 skipset verified: ${captureSet.size} identities, capture sha256 ${observed}`);
