import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { chmod, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import {
  buildExecuteRequestScript,
  buildExplainRequestScript,
} from './owned-query-probe.mjs';

const repoRoot = resolve(import.meta.dirname, '..');
const cliPath = join(repoRoot, 'scripts', 'owned-query-probe.mjs');
const sensitiveCategory = 'PRIVATE_CATEGORY_VALUE_79b1';
const sensitiveResourcePath = 'PRIVATE_RESOURCE_PATH_40ca';
const indexName = 'loom_probe_test_index';
const ownedContainer = {
  Name: '/loom-dev-6d7df93d6a37-arangodb-1',
  State: { Running: true },
  Config: {
    Labels: {
      'com.docker.compose.project': 'loom-dev-6d7df93d6a37',
      'com.docker.compose.service': 'arangodb',
    },
    Env: ['ARANGO_NO_AUTH=1'],
  },
};
const queryText = `FOR root IN @@root_collection
  FILTER root.project == @project
    AND root.dataset_generation == @dataset_generation
    AND (@auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths)
  FOR terminal IN @@__loom_related_category_target_collection
    FILTER terminal.project == @project
      AND terminal.dataset_generation == @dataset_generation
      AND (@auth_resource_paths_unrestricted == true OR terminal.auth_resource_path IN @auth_resource_paths)
  RETURN {root: root._key, terminal: terminal._key, category: @category}`;
const unrestrictedBinds = {
  '@root_collection': 'Specimen',
  '@__loom_related_category_target_collection': 'Observation',
  project: 'loom_dev_cda_fhir',
  dataset_generation: 'cda-fhir-v1',
  auth_resource_paths_unrestricted: true,
  auth_resource_paths: null,
  category: sensitiveCategory,
};

function keysNamed(value, wanted, found = []) {
  if (Array.isArray(value)) {
    for (const item of value) keysNamed(item, wanted, found);
  } else if (value && typeof value === 'object') {
    for (const [key, child] of Object.entries(value)) {
      if (key === wanted) found.push(child);
      keysNamed(child, wanted, found);
    }
  }
  return found;
}

function sha256(bytes) {
  return createHash('sha256').update(bytes).digest('hex');
}

async function makeHarness(t) {
  const dir = await mkdtemp(join(tmpdir(), 'loom-owned-query-probe-'));
  t.after(async () => rm(dir, { recursive: true, force: true }));
  const binDir = join(dir, 'bin');
  const artifactDir = join(dir, 'artifacts');
  await import('node:fs/promises').then(({ mkdir }) => Promise.all([
    mkdir(binDir),
    mkdir(artifactDir),
  ]));
  const eventsPath = join(dir, 'events.jsonl');
  const dockerCallsPath = join(dir, 'docker-calls.jsonl');
  const dockerPath = join(binDir, 'docker');
  const rtkPath = join(binDir, 'rtk');
  const node = process.execPath;
  const dockerSource = `#!${node}
import { appendFileSync } from 'node:fs';
const args = process.argv.slice(2);
appendFileSync(process.env.PROBE_DOCKER_CALLS, JSON.stringify(args) + '\\n');
if (process.env.PROBE_DOCKER_FAIL === '1') {
  process.stderr.write('PRIVATE_DOCKER_ERROR_DETAIL');
  process.exit(19);
}
if (args.length !== 2 || args[0] !== 'inspect') process.exit(20);
process.stdout.write(JSON.stringify([${JSON.stringify(ownedContainer)}]));
`;
  const rtkSource = `#!${node}
import { appendFileSync } from 'node:fs';
const args = process.argv.slice(2);
const shellCommand = args.at(-1) ?? '';
const operation = shellCommand.includes('db._query(') ? 'execute' : 'explain';
appendFileSync(process.env.PROBE_EVENTS, JSON.stringify({
  operation,
  hasEscapedAt: shellCommand.includes('\\\\u0040'),
  hasRawAt: shellCommand.includes('@'),
}) + '\\n');
if (process.env.PROBE_HANG_OPERATION === operation) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 5000);
  process.exit(0);
}
if (operation === 'explain') {
  const scenario = process.env.PROBE_EXPLAIN_SCENARIO ?? 'normal';
  const expected = scenario === 'mismatch' ? 'other_index' : ${JSON.stringify(indexName)};
  const node = {
    id: 7,
    type: 'IndexNode',
    collection: 'Observation',
    indexes: [{ id: 'Observation/77', name: expected, type: 'persistent', fields: ['project', 'dataset_generation', 'auth_resource_path'] }],
  };
  const nodes = scenario === 'write' ? [node, { id: 8, type: 'InsertNode', collection: 'Observation' }] : [node];
  const envelope = {
    stage: scenario === 'bad-body' ? 'explain-failed' : 'explain-complete',
    parseStatusCode: 200,
    parseServerElapsedMs: 4,
    explainStatusCode: scenario === 'bad-body' ? 400 : 200,
    explainServerElapsedMs: 6,
    errorNum: scenario === 'bad-body' ? 1203 : null,
    errorCode: scenario === 'bad-body' ? 400 : 200,
    message: scenario === 'bad-body' ? 'Arango reported a query error.' : null,
    parserReadOnly: true,
    planCount: 1,
    planEstimates: [{ plan: 0, estimatedCost: 4, estimatedNrItems: 2 }],
    nodes: nodes.map(value => ({ plan: 0, nodeId: value.id, type: value.type, collection: value.collection, estimatedNrItems: 2 })),
    indexes: [{ plan: 0, nodeId: 7, nodeType: 'IndexNode', collection: 'Observation', id: 'Observation/77', name: expected, type: 'persistent', fields: ['project', 'dataset_generation', 'auth_resource_path'] }],
    warnings: [{ code: 321, message: 'Arango reported a query warning.' }],
    readOnlyPlan: scenario !== 'write',
  };
  const marker = [...shellCommand.matchAll(/__LOOM_[A-Z0-9_]+__/g)].map(match => match[0])[0];
  if (!marker) process.exit(21);
  process.stdout.write(marker + JSON.stringify(envelope) + '\\n');
} else {
  const scenario = process.env.PROBE_EXECUTE_SCENARIO ?? 'normal';
  const envelope = scenario === 'no-stats'
    ? { ok: true, elapsedMs: 7, resultCount: 2, stats: null }
    : scenario === 'error'
      ? { ok: false, elapsedMs: 7, errorNum: 32, errorCode: 400, message: 'Arango reported a query error.', rawError: 'PRIVATE_QUERY_ERROR_DETAIL' }
      : scenario === 'malformed-envelope'
        ? { elapsedMs: 7, resultCount: 1, safeIntegerCounts: { scoped_roots: 7 }, errorNum: 32, errorCode: 400, message: 'PRIVATE_QUERY_ERROR_DETAIL', rawError: 'PRIVATE_QUERY_ERROR_DETAIL', rows: [{ category: ${JSON.stringify(sensitiveCategory)} }] }
        : scenario === 'safe-counts-zero'
          ? { ok: true, elapsedMs: 7, resultCount: 0, safeIntegerCounts: { scoped_roots: 7 } }
          : scenario === 'safe-counts-many'
            ? { ok: true, elapsedMs: 7, resultCount: 2, safeIntegerCounts: { scoped_roots: 7 } }
      : scenario === 'safe-integer-counts'
        ? { ok: true, elapsedMs: 7, resultCount: 1, safeIntegerCounts: { scoped_roots: 742505, reachable_patients: 81 }, stats: { executionTime: 0.007, scannedIndex: 11, scannedFull: 0, peakMemoryUsage: 123456 } }
      : { ok: true, elapsedMs: 7, resultCount: 2, stats: { executionTime: 0.007, scannedIndex: 11, scannedFull: 0, peakMemoryUsage: 123456 } };
  const marker = [...shellCommand.matchAll(/__LOOM_[A-Z0-9_]+__/g)].map(match => match[0])[0];
  if (!marker) process.exit(22);
  process.stdout.write(marker + JSON.stringify(envelope) + '\\n');
}
`;
  await writeFile(dockerPath, dockerSource);
  await writeFile(rtkPath, rtkSource);
  await chmod(dockerPath, 0o755);
  await chmod(rtkPath, 0o755);
  return { dir, binDir, artifactDir, eventsPath, dockerCallsPath };
}

async function runProbe(t, {
  query = queryText,
  bindVars = unrestrictedBinds,
  args = [],
  dockerFail = false,
  explainScenario = 'normal',
  executeScenario = 'normal',
  hangOperation = '',
  existingOutput = false,
} = {}) {
  const harness = await makeHarness(t);
  const queryPath = join(harness.artifactDir, 'query.aql');
  const bindPath = join(harness.artifactDir, 'bind-vars.json');
  const outputPath = join(harness.artifactDir, 'report.json');
  const queryBytes = Buffer.from(query);
  const bindBytes = Buffer.from(`${JSON.stringify(bindVars, null, 2)}\n`);
  await writeFile(queryPath, queryBytes);
  await writeFile(bindPath, bindBytes);
  if (existingOutput) await writeFile(outputPath, 'preserve this report\n');
  const env = {
    ...process.env,
    PATH: `${harness.binDir}:${process.env.PATH}`,
    PROBE_DOCKER_CALLS: harness.dockerCallsPath,
    PROBE_EVENTS: harness.eventsPath,
    PROBE_DOCKER_FAIL: dockerFail ? '1' : '0',
    PROBE_EXPLAIN_SCENARIO: explainScenario,
    PROBE_EXECUTE_SCENARIO: executeScenario,
    PROBE_HANG_OPERATION: hangOperation,
  };
  const command = spawnSync(process.execPath, [
    cliPath,
    '--query', queryPath,
    '--bind-vars', bindPath,
    '--output', outputPath,
    ...args,
  ], { cwd: repoRoot, env, encoding: 'utf8', timeout: 20000, maxBuffer: 2 * 1024 * 1024 });
  let report = null;
  if (!existingOutput) {
    try {
      report = JSON.parse(await readFile(outputPath, 'utf8'));
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  }
  let events = [];
  try {
    events = (await readFile(harness.eventsPath, 'utf8')).trim().split('\n').filter(Boolean).map(JSON.parse);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  let dockerCalls = [];
  try {
    dockerCalls = (await readFile(harness.dockerCallsPath, 'utf8')).trim().split('\n').filter(Boolean).map(JSON.parse);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  return { ...harness, queryPath, bindPath, outputPath, queryBytes, bindBytes, command, report, events, dockerCalls };
}

function assertNoSensitiveValues(run) {
  const visible = `${run.command.stdout ?? ''}\n${run.command.stderr ?? ''}\n${JSON.stringify(run.report)}`;
  assert(!visible.includes(sensitiveCategory), 'category value leaked to CLI output or report');
  assert(!visible.includes(sensitiveResourcePath), 'resource path leaked to CLI output or report');
  assert(!visible.includes('PRIVATE_WARNING_DETAIL'), 'raw warning message leaked');
  assert(!visible.includes('PRIVATE_ARANGO_ERROR_DETAIL'), 'raw Arango error leaked');
  assert(!visible.includes('PRIVATE_DOCKER_ERROR_DETAIL'), 'raw Docker error leaked');
  assert(!visible.includes('PRIVATE_QUERY_ERROR_DETAIL'), 'raw query error leaked');
}

test('CLI defaults to EXPLAIN, records hashes and safe plan metadata without data values', async (t) => {
  const run = await runProbe(t, { args: ['--expected-index', indexName] });
  assert.equal(run.command.status, 0, run.command.stderr);
  assert.ok(run.report);
  assert.match(run.report.status, /explain|plan/i);
  assert.deepEqual(run.events.map(event => event.operation), ['explain']);
  assert.equal(run.dockerCalls.length, 1);
  assert.equal(run.events[0].hasEscapedAt, true);
  assert.equal(run.events[0].hasRawAt, false);
  assert.ok(JSON.stringify(run.report).includes(sha256(run.queryBytes)));
  assert.ok(JSON.stringify(run.report).includes(sha256(run.bindBytes)));
  assert.ok(JSON.stringify(run.report).includes(run.queryPath));
  assert.ok(keysNamed(run.report, 'candidateIndexSelected').includes(true));
  assert.ok(keysNamed(run.report, 'code').includes(321));
  assert.equal(run.report.parser.readOnlyCollectionAccess, true);
  assert.equal(run.report.response.message, null);
  assertNoSensitiveValues(run);
});

test('execute runs one bounded query only after a safe EXPLAIN and reports scan/memory stats', async (t) => {
  const run = await runProbe(t, { args: ['--execute', '--expected-index', indexName] });
  assert.equal(run.command.status, 0, run.command.stderr);
  assert.deepEqual(run.events.map(event => event.operation), ['explain', 'execute']);
  assert.equal(keysNamed(run.report, 'scannedIndex')[0], 11);
  assert.equal(keysNamed(run.report, 'scannedFull')[0], 0);
  assert.equal(keysNamed(run.report, 'peakMemoryUsage')[0], 123456);
  assert.ok(Object.entries(run.report.timing ?? {}).some(([key, value]) => /HostElapsedMs$/.test(key) && Number.isFinite(value)));
  assert.ok(Object.keys(run.report.timing ?? {}).some(key => /server|arang/i.test(key)));
  assertNoSensitiveValues(run);
});

test('safe-integer-count mode accepts only one bounded object with approved non-negative integer fields', async (t) => {
  const run = await runProbe(t, {
    args: ['--execute', '--safe-integer-counts'],
    executeScenario: 'safe-integer-counts',
  });
  assert.equal(run.command.status, 0, run.command.stderr);
  assert.equal(run.report.execution.resultCount, 1);
  assert.deepEqual(run.report.execution.safeIntegerCounts, { reachable_patients: 81, scoped_roots: 742505 });
  assertNoSensitiveValues(run);

  const failed = await runProbe(t, {
    args: ['--execute', '--safe-integer-counts'],
    executeScenario: 'error',
  });
  assert.notEqual(failed.command.status, 0);
  assert.equal(failed.report.failure.stage, 'execute');
  assert.equal(failed.report.response.errorNum, 32);
  assertNoSensitiveValues(failed);

  const malformed = await runProbe(t, {
    args: ['--execute', '--safe-integer-counts'],
    executeScenario: 'malformed-envelope',
  });
  assert.notEqual(malformed.command.status, 0);
  assert.equal(malformed.report.failure.stage, 'execute');
  assert.equal(malformed.report.response.errorNum, 32);
  assert.equal(malformed.report.response.errorCode, 400);
  assert.equal(malformed.report.response.message, 'Arango reported a query error.');
  assert.equal(malformed.report.execution.safeIntegerCounts, undefined);
  assert.equal(JSON.stringify(malformed.report).includes('rows'), false);
  assertNoSensitiveValues(malformed);

  for (const [executeScenario, expectedResultCount] of [['safe-counts-zero', 0], ['safe-counts-many', 2]]) {
    const invalidCount = await runProbe(t, {
      args: ['--execute', '--safe-integer-counts'],
      executeScenario,
    });
    assert.notEqual(invalidCount.command.status, 0);
    assert.equal(invalidCount.report.failure.stage, 'safe-integer-result');
    assert.equal(invalidCount.report.execution.resultCount, expectedResultCount);
    assert.equal(invalidCount.report.execution.safeIntegerCounts, undefined);
    assertNoSensitiveValues(invalidCount);
  }

  const withoutExecute = await runProbe(t, { args: ['--safe-integer-counts'] });
  assert.notEqual(withoutExecute.command.status, 0);
  assert.equal(withoutExecute.report.failure.stage, 'arguments');
  assert.equal(withoutExecute.dockerCalls.length, 0);
  assertNoSensitiveValues(withoutExecute);

  const script = buildExecuteRequestScript({
    query: 'RETURN {scoped_roots: 2}',
    bindVars: unrestrictedBinds,
    maxRuntimeSeconds: 8,
    memoryLimitBytes: 268435456,
    safeIntegerCounts: true,
  });
  const executeRows = rows => {
    const printed = [];
    let offset = 0;
    vm.runInNewContext(script, {
      Date,
      JSON,
      Number,
      Object,
      Array,
      print: value => printed.push(value),
      db: {
        _query: () => ({
          hasNext: () => offset < rows.length,
          next: () => rows[offset++],
          getExtra: () => ({ stats: null }),
        }),
      },
    });
    return JSON.parse(printed[0].slice(printed[0].indexOf('__LOOM_OWNED_QUERY_PROBE__') + '__LOOM_OWNED_QUERY_PROBE__'.length));
  };
  assert.deepEqual(executeRows([{ scoped_roots: 2 }]).safeIntegerCounts, { scoped_roots: 2 });
  assert.equal(executeRows([]).ok, false, 'zero result rows must fail safe-count mode');
  assert.equal(executeRows([{ scoped_roots: 2 }, { scoped_roots: 3 }]).ok, false, 'multiple result rows must fail safe-count mode');
  for (const row of [
    { scoped_roots: '2' },
    { scoped_roots: -1 },
    { scoped_roots: Number.NaN },
    { scoped_roots: Number.MAX_SAFE_INTEGER + 1 },
    { unexpected: sensitiveCategory },
  ]) {
    const envelope = executeRows([row]);
    assert.equal(envelope.ok, false);
    assert.equal(envelope.safeIntegerCounts, undefined);
    assert.equal(Object.hasOwn(envelope, 'rows'), false);
  }
  assertNoSensitiveValues({ command: { stdout: '', stderr: '' }, report: executeRows([{ unexpected: sensitiveCategory }]) });
});

test('expected-index mismatch and write plans both prevent execution', async (t) => {
  const mismatch = await runProbe(t, { args: ['--execute', '--expected-index', indexName], explainScenario: 'mismatch' });
  assert.notEqual(mismatch.command.status, 0);
  assert.deepEqual(mismatch.events.map(event => event.operation), ['explain']);
  assert.match(mismatch.report.status, /index|explain/i);

  const write = await runProbe(t, { args: ['--execute'], explainScenario: 'write' });
  assert.notEqual(write.command.status, 0);
  assert.deepEqual(write.events.map(event => event.operation), ['explain']);
  assert.match(write.report.failure?.stage ?? '', /write|unsafe|read.?only/i);
  assert.equal(write.report.parser.readOnlyCollectionAccess, true);
  assert.equal(write.report.plan.readOnly, false);
});

test('wrong project and inconsistent unrestricted auth scope fail before Docker inspection', async (t) => {
  const wrongProject = await runProbe(t, {
    bindVars: { ...unrestrictedBinds, project: 'another-project' },
    args: ['--execute'],
  });
  assert.notEqual(wrongProject.command.status, 0);
  assert.ok(wrongProject.report);
  assert.match(wrongProject.report.status, /scope|invalid|failed/i);
  assert.equal(wrongProject.dockerCalls.length, 0);
  assertNoSensitiveValues(wrongProject);

  const wrongGeneration = await runProbe(t, {
    bindVars: { ...unrestrictedBinds, dataset_generation: 'another-generation' },
    args: ['--execute'],
  });
  assert.notEqual(wrongGeneration.command.status, 0);
  assert.equal(wrongGeneration.dockerCalls.length, 0);
  assertNoSensitiveValues(wrongGeneration);

  const badAuth = await runProbe(t, {
    bindVars: { ...unrestrictedBinds, auth_resource_paths: [sensitiveResourcePath] },
    args: ['--execute'],
  });
  assert.notEqual(badAuth.command.status, 0);
  assert.equal(badAuth.dockerCalls.length, 0);
  assertNoSensitiveValues(badAuth);
});

test('restricted auth scope remains valid without echoing its path value', async (t) => {
  const run = await runProbe(t, {
    bindVars: { ...unrestrictedBinds, auth_resource_paths_unrestricted: false, auth_resource_paths: [sensitiveResourcePath] },
  });
  assert.equal(run.command.status, 0, run.command.stderr);
  assert.deepEqual(run.events.map(event => event.operation), ['explain']);
  assertNoSensitiveValues(run);
  assert.equal(run.report.bindScope.authorization, 'restricted');
});

test('generation alias and empty unrestricted path array keep their original bind shape', async (t) => {
  const query = queryText.replaceAll('@dataset_generation', '@generation');
  const bindVars = { ...unrestrictedBinds, generation: unrestrictedBinds.dataset_generation };
  delete bindVars.dataset_generation;
  bindVars.auth_resource_paths = [];
  const run = await runProbe(t, { query, bindVars });
  assert.equal(run.command.status, 0, run.command.stderr);
  assert.deepEqual(run.events.map(event => event.operation), ['explain']);
  assert.equal(JSON.stringify(run.report).includes('generationBindName'), true);
  assertNoSensitiveValues(run);
});

test('runtime and memory ceilings reject values above the fixed limits', async (t) => {
  const tooLong = await runProbe(t, { args: ['--max-runtime-seconds', '8.1'] });
  assert.notEqual(tooLong.command.status, 0);
  assert.ok(tooLong.report);
  assert.equal(tooLong.dockerCalls.length, 0);
  assert.match(tooLong.report.status, /invalid|limit|runtime|failed/i);

  const tooMuchMemory = await runProbe(t, { args: ['--memory-limit-bytes', '268435457'] });
  assert.notEqual(tooMuchMemory.command.status, 0);
  assert.ok(tooMuchMemory.report);
  assert.equal(tooMuchMemory.dockerCalls.length, 0);
  assert.match(tooMuchMemory.report.status, /invalid|limit|memory|failed/i);
});

test('Docker inspect I/O failure still writes a structured sanitized report', async (t) => {
  const run = await runProbe(t, { dockerFail: true });
  assert.notEqual(run.command.status, 0);
  assert.ok(run.report);
  assert.match(run.report.status, /docker|inspect|failed/i);
  assert.equal(run.events.length, 0);
  assertNoSensitiveValues(run);
});

test('missing execution statistics are represented as unavailable', async (t) => {
  const run = await runProbe(t, { args: ['--execute'], executeScenario: 'no-stats' });
  assert.equal(run.command.status, 0, run.command.stderr);
  assert.deepEqual(run.events.map(event => event.operation), ['explain', 'execute']);
  const peakMemory = keysNamed(run.report, 'peakMemoryUsage');
  assert.ok(peakMemory.length > 0);
  assert.ok(peakMemory[0] === null || peakMemory[0] === 'unavailable');
  assertNoSensitiveValues(run);
});

test('query errors retain numeric diagnostics and expose only the safe message', async (t) => {
  const run = await runProbe(t, { args: ['--execute'], executeScenario: 'error' });
  assert.notEqual(run.command.status, 0);
  assert.deepEqual(run.events.map(event => event.operation), ['explain', 'execute']);
  assert.equal(run.report.response.errorNum, 32);
  assert.equal(run.report.response.errorCode, 400);
  assert.equal(run.report.response.message, 'Arango reported a query error.');
  assertNoSensitiveValues(run);
});

test('a fresh output guard never overwrites an existing report', async (t) => {
  const run = await runProbe(t, { existingOutput: true });
  assert.notEqual(run.command.status, 0);
  assert.equal(run.dockerCalls.length, 0);
  assert.equal(run.events.length, 0);
  assert.equal(await readFile(run.outputPath, 'utf8'), 'preserve this report\n');
  assertNoSensitiveValues(run);
});

test('host timeout becomes a structured bounded-query failure', async (t) => {
  const run = await runProbe(t, {
    args: ['--execute', '--max-runtime-seconds', '0.1'],
    hangOperation: 'execute',
  });
  assert.notEqual(run.command.status, 0);
  assert.ok(run.report);
  assert.match(run.report.status, /timeout|failed/i);
  assert.deepEqual(run.events.map(event => event.operation), ['explain', 'execute']);
  assertNoSensitiveValues(run);
});

test('generated JavaScript restores every escaped bind marker and the exact null/resource bind payload', () => {
  const query = `LET p = @project LET g = @generation LET u = @auth_resource_paths_unrestricted LET paths = @auth_resource_paths RETURN { raw: @category, root: @@root_collection, terminal: @@__loom_related_category_target_collection, at: "a@b" }`;
  const bindVars = {
    '@root_collection': 'Specimen',
    '@__loom_related_category_target_collection': 'Observation',
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
    category: 'value@with-at',
    auth_resource_paths_unrestricted: true,
    auth_resource_paths: null,
  };
  const explainScript = buildExplainRequestScript({ query, bindVars, database: 'loom_dev' });
  assert(!explainScript.includes('@'));
  let capturedRequest;
  const printed = [];
  let requestCount = 0;
  vm.runInNewContext(explainScript, {
    Date,
    JSON,
    print: value => printed.push(value),
    require: name => {
      assert.equal(name, '@arangodb/request');
      return options => {
        requestCount += 1;
        if (options.url.endsWith('/_api/query')) {
          return {
            statusCode: 200,
            body: JSON.stringify({
              parsed: true,
              bindVars: ['project', 'generation', 'auth_resource_paths_unrestricted', 'auth_resource_paths'],
              collections: [{ name: 'Specimen', type: 'read' }],
            }),
          };
        }
        capturedRequest = options;
        return {
          statusCode: 200,
          body: JSON.stringify({
            plan: { estimatedCost: 1, estimatedNrItems: 1, nodes: [{ id: 1, type: 'IndexNode', collection: 'Observation', indexes: [] }] },
            warnings: [],
          }),
        };
      };
    },
  });
  assert.ok(capturedRequest);
  assert.deepEqual(JSON.parse(capturedRequest.body), { query, bindVars });
  assert.equal(requestCount, 2);
  assert.equal(printed.length, 1);
  const explainEnvelope = JSON.parse(printed[0].slice(printed[0].indexOf('__LOOM_OWNED_QUERY_PROBE__') + '__LOOM_OWNED_QUERY_PROBE__'.length));
  assert.equal(explainEnvelope.parserReadOnly, true);
  assert.equal(explainEnvelope.message, null);

  const executeScript = buildExecuteRequestScript({
    query,
    bindVars,
    maxRuntimeSeconds: 8,
    memoryLimitBytes: 268435456,
  });
  assert(!executeScript.includes('@'));
  let capturedQuery;
  let capturedBinds;
  const executePrinted = [];
  vm.runInNewContext(executeScript, {
    Date,
    JSON,
    print: value => executePrinted.push(value),
    db: {
      _query(actualQuery, actualBinds, options) {
        capturedQuery = actualQuery;
        capturedBinds = actualBinds;
        assert.equal(options.maxRuntime, 8);
        assert.equal(options.memoryLimit, 268435456);
        return {
          hasNext: () => false,
          next: () => null,
          getExtra: () => ({ stats: null }),
        };
      },
    },
  });
  assert.equal(capturedQuery, query);
  assert.deepEqual(JSON.parse(JSON.stringify(capturedBinds)), bindVars);
  assert.equal(executePrinted.length, 1);

  let parseCalls = 0;
  const writePrinted = [];
  vm.runInNewContext(buildExplainRequestScript({ query: 'INSERT { value: @project } INTO Observation', bindVars }), {
    Date,
    JSON,
    print: value => writePrinted.push(value),
    require: () => options => {
      parseCalls += 1;
      assert.ok(options.url.endsWith('/_api/query'));
      return {
        statusCode: 200,
        body: JSON.stringify({
          parsed: true,
          bindVars: ['project', 'generation', 'auth_resource_paths_unrestricted', 'auth_resource_paths'],
          collections: [{ name: 'Observation', type: 'write' }],
        }),
      };
    },
  });
  assert.equal(parseCalls, 1, 'a write parser result must stop before EXPLAIN');
  const writeEnvelope = JSON.parse(writePrinted[0].slice(writePrinted[0].indexOf('__LOOM_OWNED_QUERY_PROBE__') + '__LOOM_OWNED_QUERY_PROBE__'.length));
  assert.equal(writeEnvelope.stage, 'parse-write-scope-failed');
  assert.equal(writeEnvelope.parserReadOnly, false);

  const parserErrorPrinted = [];
  vm.runInNewContext(buildExplainRequestScript({ query, bindVars }), {
    Date,
    JSON,
    print: value => parserErrorPrinted.push(value),
    require: () => () => ({
      statusCode: 400,
      body: JSON.stringify({ error: true, errorNum: 1501, errorMessage: 'PRIVATE_QUERY_ERROR_DETAIL' }),
    }),
  });
  const parserErrorEnvelope = JSON.parse(parserErrorPrinted[0].slice(parserErrorPrinted[0].indexOf('__LOOM_OWNED_QUERY_PROBE__') + '__LOOM_OWNED_QUERY_PROBE__'.length));
  assert.equal(parserErrorEnvelope.stage, 'parse-failed');
  assert.equal(parserErrorEnvelope.errorNum, 1501);
  assert.equal(parserErrorEnvelope.errorCode, 400);
  assert.equal(parserErrorEnvelope.message, 'Arango reported a query error.');
});
