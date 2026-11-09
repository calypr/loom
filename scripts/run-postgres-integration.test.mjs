import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  DATASET_INTEGRATION_PACKAGE,
  DATASET_INTEGRATION_TESTS,
  EXPLORER_INTEGRATION_PACKAGE,
  EXPLORER_INTEGRATION_TESTS,
  EXPECTED_CHECKS,
  NAMESPACE_PREFIX,
  PROBE_SOURCE,
  REQUIRED_INTEGRATION_TEST,
  REQUIRED_MAJOR_VERSION,
  checkGoEvents,
  extractSecrets,
  inspectComposeContract,
  makeRunId,
  ownedDatabaseName,
  parseAdminUrl,
  runPostgres,
  scrubSecrets,
  selectComposeRuntime,
} from './run-postgres-integration.mjs';

const ADMIN_URL = 'postgresql://loom_test:s3cret-test-password@127.0.0.1:55432/loom_test?sslmode=disable';
const events = list => list.map(event => JSON.stringify(event)).join('\n');
const passingGo = (args = []) => ({
  status: 0,
  stdout: events(args.at(-1) === DATASET_INTEGRATION_PACKAGE
    ? [
      ...DATASET_INTEGRATION_TESTS.flatMap(name => [
        { Action: 'run', Package: 'datasetpkg', Test: name },
        { Action: 'pass', Package: 'datasetpkg', Test: name },
      ]),
      { Action: 'pass', Package: 'datasetpkg' },
    ]
    : args.at(-1) === EXPLORER_INTEGRATION_PACKAGE
      ? [
        ...EXPLORER_INTEGRATION_TESTS.flatMap(name => [
          { Action: 'run', Package: 'explorerpkg', Test: name },
          { Action: 'pass', Package: 'explorerpkg', Test: name },
        ]),
        { Action: 'pass', Package: 'explorerpkg' },
      ]
      : [
      { Action: 'run', Package: 'pkg', Test: REQUIRED_INTEGRATION_TEST },
      { Action: 'output', Package: 'pkg', Test: REQUIRED_INTEGRATION_TEST, Output: 'ok' },
      { Action: 'pass', Package: 'pkg', Test: REQUIRED_INTEGRATION_TEST },
      { Action: 'pass', Package: 'pkg' },
    ]),
  stderr: '',
});

function harness({ probe, go, composeProbe, env = { LOOM_TEST_POSTGRES_URL: ADMIN_URL } } = {}) {
  const calls = [];
  const defaultProbe = async (command, args) => {
    calls.push({ command, args });
    switch (command) {
      case 'ping': return { ok: true, op: 'ping', versionNum: 180006, version: '18.6', major: 18 };
      case 'create': return { ok: true, op: 'create', database: args[0] };
      case 'acceptance': return { ok: true, op: 'acceptance', database: args[0], token: args[1], rows: 3, checked: [...EXPECTED_CHECKS] };
      case 'drop': return { ok: true, op: 'drop', database: args[0] };
      case 'list': return { ok: true, op: 'list', prefix: args[0], databases: [] };
      default: throw new Error(`unexpected probe command ${command}`);
    }
  };
  const goCalls = [];
  const goTest = (args, childEnv) => {
    goCalls.push({ args, childEnv });
    return go ? go(args, childEnv) : passingGo(args);
  };
  const composeCalls = [];
  const defaultComposeProbe = (command, args) => {
    composeCalls.push({ command, args });
    if (command === 'docker' && args.join(' ') === 'compose version') return { status: 0, stdout: '', stderr: '' };
    if (command === 'docker' && args.includes('config')) {
      const services = args.includes('--profile')
        ? ['arangodb', 'clickhouse', 'postgres18-test']
        : ['arangodb', 'clickhouse'];
      return { status: 0, stdout: `${services.join('\n')}\n`, stderr: '' };
    }
    return { status: 1, stdout: '', stderr: 'unavailable' };
  };
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const run = () => runPostgres(out, {
    env,
    probe: probe ?? defaultProbe,
    goTest,
    composeProbe: composeProbe ?? defaultComposeProbe,
    runId: '173000000000000042_0123456789ab',
  });
  return { calls, goCalls, composeCalls, out, run };
}

