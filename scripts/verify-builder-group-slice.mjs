#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { createDevSession } from './loom-dev.mjs';
import { resolveTlaToolsJar } from './verify-builder-traces.mjs';
import { sourceFingerprint } from './verify-ui/helpers/source-fingerprint.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const { values } = parseArgs({ options: {
  'checks-only': { type: 'boolean' },
  help: { type: 'boolean' },
} });
if (values.help) {
  console.log('usage: node scripts/verify-builder-group-slice.mjs [--checks-only]');
  console.log('Uses the owned LOOM_DEV_* stack and checksum-pinned TLA2TOOLS_JAR. The default includes the native Group browser lifecycle and captured-trace replay.');
  process.exit(0);
}

const directory = mkdtempSync(join(tmpdir(), 'loom-group-slice-'));
const result = { status: 'failed', browser: 'not-run', directory, checks: [] };
const initialSource = sourceFingerprint(root);
const target = createDevSession(process.env, root);
const env = {
  ...process.env,
  GOCACHE: process.env.LOOM_GO_CACHE ?? join(tmpdir(), 'loom-go-build-cache'),
  LOOM_DEV_SOURCE_ROOT: root,
  LOOM_DEV_COMPOSE_PROJECT: target.composeProject,
  LOOM_DEV_PROJECT: target.fixtureProject,
  LOOM_DEV_GENERATION: target.fixtureGeneration,
  LOOM_DEV_API_PORT: String(target.apiPort),
  LOOM_DEV_UI_PORT: String(target.uiPort),
  LOOM_CDA_SOURCE_ROOT: root,
  LOOM_CDA_PROJECT: target.fixtureProject,
  LOOM_CDA_GENERATION: target.fixtureGeneration,
  LOOM_CDA_API_ORIGIN: target.apiUrl,
  LOOM_CDA_UI_ORIGIN: target.uiUrl,
  LOOM_CDA_COMPOSE_PROJECT: target.composeProject,
  LOOM_CDA_API_CONTAINER: `${target.composeProject}-loom-api-1`,
  LOOM_CDA_ARANGO_CONTAINER: `${target.composeProject}-arangodb-1`,
  LOOM_CDA_CLICKHOUSE_CONTAINER: `${target.composeProject}-clickhouse-1`,
};

function run(name, executable, args, cwd = root) {
  console.log(`Running ${name}`);
  const log = join(directory, `${name}.log`);
  let output;
  try {
    output = execFileSync(executable, args, { cwd, env, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
  } catch (error) {
    writeFileSync(log, `${error.stdout ?? ''}\n${error.stderr ?? ''}`);
    throw new Error(`${name} failed; see ${log}`, { cause: error });
  }
  writeFileSync(log, output);
  assert.equal(sourceFingerprint(root).sha256, initialSource.sha256, `source changed during ${name}`);
  result.checks.push({ name, log });
  return output;
}

try {
  const jar = resolveTlaToolsJar({ env });
  const modelOutput = run('group-model', 'java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC',
    '-workers', '1', '-metadir', join(directory, 'tlc'), '-config', 'BuilderJourneyGroupSafety.cfg', 'BuilderJourney.tla'],
  join(root, 'verification/builder-interactions'));
  assert.match(modelOutput, /Model checking completed\. No error has been found\./);

  const uiReport = join(directory, 'frontend.json');
  run('frontend', process.execPath, [join(root, 'ui/node_modules/vitest/vitest.mjs'), 'run',
    'src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx',
    '-t', 'replays explicit cancellation schedule|regression: old Apply', '--reporter=json', `--outputFile=${uiReport}`],
  join(root, 'ui/packages/loom-ui'));
  const frontend = JSON.parse(readFileSync(uiReport, 'utf8'));
  const selected = frontend.testResults.flatMap(suite => suite.assertionResults)
    .filter(test => /replays explicit cancellation schedule|regression: old Apply/.test(test.fullName));
  assert.equal(selected.length, 5, 'all three cancellation schedules and both Apply ownership regressions must run');
  assert(selected.every(test => test.status === 'passed'), 'selected frontend schedules must pass without skips');

  const backendTest = 'TestBuilderFullUIGroupFilterDeriveAggregateEditReplaysLifecycle';
  const backend = run('backend', 'go', ['test', './internal/explorer/lifecycle', '-count=1', '-v', '-run', `^${backendTest}$`]);
  assert(backend.includes(`--- PASS: ${backendTest}`), 'real-service lifecycle must run and pass');

  if (!values['checks-only']) {
    const browser = JSON.parse(run('browser', process.execPath, [join(root, 'scripts/run-native-verification-bracket.mjs'),
      '--scenario', 'builder-authoring', '--case', 'group-entry', '--target-from-environment',
      '--grep', 'standard Group entry proposes an empty-key COUNT_ROWS preview$']));
    assert.equal(browser.status, 'passed', 'native browser bracket must pass');
    assert(browser.report, 'native browser bracket must retain its domain report');
    const domain = JSON.parse(readFileSync(browser.report, 'utf8'));
    assert.equal(domain.target?.builderJourneyTrace?.status, 'pass', 'observed browser trace must pass TLA+ replay');
    result.browser = 'passed';
    result.browserSummary = browser.summary;
    result.trace = domain.target.builderJourneyTrace;
  }
  result.status = values['checks-only'] ? 'checks-passed-browser-unverified' : 'passed';
} catch (error) {
  result.error = error.message;
  process.exitCode = 1;
} finally {
  writeFileSync(join(directory, 'summary.json'), JSON.stringify(result, null, 2) + '\n');
  console.log(JSON.stringify(result, null, 2));
}
