#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createDevSession } from '../loom-dev.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const session = createDevSession(process.env, root);
const specs = readdirSync(join(root, 'scripts/e2e')).filter(name => name.endsWith('.spec.mjs')).sort();
assert.ok(specs.length, 'No end-to-end specs found');
const result = spawnSync(process.execPath, ['--test', '--test-concurrency=1', ...specs.map(name => `scripts/e2e/${name}`)], {
  cwd: root, stdio: 'inherit', detached: true, timeout: 8 * 60 * 1000,
  env: { ...process.env, LOOM_DEV_PROJECT: session.fixtureProject, LOOM_DEV_UI_URL: session.uiUrl, LOOM_DEV_API_URL: session.apiUrl },
});
if (result.error && result.pid) {
  try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
}
if (result.error) throw result.error;
assert.equal(result.status, 0, `End-to-end tests failed (${result.signal ?? result.status})`);