test('parseAdminUrl accepts an owned postgresql service and rejects substitutes', () => {
  const admin = parseAdminUrl(ADMIN_URL);
  assert.deepEqual({ host: admin.host, port: admin.port, username: admin.username, database: admin.database },
    { host: '127.0.0.1', port: '55432', username: 'loom_test', database: 'loom_test' });
  assert.ok(parseAdminUrl('postgres://u:pwd4test@h:1/db').database === 'db');
  assert.throws(() => parseAdminUrl(''), /must name an owned PostgreSQL 18 test service/);
  assert.throws(() => parseAdminUrl(undefined), /must name an owned PostgreSQL 18 test service/);
  assert.throws(() => parseAdminUrl('http://127.0.0.1:5432/db'), /postgresql:\/\/ scheme/);
  assert.throws(() => parseAdminUrl('not a url at all'), /postgresql:\/\/ connection URL/);
  assert.throws(() => parseAdminUrl('postgresql://u:pwd4test@h/db?x=1#frag'.replace('pwd4test', '')), /test password/);
  assert.throws(() => parseAdminUrl('postgresql://u:pwd4test@h/'), /maintenance database/);
  // pgconn lets a query dbname outrank the URL path; the runner must refuse it.
  assert.throws(() => parseAdminUrl('postgresql://u:pwd4test@h:5432/db?sslmode=disable&dbname=other'), /must not pin a database in the query string/);
  assert.throws(() => parseAdminUrl('postgresql://u:pwd4test@h/db?database=other'), /must not pin a database in the query string/);
  assert.ok(parseAdminUrl('postgresql://u:pwd4test@h:5432/db?sslmode=disable').database === 'db');
});

test('the owned namespace pattern is enforced before any probe sees a name', () => {
  assert.equal(ownedDatabaseName('173000000000000042_0123456789ab'), 'pgfixture_173000000000000042_0123456789ab');
  assert.throws(() => ownedDatabaseName('shared_production'), /outside the owned namespace pattern/);
  assert.throws(() => ownedDatabaseName('173000000000000042_0123456789a'), /outside the owned namespace pattern/);
  assert.throws(() => ownedDatabaseName('../evil'), /outside the owned namespace pattern/);
  const ids = new Set(Array.from({ length: 50 }, () => makeRunId()));
  assert.equal(ids.size, 50, 'run ids must be unique');
  for (const id of ids) assert.match(ownedDatabaseName(id), /^pgfixture_[0-9]+_[0-9a-f]{12}$/);
});

test('scrubSecrets redacts credential material but never the world', () => {
  const secrets = extractSecrets(ADMIN_URL);
  assert.ok(scrubSecrets(`could not connect using ${ADMIN_URL}`, secrets).includes('[redacted]'));
  assert.ok(!scrubSecrets(`could not connect using ${ADMIN_URL}`, secrets).includes('s3cret-test-password'));
  assert.ok(!scrubSecrets('loom_test:s3cret-test-password@127.0.0.1', secrets).includes('s3cret-test-password@'));
  assert.equal(scrubSecrets('ab is too short to redact', ['ab']), 'ab is too short to redact');
  assert.ok(extractSecrets(ADMIN_URL).includes('s3cret-test-password'));
});

