import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { registry } from './verify-ui/registry.mjs';
import { planFocusedCheckGroups } from './verify-ui/helpers/focused-check-groups.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const tool = join(root, 'scripts/build-test-obligations.mjs');
const invoke = (...args) => spawnSync(process.execPath, [tool, ...args], { cwd: root, encoding: 'utf8', timeout: 30_000 });
const sha256 = value => createHash('sha256').update(value).digest('hex');

function withDirectory(run) {
  const directory = mkdtempSync(join(tmpdir(), 'loom-obligation-report-'));
  try { run(directory); } finally { rmSync(directory, { recursive: true, force: true }); }
}

test('model parsing and focused-receipt negative controls pass without a source catalog', () => {
  const result = invoke('--self-test');
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /10 fixtures/);
  assert.match(result.stdout, /fail-closed negatives/);
});

test('derived report contains every registered case and rejects a corrupted export', () => withDirectory(directory => {
  const json = join(directory, 'report.json');
  const markdown = join(directory, 'report.md');
  const args = ['--json', json, '--markdown', markdown];
  const generated = invoke(...args);
  assert.equal(generated.status, 0, generated.stderr);
  const report = JSON.parse(readFileSync(json, 'utf8'));
  assert.equal(Object.hasOwn(report.sources, 'semanticCatalog'), false);
  assert.equal(Object.hasOwn(report, 'mjsCatalogSummary'), false);
  const tests = new Map(report.tests.map(row => [row.id, row]));
  for (const scenario of registry) {
    for (const [caseName, contract] of Object.entries(scenario.cases ?? {})) {
      const row = tests.get(`playwright:${scenario.id}:${caseName}`);
      assert.ok(row, `${scenario.id}:${caseName} is missing`);
      assert.deepEqual(row.focusedChecks, (contract.focusedChecks ?? []).map(check => check.id).sort());
      assert.equal(Object.hasOwn(row, 'semanticClaimIds'), false);
    }
  }
  assert.equal(invoke(...args, '--check').status, 0);
  writeFileSync(json, '{}\n');
  const corrupt = invoke(...args, '--check');
  assert.notEqual(corrupt.status, 0);
  assert.match(corrupt.stderr, /is stale/);
}));

test('runtime receipts credit only executed tests and reject stale inputs and altered commands', () => withDirectory(directory => {
  const scenario = registry.find(row => row.id === 'cda-workspace-combine');
  const contract = scenario.cases['group-pivot-join'];
  const plans = planFocusedCheckGroups(contract.focusedChecks, root);
  const groups = contract.focusedChecks.map((check, index) => {
    const plan = plans[index];
    const paths = [...check.command.slice(1), ...check.sourceFiles];
    const inputs = paths.map(path => ({ path, sha256: sha256(readFileSync(resolve(root, path))) }));
    return { id: check.id, runner: plan.runner, status: 'passed', exitCode: 0, timedOut: false,
      inputs, inputsUnchanged: true, declaredInputsHash: sha256(JSON.stringify(inputs)) };
  });
  const commands = Object.fromEntries(plans.map(plan => [`focused-${plan.id}`, {
    executable: plan.executable, arguments: plan.args, cwd: plan.cwd, exitCode: 0,
  }]));
  const summary = { schemaVersion: 1, scenario: scenario.id, case: 'group-pivot-join', mode: 'checks-only',
    focusedChecks: { groups }, commands };
  const source = join(directory, 'summary.json');
  const output = join(directory, 'receipt.json');
  const run = value => {
    writeFileSync(source, JSON.stringify(value));
    const result = invoke('--runtime-summary', source, '--runtime-output', output);
    assert.equal(result.status, 0, result.stderr);
    return JSON.parse(readFileSync(output, 'utf8'));
  };
  const valid = run(summary);
  assert.equal(valid.schemaVersion, 2);
  assert.deepEqual(valid.executedTests.map(row => row.path), contract.focusedChecks[0].command.slice(1));
  assert.equal(valid.rejectedGroups.length, 0);
  assert.match(valid.scope, /no case, journey, browser, or formal-model pass/);
  const stale = structuredClone(summary);
  stale.focusedChecks.groups[0].inputs[1].sha256 = '0'.repeat(64);
  stale.focusedChecks.groups[0].declaredInputsHash = sha256(JSON.stringify(stale.focusedChecks.groups[0].inputs));
  const rejected = run(stale);
  assert.equal(rejected.executedTests.length, 0);
  assert.ok(rejected.rejectedGroups.length > 0);
  const altered = structuredClone(summary);
  altered.commands[`focused-${plans[0].id}`].arguments.push('--unregistered');
  assert.equal(run(altered).executedTests.length, 0);
}));
