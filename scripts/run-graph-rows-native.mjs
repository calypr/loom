#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { dirname, resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { getScenario, scenarioCaseFor } from './verify-ui/registry.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
let selected, reuseHistoryFixture, historyFixtureSHA256, checksOnly = false;
for (let i = 2; i < process.argv.length; i++) {
  const option = process.argv[i];
  if (option === '--case') selected = process.argv[++i];
  else if (option === '--reuse-history-fixture') {
    assert(process.argv[i + 1] && !process.argv[i + 1].startsWith('--'), '--reuse-history-fixture requires a path');
    reuseHistoryFixture = process.argv[++i];
  }
  else if (option === '--history-fixture-sha256') {
    assert(process.argv[i + 1] && !process.argv[i + 1].startsWith('--'), '--history-fixture-sha256 requires a hash');
    historyFixtureSHA256 = process.argv[++i];
  }
  else if (option === '--checks-only') checksOnly = true;
  else throw new Error(`Unknown option ${option}; use --case imported|ordinary|carry-in|related-rows|history-stress|upstream-dependency [--checks-only] [--reuse-history-fixture PATH --history-fixture-sha256 SHA256]`);
}
assert(['imported', 'ordinary', 'carry-in', 'related-rows', 'history-stress', 'upstream-dependency'].includes(selected), 'Select exactly one --case imported|ordinary|carry-in|related-rows|history-stress|upstream-dependency');
assert(!reuseHistoryFixture || selected === 'history-stress', 'Fixture reuse is available only for history-stress');
assert(Boolean(reuseHistoryFixture) === Boolean(historyFixtureSHA256), 'History fixture reuse requires both --reuse-history-fixture PATH and --history-fixture-sha256 SHA256');
if (historyFixtureSHA256) assert.match(historyFixtureSHA256, /^[a-f0-9]{64}$/);
const contract = scenarioCaseFor(getScenario('graph-rows'), selected);
const run = (args, timeout = 240_000) => {
  const result = spawnSync(process.execPath, args, { cwd: root, env: process.env, encoding: 'utf8', timeout, maxBuffer: 8 << 20 });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  assert.ifError(result.error);
  assert.equal(result.status, 0, `Verification command failed: node ${args.join(' ')}`);
  return result.stdout;
};
for (const check of contract.focusedChecks) {
  assert.equal(check.cwd, '.');
  assert.equal(check.command[0], 'node-test');
  run(['--test', ...check.command.slice(1)]);
}
if (!checksOnly) {
  if (selected === 'history-stress') {
    if (reuseHistoryFixture) run(['scripts/revalidate-graph-rows-history.mjs', '--fixture', resolve(reuseHistoryFixture), '--sha256', historyFixtureSHA256], 120_000);
    else run(['scripts/prepare-graph-rows-history.mjs'], 900_000);
    process.env.LOOM_GRAPH_ROWS_HISTORY_PREPARATION = join(process.env.LOOM_GRAPH_ROWS_EVIDENCE, 'history-preparation.json');
  }
  const args = ['scripts/node_modules/playwright/cli.js', 'test', '--config', 'scripts/graph-rows.playwright.config.mjs', '--grep', contract.playwrightGrep];
  const listed = run([...args, '--list']);
  assert(/Total: 1 test in 1 file/.test(listed), 'Official Playwright discovery must select exactly one native test');
  run(args);
}