test('the required Go case must run and pass; skips are never acceptance', () => {
  const good = [{ Action: 'pass', Package: 'pkg' }, { Action: 'run', Test: REQUIRED_INTEGRATION_TEST }, { Action: 'pass', Test: REQUIRED_INTEGRATION_TEST }];
  assert.deepEqual(checkGoEvents(events(good), REQUIRED_INTEGRATION_TEST).passes, [REQUIRED_INTEGRATION_TEST]);
  assert.throws(() => checkGoEvents(events(good.filter(e => e.Action !== 'run')), REQUIRED_INTEGRATION_TEST), /did not run/);
  assert.throws(() => checkGoEvents(events(good.filter(e => !(e.Test && e.Action === 'pass'))), REQUIRED_INTEGRATION_TEST), /did not pass|No test assertions/);
  assert.throws(() => checkGoEvents(events([{ Action: 'pass', Package: 'pkg' }, { Action: 'skip', Test: REQUIRED_INTEGRATION_TEST }]), REQUIRED_INTEGRATION_TEST), /was skipped, a blocker and never acceptance/);
  assert.throws(() => checkGoEvents(events(good.concat([{ Action: 'skip', Test: 'TestUnrelated' }])), REQUIRED_INTEGRATION_TEST), /Unexpected skipped case/);
  assert.throws(() => checkGoEvents(events([{ Action: 'fail', Package: 'pkg' }]), REQUIRED_INTEGRATION_TEST), /Go reported failure/);
  assert.throws(() => checkGoEvents(events([{ Action: 'run', Test: REQUIRED_INTEGRATION_TEST }, { Action: 'pass', Test: REQUIRED_INTEGRATION_TEST }]), REQUIRED_INTEGRATION_TEST), /No successful package completion/);
});

test('selectComposeRuntime prefers docker compose, supports podman, never assumes a socket', () => {
  const makeProbe = available => (command, args) =>
    ({ status: available.includes(`${command} ${args.join(' ')}`) ? 0 : 1, stdout: '', stderr: '' });
  assert.deepEqual(selectComposeRuntime(makeProbe(['docker compose version'])), ['docker', 'compose']);
  assert.deepEqual(selectComposeRuntime(makeProbe(['podman compose version'])), ['podman', 'compose']);
  assert.deepEqual(selectComposeRuntime(makeProbe(['podman-compose version'])), ['podman-compose']);
  assert.equal(selectComposeRuntime(makeProbe([])), null);
});

test('the compose contract keeps the default stack Arango + ClickHouse and PG behind a profile', () => {
  const output = services => ({ status: 0, stdout: `${services.join('\n')}\n`, stderr: '' });
  const probe = (command, args) => {
    if (command === 'docker' && args.join(' ') === 'compose version') return { status: 0, stdout: '', stderr: '' };
    if (args.includes('--profile')) return output(['arangodb', 'clickhouse', 'postgres18-test']);
    if (args.includes('mysql')) return output(['arangodb', 'clickhouse', 'mysql']);
    return output(['arangodb', 'clickhouse']);
  };
  const verified = inspectComposeContract(probe);
  assert.equal(verified.status, 'verified');
  assert.deepEqual(verified.defaultServices, ['arangodb', 'clickhouse']);
  assert.ok(verified.profileServices.includes('postgres18-test'));
  assert.throws(() => inspectComposeContract((command, args) =>
    command === 'docker' && args.join(' ') === 'compose version'
      ? { status: 0, stdout: '', stderr: '' }
      : args.includes('--profile') ? output(['arangodb', 'clickhouse']) : output(['arangodb', 'clickhouse'])),
  /postgres18-test service is missing/);
  assert.throws(() => inspectComposeContract((command, args) =>
    command === 'docker' && args.join(' ') === 'compose version'
      ? { status: 0, stdout: '', stderr: '' }
      : output(args.includes('--profile') ? ['arangodb', 'clickhouse', 'mysql', 'postgres18-test'] : ['arangodb', 'clickhouse', 'mysql'])),
  /must stay Arango \+ ClickHouse/);
  assert.throws(() => inspectComposeContract(() => ({ status: 1, stdout: '', stderr: 'missing' })),
    err => err.code === 'EComposeUnavailable');
});

test('the embedded probe keeps its credential and namespace guards', () => {
  assert.ok(PROBE_SOURCE.includes('pgfixture_'), 'probe must enforce the owned namespace');
  assert.ok(PROBE_SOURCE.includes('LOOM_PG_FIXTURE_ADMIN_URL'), 'DSN must come from the environment only');
  assert.ok(!PROBE_SOURCE.includes('s3cret-test-password'));
  assert.ok(PROBE_SOURCE.includes('WITH (FORCE)'), 'cleanup must be able to finish against idle connections');
  assert.ok(PROBE_SOURCE.includes('23505') && PROBE_SOURCE.includes('22P02'), 'negative checks must classify SQLSTATEs');
  // The version constant is interpolated from the runner, so runner, probe and
  // the Go package mirror one value.
  assert.ok(PROBE_SOURCE.includes(`const requiredMajor = ${REQUIRED_MAJOR_VERSION}`), 'probe version constant must be interpolated');
  // The effective-target guard must run before any fixture write.
  assert.ok(PROBE_SOURCE.includes('current_database()'), 'probe must prove the effective target database');
  assert.ok(PROBE_SOURCE.indexOf('current_database()') < PROBE_SOURCE.indexOf('CREATE TABLE loom_fixture_rows'), 'target guard must precede writes');
  assert.ok(PROBE_SOURCE.includes("ESCAPE 'x'"), 'leftover audit must escape LIKE wildcards');
  assert.equal(NAMESPACE_PREFIX, 'pgfixture_');
});

