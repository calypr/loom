import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repository = fileURLToPath(new URL('../', import.meta.url));
const evidence = resolve(process.argv[2] ?? `/tmp/loom-builder-reconciliation-${Date.now()}`);
const resultsPath = resolve(evidence, 'vitest.json');
const report = { scope: 'Builder workspace reconciliation unit contracts', started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });

try {
  const args = ['proxy', 'npm', 'exec', '--workspace', '@calypr/loom-ui', '--', 'vitest', 'run',
    'src/features/ExplorerBuilder/BuilderWorkspace.reconciliation.unit.test.tsx',
    '--reporter=json', `--outputFile=${resultsPath}`];
  const run = spawnSync('rtk', args, { cwd: resolve(repository, 'ui'), encoding: 'utf8', timeout: 120000 });
  report.command = ['rtk', ...args];
  report.exitCode = run.status;
  report.stdout = run.stdout;
  report.stderr = run.stderr;
  if (run.error) throw run.error;
  const results = JSON.parse(await readFile(resultsPath, 'utf8'));
  report.counts = { total: results.numTotalTests, passed: results.numPassedTests,
    failed: results.numFailedTests, skipped: results.numPendingTests, todo: results.numTodoTests };
  report.cases = results.testResults.flatMap(suite => suite.assertionResults.map(test => ({
    name: test.fullName, status: test.status, durationMs: test.duration, failures: test.failureMessages,
  })));
  assert.equal(run.status, 0, 'Workspace reconciliation suite failed; inspect vitest.json');
  assert(results.success, 'Vitest must report success');
  assert(report.cases.length >= 26, 'Expected workspace contract coverage is missing');
  assert(report.cases.every(test => test.status === 'passed'), 'Every workspace contract must execute and pass');
  for (const required of [
    'sends selected fields and concepts as one construction-choice command batch',
    'clears an interrupted attached-selection load when switching tables',
    'keeps a handed-off selection active when the current table has another collection attached',
    'assesses and applies a row change without destructive reset',
    'reassesses an ambiguous row change with the relationship chosen by the user',
    'keeps the preview visible, collapses source setup, supports focusable column selection, and applies only the reviewed receipt command',
  ]) {
    assert(report.cases.some(test => test.name.endsWith(required)), `Required contract missing: ${required}`);
  }
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  process.exitCode = 1;
} finally {
  report.finished = new Date().toISOString();
  await writeFile(resolve(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
console.log(JSON.stringify({ status: report.status, evidence, counts: report.counts, error: report.error }));