test('a cleanup or audit failure never overwrites the primary failure message', async () => {
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const evidence = await runPostgres(out, {
    env: { LOOM_TEST_POSTGRES_URL: ADMIN_URL },
    runId: '173000000000000042_0123456789ab',
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async (command, args) => {
      if (command === 'ping') return { ok: true, major: 18, versionNum: 180006, version: '18.6' };
      if (command === 'create') return { ok: true, database: args[0] };
      if (command === 'acceptance') return { ok: false, op: 'acceptance', error: 'literal row mismatch' };
      if (command === 'drop') throw new Error('drop transport failed');
      if (command === 'list') return { ok: true, databases: ['pgfixture_173000000000000042_0123456789ab'] };
      throw new Error(`unexpected ${command}`);
    },
  });
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /literal row mismatch/, 'the primary failure must survive');
  assert.match(evidence.error, /cleanup: drop transport failed/);
  assert.match(evidence.error, /leftover audit: Owned namespace still present/);
  assert.equal(evidence.cleanup.length, 1);
});

test('a full owned-namespace run passes with finally-cleanup of only its namespace', async () => {
  const { calls, goCalls, out, run } = harness();
  const evidence = await run();
  assert.equal(evidence.status, 'passed');
  assert.deepEqual(calls.map(c => c.command), ['ping', 'create', 'acceptance', 'drop', 'list']);
  assert.equal(calls[1].args[0], evidence.namespace.database);
  assert.ok(calls[1].args[0].startsWith('pgfixture_'));
  assert.deepEqual(evidence.cleanup, [{ database: evidence.namespace.database, status: 'dropped' }]);
  assert.deepEqual(evidence.leftovers, []);
  assert.equal(calls[4].args[0], evidence.namespace.database, 'audit must cover only this run namespace');
  assert.equal(goCalls.length, 3, 'the pg-store-01, dataset, and explorer lanes must all run');
  assert.equal(goCalls[0].childEnv.LOOM_TEST_POSTGRES_URL, ADMIN_URL, 'URL must be propagated to the child');
  assert.ok(goCalls[0].args.includes(`^${REQUIRED_INTEGRATION_TEST}$`));
  assert.ok(goCalls[0].args.includes('./internal/store/postgres'));
  assert.ok(goCalls[0].args.some(a => /^-timeout=\d+s$/.test(a) && Number(a.slice(9, -1)) <= 180),
    'the Go test timeout must be bounded at or below the aligned 180s budget');
  assert.deepEqual(evidence.integration.passes, [REQUIRED_INTEGRATION_TEST]);
  assert.deepEqual(evidence.phases, ['readiness', 'namespace', 'acceptance', 'integration', 'dataset-integration', 'explorer-integration']);
  // The dataset lane runs whole-package: no -run filter, same propagated URL.
  assert.equal(goCalls[1].childEnv.LOOM_TEST_POSTGRES_URL, ADMIN_URL);
  assert.equal(goCalls[1].args.at(-1), DATASET_INTEGRATION_PACKAGE);
  assert.ok(!goCalls[1].args.includes('-run'), 'whole-package execution is the never-skipped guarantee');
  assert.ok(goCalls[1].args.includes('-json') && goCalls[1].args.includes('-count=1'));
  assert.deepEqual(evidence.datasetIntegration.anchors, [...DATASET_INTEGRATION_TESTS]);
  for (const name of DATASET_INTEGRATION_TESTS) assert.ok(evidence.datasetIntegration.passes.includes(name));
  // The explorer lane runs whole-package too: no -run filter, same URL.
  assert.equal(goCalls[2].childEnv.LOOM_TEST_POSTGRES_URL, ADMIN_URL);
  assert.equal(goCalls[2].args.at(-1), EXPLORER_INTEGRATION_PACKAGE);
  assert.ok(!goCalls[2].args.includes('-run'), 'whole-package execution is the never-skipped guarantee');
  assert.ok(goCalls[2].args.includes('-json') && goCalls[2].args.includes('-count=1'));
  assert.deepEqual(evidence.explorerIntegration.anchors, [...EXPLORER_INTEGRATION_TESTS]);
  for (const name of EXPLORER_INTEGRATION_TESTS) assert.ok(evidence.explorerIntegration.passes.includes(name));
  const summary = readFileSync(join(out, 'summary.json'), 'utf8');
  assert.ok(!summary.includes('s3cret-test-password'), 'evidence must be credential-free');
  assert.equal(JSON.parse(summary).status, 'passed');
  assert.ok(readFileSync(join(out, 'integration.jsonl'), 'utf8').includes(REQUIRED_INTEGRATION_TEST));
  assert.ok(readFileSync(join(out, 'dataset-integration.jsonl'), 'utf8').includes(DATASET_INTEGRATION_TESTS[0]));
  assert.ok(readFileSync(join(out, 'explorer-integration.jsonl'), 'utf8').includes(EXPLORER_INTEGRATION_TESTS[0]));
});

test('a failing dataset lane fails the run and its evidence is still scrubbed', async () => {
  const { goCalls, out, run } = harness({
    go: args => args.at(-1) === DATASET_INTEGRATION_PACKAGE
      ? { status: 1, stdout: events([{ Action: 'fail', Package: 'datasetpkg' }]), stderr: `connection refused for ${ADMIN_URL}` }
      : passingGo(args),
  });
  const evidence = await run();
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /internal\/dataset\/postgres real-PG suite failed/);
  assert.equal(goCalls.length, 2, 'the dataset lane must actually be attempted after pg-store-01 passes');
  assert.ok(!('datasetIntegration' in evidence), 'a failed lane records no pass evidence');
  assert.deepEqual(evidence.cleanup, [{ database: evidence.namespace.database, status: 'dropped' }], 'cleanup must still own its namespace');
  const stderr = readFileSync(join(out, 'dataset-integration.stderr'), 'utf8');
  assert.ok(!stderr.includes('s3cret-test-password'), 'dataset lane evidence must be credential-free');
  assert.ok(stderr.includes('[redacted]'), 'dataset lane evidence must record the redaction');
});

test('a dataset lane that skips any case is a blocker, never acceptance', async () => {
  const { out, run } = harness({
    go: args => args.at(-1) === DATASET_INTEGRATION_PACKAGE
      ? {
        status: 0,
        stdout: events([
          { Action: 'run', Package: 'datasetpkg', Test: DATASET_INTEGRATION_TESTS[0] },
          { Action: 'skip', Package: 'datasetpkg', Test: DATASET_INTEGRATION_TESTS[0] },
          { Action: 'pass', Package: 'datasetpkg' },
        ]),
        stderr: '',
      }
      : passingGo(args),
  });
  const evidence = await run();
  assert.equal(evidence.status, 'failed', 'a skipped dataset case cannot masquerade as real-PG acceptance');
  assert.match(evidence.error, new RegExp(DATASET_INTEGRATION_TESTS[0]));
});

test('a dataset lane that never runs an anchor case fails the run', async () => {
  const { out, run } = harness({
    go: args => args.at(-1) === DATASET_INTEGRATION_PACKAGE
      ? { status: 0, stdout: events([{ Action: 'pass', Package: 'datasetpkg' }]), stderr: '' }
      : passingGo(args),
  });
  const evidence = await run();
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /did not run/);
});

test('a failing explorer lane fails the run and its evidence is still scrubbed', async () => {
  const { goCalls, out, run } = harness({
    go: args => args.at(-1) === EXPLORER_INTEGRATION_PACKAGE
      ? { status: 1, stdout: events([{ Action: 'fail', Package: 'explorerpkg' }]), stderr: `connection refused for ${ADMIN_URL}` }
      : passingGo(args),
  });
  const evidence = await run();
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /internal\/explorer\/postgres real-PG suite failed/);
  assert.equal(goCalls.length, 3, 'the explorer lane must actually be attempted after the dataset lane passes');
  assert.ok(!('explorerIntegration' in evidence), 'a failed lane records no pass evidence');
  assert.deepEqual(evidence.cleanup, [{ database: evidence.namespace.database, status: 'dropped' }], 'cleanup must still own its namespace');
  const stderr = readFileSync(join(out, 'explorer-integration.stderr'), 'utf8');
  assert.ok(!stderr.includes('s3cret-test-password'), 'explorer lane evidence must be credential-free');
  assert.ok(stderr.includes('[redacted]'), 'explorer lane evidence must record the redaction');
});

test('an explorer lane that skips any case is a blocker, never acceptance', async () => {
  const { out, run } = harness({
    go: args => args.at(-1) === EXPLORER_INTEGRATION_PACKAGE
      ? {
        status: 0,
        stdout: events([
          { Action: 'run', Package: 'explorerpkg', Test: EXPLORER_INTEGRATION_TESTS[0] },
          { Action: 'skip', Package: 'explorerpkg', Test: EXPLORER_INTEGRATION_TESTS[0] },
          { Action: 'pass', Package: 'explorerpkg' },
        ]),
        stderr: '',
      }
      : passingGo(args),
  });
  const evidence = await run();
  assert.equal(evidence.status, 'failed', 'a skipped explorer case cannot masquerade as real-PG acceptance');
  assert.match(evidence.error, new RegExp(EXPLORER_INTEGRATION_TESTS[0]));
});

test('an explorer lane that never runs an anchor case fails the run', async () => {
  const { out, run } = harness({
    go: args => args.at(-1) === EXPLORER_INTEGRATION_PACKAGE
      ? { status: 0, stdout: events([{ Action: 'pass', Package: 'explorerpkg' }]), stderr: '' }
      : passingGo(args),
  });
  const evidence = await run();
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /did not run/);
});

test('a non-18 server fails readiness before any namespace is created', async () => {
  const calls = [];
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const evidence = await runPostgres(out, {
    env: { LOOM_TEST_POSTGRES_URL: ADMIN_URL },
    runId: '173000000000000042_0123456789ab',
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async (command, args) => {
      calls.push({ command, args });
      if (command === 'ping') return { ok: true, op: 'ping', versionNum: 170014, version: '17.4', major: 17 };
      if (command === 'list') return { ok: true, databases: [] };
      throw new Error(`probe must not proceed past readiness: ${command}`);
    },
  });
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /requires PostgreSQL 18/);
  assert.match(evidence.error, /17\.4/);
  assert.deepEqual(calls.map(c => c.command), ['ping', 'list'], 'only the leftover audit may follow a readiness failure');
  assert.deepEqual(evidence.cleanup, []);
});

test('a failed create owns nothing and drops nothing', async () => {
  const calls = [];
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const evidence = await runPostgres(out, {
    env: { LOOM_TEST_POSTGRES_URL: ADMIN_URL },
    runId: '173000000000000042_0123456789ab',
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async (command, args) => {
      calls.push({ command, args });
      if (command === 'ping') return { ok: true, major: 18, versionNum: 180006, version: '18.6' };
      if (command === 'create') return { ok: false, op: 'create', error: 'permission denied to create database' };
      if (command === 'list') return { ok: true, databases: [] };
      throw new Error(`probe must not proceed past create: ${command}`);
    },
    goTest: () => { throw new Error('go test must not start after create failure'); },
  });
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /Refusing unavailable fixture database/);
  assert.deepEqual(evidence.cleanup, [], 'nothing was owned, so nothing may be dropped');
  assert.deepEqual(calls.map(c => c.command), ['ping', 'create', 'list']);
});

test('cleanup runs in finally even when acceptance fails mid-run', async () => {
  const calls = [];
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const evidence = await runPostgres(out, {
    env: { LOOM_TEST_POSTGRES_URL: ADMIN_URL },
    runId: '173000000000000042_0123456789ab',
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async (command, args) => {
      calls.push(command);
      if (command === 'ping') return { ok: true, major: 18, versionNum: 180006, version: '18.6' };
      if (command === 'create') return { ok: true, database: args[0] };
      if (command === 'acceptance') return { ok: false, op: 'acceptance', error: 'literal row mismatch: got []' };
      if (command === 'drop') return { ok: true, database: args[0] };
      if (command === 'list') return { ok: true, databases: [] };
      throw new Error(`unexpected ${command}`);
    },
  });
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /literal row mismatch/);
  assert.deepEqual(calls, ['ping', 'create', 'acceptance', 'drop', 'list']);
  assert.deepEqual(evidence.cleanup, [{ database: evidence.namespace.database, status: 'dropped' }]);
});

test('a skipped required case cannot masquerade as real-PG acceptance', async () => {
  const calls = [];
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const skippedGo = {
    status: 0,
    stdout: events([
      { Action: 'run', Package: 'pkg', Test: REQUIRED_INTEGRATION_TEST },
      { Action: 'output', Package: 'pkg', Test: REQUIRED_INTEGRATION_TEST, Output: 'set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests' },
      { Action: 'skip', Package: 'pkg', Test: REQUIRED_INTEGRATION_TEST },
      { Action: 'pass', Package: 'pkg' },
    ]),
    stderr: '',
  };
  const evidence = await runPostgres(out, {
    env: { LOOM_TEST_POSTGRES_URL: ADMIN_URL },
    runId: '173000000000000042_0123456789ab',
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async (command, args) => {
      calls.push(command);
      if (command === 'ping') return { ok: true, major: 18, versionNum: 180006, version: '18.6' };
      if (command === 'create') return { ok: true, database: args[0] };
      if (command === 'acceptance') return { ok: true, token: args[1], rows: 3, checked: [...EXPECTED_CHECKS] };
      if (command === 'drop') return { ok: true, database: args[0] };
      if (command === 'list') return { ok: true, databases: [] };
      throw new Error(`unexpected ${command}`);
    },
    goTest: () => skippedGo,
  });
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /was skipped, a blocker and never acceptance/, 'a skipped required case is a blocker, not a pass');
  assert.deepEqual(evidence.cleanup, [{ database: evidence.namespace.database, status: 'dropped' }]);
});

test('leftovers in the owned namespace fail the run', async () => {
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  let ownedName2;
  const evidence = await runPostgres(out, {
    env: { LOOM_TEST_POSTGRES_URL: ADMIN_URL },
    runId: '173000000000000042_0123456789ab',
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async (command, args) => {
      if (command === 'ping') return { ok: true, major: 18, versionNum: 180006, version: '18.6' };
      if (command === 'create') { ownedName2 = args[0]; return { ok: true, database: args[0] }; }
      if (command === 'acceptance') return { ok: true, token: args[1], rows: 3, checked: [...EXPECTED_CHECKS] };
      if (command === 'drop') return { ok: true, database: args[0] };
      if (command === 'list') return { ok: true, databases: [ownedName2] };
      throw new Error(`unexpected ${command}`);
    },
  });
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /still present after cleanup/);
  assert.deepEqual(evidence.leftovers, [ownedName2]);
});

test('credentials never reach evidence or summary output on failure paths', async () => {
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const evidence = await runPostgres(out, {
    env: { LOOM_TEST_POSTGRES_URL: ADMIN_URL },
    runId: '173000000000000042_0123456789ab',
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async (command, args) => {
      if (command === 'ping') return { ok: true, major: 18, versionNum: 180006, version: '18.6' };
      if (command === 'create') return { ok: true, database: args[0] };
      if (command === 'acceptance') return { ok: true, token: args[1], rows: 3, checked: [...EXPECTED_CHECKS] };
      if (command === 'drop') return { ok: true, database: args[0] };
      if (command === 'list') return { ok: true, databases: [] };
      // Probe failures leak the DSN; the runner must scrub before recording.
      return { ok: false, op: command, error: `authentication failed for ${ADMIN_URL}` };
    },
    goTest: () => ({ status: 1, stdout: `panic: pool dial failed for ${ADMIN_URL}\n`, stderr: `pool dial failed for ${ADMIN_URL}` }),
  });
  assert.equal(evidence.status, 'failed');
  const dump = JSON.stringify(evidence);
  assert.ok(!dump.includes('s3cret-test-password'), 'evidence must never carry the password');
  assert.ok(!dump.includes(ADMIN_URL), 'evidence must never carry the raw URL');
  const summary = readFileSync(join(out, 'summary.json'), 'utf8');
  assert.ok(!summary.includes('s3cret-test-password'));
  // Every evidence file on disk is scrubbed, including the Go event stream.
  const stdoutEvidence = readFileSync(join(out, 'integration.jsonl'), 'utf8');
  assert.ok(!stdoutEvidence.includes('s3cret-test-password'), 'integration.jsonl must be credential-free');
  assert.ok(stdoutEvidence.includes('[redacted]'), 'integration.jsonl must record the redaction');
  assert.ok(readFileSync(join(out, 'integration.stderr'), 'utf8').includes('[redacted]'));
});

test('a missing compose runtime is recorded as blocked, never faked healthy', async () => {
  const composeCalls = [];
  const { out, run } = harness({
    composeProbe: (command, args) => {
      composeCalls.push({ command, args });
      return { status: 1, stdout: '', stderr: 'command not found' };
    },
  });
  const evidence = await run();
  assert.equal(evidence.status, 'passed');
  assert.equal(evidence.compose.status, 'blocked-missing-runtime');
  assert.match(evidence.compose.note, /blocked/);
  assert.ok(composeCalls.length > 0, 'the runner must actually probe for a runtime');
});

test('a broken compose contract fails closed', async () => {
  const { out, run } = harness({
    composeProbe: (command, args) => {
      if (args.join(' ') === 'compose version') return { status: 0, stdout: '', stderr: '' };
      const services = args.includes('--profile') ? ['arangodb', 'clickhouse', 'mysql', 'postgres18-test'] : ['arangodb', 'clickhouse', 'mysql'];
      return { status: 0, stdout: `${services.join('\n')}\n`, stderr: '' };
    },
  });
  const evidence = await run();
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /must stay Arango \+ ClickHouse/);
  assert.ok(!('readiness' in evidence), 'compose contract failure must stop the run before touching the service');
});

test('an admin URL that pins dbname fails closed before any service contact', async () => {
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const calls = [];
  const evidence = await runPostgres(out, {
    env: { LOOM_TEST_POSTGRES_URL: `${ADMIN_URL}&dbname=production_data` },
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async command => { calls.push(command); return { ok: true, command, databases: [] }; },
    goTest: () => { throw new Error('go test must not start for a rejected URL'); },
  });
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /must not pin a database/);
  assert.deepEqual(calls, ['list'], 'only the read-only leftover audit may run; no mutation, no service writes');
  assert.deepEqual(evidence.cleanup, []);
});

test('a missing LOOM_TEST_POSTGRES_URL is a recorded blocker, never a pass', async () => {
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  const evidence = await runPostgres(out, {
    env: {},
    compose: () => ({ status: 'blocked-missing-runtime' }),
    probe: async command => (command === 'list' ? { ok: true, databases: [] } : { ok: false, error: `unreachable service for ${command}` }),
  });
  assert.equal(evidence.status, 'failed');
  assert.match(evidence.error, /LOOM_TEST_POSTGRES_URL must name an owned PostgreSQL 18 test service/);
  assert.ok(!('readiness' in evidence), 'no phase may run without an owned service');
});

test('the evidence directory must start empty', async () => {
  const out = mkdtempSync(join(tmpdir(), 'loom-pgfixture-test-'));
  writeFileSync(join(out, 'stale.json'), '{}');
  await assert.rejects(
    runPostgres(out, {
      env: { LOOM_TEST_POSTGRES_URL: ADMIN_URL },
      compose: () => ({ status: 'blocked-missing-runtime' }),
      probe: async command => ({ ok: true, command }),
    }),
    /must be empty/,
  );
});
