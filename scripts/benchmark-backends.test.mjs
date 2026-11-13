import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import {
  parseArgs,
  buildVerifyInvocation,
  buildRunInvocation,
  classifyRunExit,
  classifyVerifyExit,
  formatCheckLine,
  readReportSummary,
  run,
  CHECK_LIVE,
} from './benchmark-backends.mjs';

test('parseArgs requires a mode', () => {
  assert.throws(() => parseArgs([]), /choose --verify-only/);
  assert.throws(() => parseArgs(['--verify-only']), /requires --report/);
  assert.throws(() => parseArgs(['--run']), /requires --out/);
  assert.throws(() => parseArgs(['--nope']), /unknown argument/);
  assert.throws(() => parseArgs(['--run', '--harness-selftest']), /cannot combine/);
});

test('run mode collects benchcli flags after the separator', () => {
  const flags = parseArgs(['--run', '--out', 'bench-out', '--', '--scope', 'postgres-only', '--window-note', 'granted by 2']);
  assert.equal(flags.mode, 'run');
  assert.equal(flags.out, 'bench-out');
  assert.deepEqual(flags.runArgs, ['--scope', 'postgres-only', '--window-note', 'granted by 2']);
});

test('invocations use go run by default and the binary when given', () => {
  const defaultVerify = buildVerifyInvocation({ report: 'r.json' });
  assert.equal(defaultVerify.cmd, 'go');
  assert.deepEqual(defaultVerify.args, ['run', './internal/dataframe/benchmark/cmd/benchcli', 'verify', '--report', 'r.json']);
  const binaryRun = buildRunInvocation({ benchcli: '/tmp/benchcli', runArgs: ['--scope', 'both-arms'] });
  assert.deepEqual(binaryRun, { cmd: '/tmp/benchcli', args: ['run', '--scope', 'both-arms'] });
});

test('exit classification: 0 pass, 3 disclosed not_run, anything else fail', () => {
  assert.equal(classifyRunExit(0).status, 'pass');
  assert.equal(classifyRunExit(3).status, 'not_run');
  assert.match(classifyRunExit(3).disclosure, /preflight refused/);
  assert.equal(classifyRunExit(1).status, 'fail');
  assert.equal(classifyRunExit(null).status, 'fail');
  assert.equal(classifyVerifyExit(0).status, 'pass');
  assert.equal(classifyVerifyExit(1).status, 'fail');
});

test('a blocked live run is disclosed as not_run, never a pass', () => {
  const lines = [];
  const calls = [];
  const deps = {
    log: (line) => lines.push(line),
    exec: (cmd, args) => { calls.push([cmd, args]); return { status: 3 }; },
  };
  const failures = run(parseArgs(['--run', '--out', 'out', '--', '--scope', 'postgres-only']), deps);
  assert.equal(failures, 0, 'a refused preflight is disclosed, not a failure');
  assert.deepEqual(calls.length, 1, 'no verify step runs without an artifact');
  assert.ok(lines.some((l) => l.includes('check live-run: not_run')));
  assert.ok(lines.some((l) => l.includes(`check ${CHECK_LIVE}: not_run`)));
  assert.ok(!lines.some((l) => l.includes(': pass')));
});

test('verify-only reports the Go exit as the only authority', () => {
  const lines = [];
  const passing = run(parseArgs(['--verify-only', '--report', 'r.json']), {
    log: (line) => lines.push(line),
    exec: () => ({ status: 0 }),
  });
  assert.equal(passing, 0);
  assert.ok(lines.some((l) => l.includes(`check ${CHECK_LIVE}: pass`)));

  const rejectLines = [];
  const failing = run(parseArgs(['--verify-only', '--report', 'r.json']), {
    log: (line) => rejectLines.push(line),
    exec: () => ({ status: 1 }),
  });
  assert.equal(failing, 1);
  assert.ok(rejectLines.some((l) => l.includes(`check ${CHECK_LIVE}: fail`)));
});

test('successful run verifies the produced artifact before passing', () => {
  const calls = [];
  const lines = [];
  const failures = run(parseArgs(['--run', '--out', 'out', '--']), {
    log: (line) => lines.push(line),
    exec: (cmd, args) => { calls.push(args); return { status: 0 }; },
  });
  assert.equal(failures, 0);
  assert.equal(calls.length, 2, 'run then verify');
  assert.deepEqual(calls[1].slice(-3), ['verify', '--report', 'out/report.json']);
});

test('readReportSummary surfaces scope, legs, and disclosure counts only', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'bench-summary-'));
  const path = join(dir, 'report.json');
  await writeFile(path, JSON.stringify({
    identity: { runId: 'bench-x' },
    status: 'passed',
    plan: {
      scope: 'postgres-only',
      legs: [
        { backend: 'arango', mode: 'not_run', capability: 'no service' },
        { backend: 'postgres', mode: 'run' },
      ],
      plannedNotRun: [{ workloadId: 'a', backend: 'arango', reason: 'leg not run' }],
    },
    claims: [],
  }));
  const summary = readReportSummary(path);
  assert.equal(summary.scope, 'postgres-only');
  assert.equal(summary.plannedNotRun, 1);
  assert.deepEqual(summary.legs, ['arango:not_run(no service)', 'postgres:run']);
  assert.equal(summary.claims, 0);
});

test('formatCheckLine is a stable single line', () => {
  assert.equal(formatCheckLine('x', 'pass', 'ok'), 'check x: pass ok');
  assert.equal(formatCheckLine('x', 'fail'), 'check x: fail');
});

import { classifyPlannedDisclosure } from './benchmark-backends.mjs';

function plannedSummary(overrides = {}) {
  return {
    runId: 'bench-planned-a', scope: 'planned', status: 'failed',
    legs: ['arango:not_run(no ArangoDB service)', 'postgres:not_run(blocked by PG-PG-AUTH-PATH-DISCOVERY-ALLGENS)'],
    plannedNotRun: 20, claims: 0, samples: 0, summaries: 0,
    ...overrides,
  };
}

test('planned disclosure: clean artifact is not_run, captured timing is fail', () => {
  const clean = classifyPlannedDisclosure(plannedSummary());
  assert.equal(clean.status, 'not_run');
  assert.match(clean.disclosure, /zero timing captured/);
  for (const bad of [{ samples: 1 }, { summaries: 1 }, { claims: 1 },
    { legs: ['arango:not_run(x)', 'postgres:run'] }]) {
    assert.equal(classifyPlannedDisclosure(plannedSummary(bad)).status, 'fail', `skip-capture must refuse ${JSON.stringify(bad)}`);
  }
  assert.equal(classifyPlannedDisclosure(plannedSummary({ scope: 'postgres-only' })), null);
});

test('exit-3 planned artifact is verified by Go and disclosed not_run, never pass', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'bench-planned-'));
  await writeFile(join(dir, 'report.json'), JSON.stringify({
    identity: { runId: 'bench-planned-a' }, status: 'failed',
    plan: {
      scope: 'planned',
      legs: [{ backend: 'arango', mode: 'not_run', capability: 'no service' },
        { backend: 'postgres', mode: 'not_run', capability: 'blocked by PG-PG-AUTH-PATH-DISCOVERY-ALLGENS' }],
      plannedNotRun: [],
    },
    claims: [], samples: [], summaries: [],
  }));
  const lines = [];
  const calls = [];
  const failures = run(parseArgs(['--run', '--out', dir, '--', '--scope', 'planned']), {
    log: (line) => lines.push(line),
    exec: (cmd, args) => { calls.push(args); return { status: args.includes('verify') ? 0 : 3 }; },
  });
  assert.equal(failures, 0);
  assert.equal(calls.length, 2, 'planned artifact still goes through Go verify');
  assert.ok(lines.some((l) => l.includes(`check ${CHECK_LIVE}: not_run`) && l.includes('zero timing captured')));
  assert.ok(!lines.some((l) => l.includes(': pass')));
});

test('planned artifact with escaped samples fails the check', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'bench-planned-bad-'));
  await writeFile(join(dir, 'report.json'), JSON.stringify({
    identity: { runId: 'bench-planned-bad' }, status: 'failed',
    plan: {
      scope: 'planned',
      legs: [{ backend: 'arango', mode: 'not_run', capability: 'no service' },
        { backend: 'postgres', mode: 'not_run', capability: 'blocked' }],
      plannedNotRun: [],
    },
    claims: [], samples: [{ backend: 'postgres', millis: 12 }], summaries: [],
  }));
  const lines = [];
  const calls = [];
  const failures = run(parseArgs(['--run', '--out', dir, '--', '--scope', 'planned']), {
    log: (line) => lines.push(line),
    exec: (cmd, args) => { calls.push(args); return { status: 3 }; },
  });
  assert.equal(failures, 1, 'captured timing in a planned artifact is a failure');
  assert.equal(calls.length, 1, 'a failing disclosure is not verified green');
  assert.ok(lines.some((l) => l.includes(`check ${CHECK_LIVE}: fail`) && l.includes('measurement evidence')));
});

// --- paired-driver benchmark (operator amendment 2026-11-13) -------------------

import {
  pairedServices,
  pairedDatabaseFromDsn,
  buildPairedInvocation,
  classifyPairedReport,
  renderComparisonTable,
  PAIRED_MIN_SAMPLES_PER_BACKEND,
  CHECK_PAIRED,
} from './benchmark-backends.mjs';

const SHA40 = 'a'.repeat(40);
// The paired lane requires a ClickHouse service: the publication chain is
// ClickHouse-backed end to end (authoring publishes through it).
const CH_ARGS = ['--clickhouse-url', 'clickhouse://127.0.0.1:9000'];

function pairedReportFixture(overrides = {}) {
  const samples = [];
  for (const backend of ['arango', 'postgres']) {
    for (let i = 0; i < PAIRED_MIN_SAMPLES_PER_BACKEND; i += 1) {
      samples.push({ backend, workloadId: 'select-filter', round: 1, iteration: i, warmup: false, outcome: 'passed', millis: 10 + i, cache: 'warm' });
    }
    samples.push({ backend, workloadId: 'select-filter', round: 0, iteration: 0, warmup: true, outcome: 'passed', millis: 20, cache: 'warm' });
  }
  return {
    status: 'passed',
    identity: {
      runId: 'bench-paired-1', preset: 'tiny', sourceCommit: SHA40, datasetDigest: 'd'.repeat(64),
      arangoVersion: '3.12.4', postgresVersion: 'PostgreSQL 18.0', clickhouseDisabled: false,
      config: { Warmup: 1, Iterations: 5, Concurrency: 1, Rounds: 1 },
    },
    plan: { scope: 'both-arms', legs: [{ backend: 'arango', mode: 'run' }, { backend: 'postgres', mode: 'run' }], publication: { run: true } },
    gateReports: [{ workloadId: 'select-filter', claimable: true, arango: { outcome: 'passed' }, postgres: { outcome: 'passed' } }],
    samples,
    summaries: [
      { workloadId: 'select-filter', backend: 'arango', cache: 'warm', summary: { count: 5, failedCount: 0, medianMillis: 12, minMillis: 10, maxMillis: 14 } },
      { workloadId: 'select-filter', backend: 'postgres', cache: 'warm', summary: { count: 5, failedCount: 0, medianMillis: 22, minMillis: 20, maxMillis: 24 } },
    ],
    claims: [],
    ...overrides,
  };
}

test('paired: missing services fail the check, never skip it', () => {
  const seen = pairedServices({});
  assert.equal(seen.problems.length, 2);
  const logs = [];
  let spawned = false;
  const failures = run(parseArgs(['--paired', '--binary', '/bin/loom', '--out', 'out', '--source-commit', SHA40, ...CH_ARGS]), {
    exec: () => { spawned = true; return { status: 0 }; },
    log: (line) => logs.push(line),
    env: {},
  });
  assert.equal(failures, 1);
  assert.equal(spawned, false, 'benchcli must not even start without both services');
  assert.match(logs.join('\n'), new RegExp(`check ${CHECK_PAIRED}: fail`));
});

test('paired: arango dsn gets a benchmark-namespaced database', () => {
  const seen = pairedServices({ LOOM_TEST_ARANGO_URL: 'http://127.0.0.1:8529', LOOM_TEST_POSTGRES_URL: 'postgresql://u:p@127.0.0.1:5432/loom_bench_paired?sslmode=disable' });
  assert.deepEqual(seen.problems, []);
  assert.match(seen.arangoUrl, /\/loom_bench_ci$/);
});

test('paired invocation: both arms, full chain, real iterations', () => {
  const flags = parseArgs(['--paired', '--binary', '/bin/loom', '--out', 'out', '--profile', 'tiny', '--iterations', '5', '--warmup', '1', '--rounds', '5', '--source-commit', SHA40, ...CH_ARGS]);
  const inv = buildPairedInvocation(flags, { arangoUrl: 'http://127.0.0.1:8529/loom_bench_ci', postgresDsn: 'postgresql://u:p@h:5432/loom_bench_paired' });
  const joined = inv.args.join(' ');
  assert.match(joined, /--scope=both-arms/);
  assert.match(joined, /--publication=run/);
  assert.match(joined, /--clickhouse-enabled/);
  assert.match(joined, /--clickhouse-url=clickhouse:\/\/127\.0\.0\.1:9000/);
  assert.match(joined, /--clickhouse-recipe=\S+recipe\.json/);
  assert.match(joined, /--iterations=5/);
  assert.match(joined, /--warmup=1/);
  assert.match(joined, /--arango-dsn=http:\/\/127\.0\.0\.1:8529\/loom_bench_ci/);
});

test('paired flags refuse a non-comparison', () => {
  const base = ['--paired', '--binary', '/bin/loom', '--out', 'out', '--source-commit', SHA40, ...CH_ARGS];
  const without = (flag) => {
    const i = base.indexOf(flag);
    return [...base.slice(0, i), ...base.slice(i + 2)];
  };
  assert.throws(() => parseArgs(without('--out')), /requires --out/);
  assert.throws(() => parseArgs(without('--binary')), /requires --binary/);
  assert.throws(() => parseArgs(without('--source-commit')), /source-commit/);
  assert.throws(() => parseArgs([...base, '--iterations', '3']), /integer >= 5/);
  assert.throws(() => parseArgs([...base, '--warmup', '0']), /warmup/);
  assert.throws(() => parseArgs([...base, '--profile', 'smoke']), /--profile must be one of/);
  assert.throws(() => parseArgs(without('--clickhouse-url')), /requires --clickhouse-url/);
  assert.throws(() => parseArgs([...base, '--clickhouse-recipe', '']), /dataframer recipe/);
});

test('paired classification: only real timing on both drivers passes', () => {
  assert.equal(classifyPairedReport(pairedReportFixture()).status, 'pass');

  const oneColdArm = pairedReportFixture();
  oneColdArm.samples = oneColdArm.samples.filter((s) => s.backend !== 'postgres' || s.warmup);
  assert.equal(classifyPairedReport(oneColdArm).status, 'fail', 'postgres with only warmups is not timed');

  const notRunArm = pairedReportFixture();
  notRunArm.plan.legs = [{ backend: 'arango', mode: 'run' }, { backend: 'postgres', mode: 'not_run', capability: 'CH-x' }];
  assert.equal(classifyPairedReport(notRunArm).status, 'fail', 'a not_run arm must fail the check');

  const plannedBoth = pairedReportFixture({ status: 'failed' });
  plannedBoth.plan.legs = [{ backend: 'arango', mode: 'not_run' }, { backend: 'postgres', mode: 'not_run' }];
  const plannedVerdict = classifyPairedReport(plannedBoth);
  assert.equal(plannedVerdict.status, 'fail');
  assert.match(plannedVerdict.disclosure, /planned or not_run arm is not a benchmark/);

  const failedStatus = pairedReportFixture({ status: 'failed' });
  assert.equal(classifyPairedReport(failedStatus).status, 'fail', 'correctness failure cannot be read as fast');

  const chDisabled = pairedReportFixture();
  chDisabled.identity.clickhouseDisabled = true;
  assert.equal(classifyPairedReport(chDisabled).status, 'fail', 'a lane that records ClickHouse as disabled cannot have run the chain the battery needs');

  const chainNotRun = pairedReportFixture();
  delete chainNotRun.plan.publication;
  assert.equal(classifyPairedReport(chainNotRun).status, 'fail', 'a report whose plan does not claim the chain ran is not a comparison');

  const blockedArm = pairedReportFixture();
  blockedArm.gateReports = [{ workloadId: 'select-filter', arango: { outcome: 'blocked' }, postgres: { outcome: 'passed' } }];
  assert.equal(classifyPairedReport(blockedArm).status, 'fail', 'a blocked arm without an exclusion marker is a fail');

  const excludedOk = pairedReportFixture();
  excludedOk.gateReports = [
    { workloadId: 'select-filter', arango: { outcome: 'passed' }, postgres: { outcome: 'passed' } },
    { workloadId: 'derive-age-band', exclusionReason: 'live battery exclusion: unproven recode', arango: { outcome: 'blocked' }, postgres: { outcome: 'blocked' } },
  ];
  assert.equal(classifyPairedReport(excludedOk).status, 'pass', 'the two reviewer-accepted exclusions stand');
});

test('paired table reports medians, ranges, units, versions, and SHA', () => {
  const table = renderComparisonTable(pairedReportFixture());
  assert.match(table, /\| select-filter \| arango \| 5 \| 12\.00 \| 10\.00 \| 14\.00 \| 4\.00 \|/);
  assert.match(table, /\| select-filter \| postgres \| 5 \| 22\.00 \| 20\.00 \| 24\.00 \| 4\.00 \|/);
  assert.match(table, /milliseconds/);
  assert.match(table, new RegExp(SHA40));
  assert.match(table, /3\.12\.4/);
  assert.match(table, /PostgreSQL 18\.0/);
  assert.match(table, /no speedup is claimed/i);
});

test('paired run end-to-end: clean both-arm report passes and writes the table', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'paired-'));
  const report = pairedReportFixture();
  const logs = [];
  const exec = (cmd, args) => {
    if (args.includes('verify')) return { status: 0 };
    return { status: 0 };
  };
  const flags = parseArgs(['--paired', '--binary', '/bin/loom', '--out', dir, '--source-commit', SHA40, ...CH_ARGS]);
  await writeFile(join(dir, 'report.json'), JSON.stringify(report));
  const failures = run(flags, { exec, log: (l) => logs.push(l), env: { LOOM_TEST_ARANGO_URL: 'http://h:8529', LOOM_TEST_POSTGRES_URL: 'postgresql://u:p@h:5432/loom_bench_paired' } });
  assert.equal(failures, 0, logs.join('\n'));
  const { readFile } = await import('node:fs/promises');
  const table = await readFile(join(dir, 'comparison.md'), 'utf8');
  assert.match(table, /select-filter/);
});

test('paired run end-to-end: zero-timing report fails the check even with exit 0', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'paired-'));
  const report = pairedReportFixture();
  report.samples = [];
  report.summaries = [];
  report.claims = [];
  report.status = 'failed';
  const exec = () => ({ status: 0 });
  const logs = [];
  const flags = parseArgs(['--paired', '--binary', '/bin/loom', '--out', dir, '--source-commit', SHA40, ...CH_ARGS]);
  await writeFile(join(dir, 'report.json'), JSON.stringify(report));
  const failures = run(flags, { exec, log: (l) => logs.push(l), env: { LOOM_TEST_ARANGO_URL: 'http://h:8529', LOOM_TEST_POSTGRES_URL: 'postgresql://u:p@h:5432/loom_bench_paired' } });
  assert.equal(failures, 1);
  assert.match(logs.join('\n'), /check paired-driver-benchmark: fail/);
});

test('paired: postgres dsn must name a loom_bench_ database', () => {
  assert.equal(pairedDatabaseFromDsn('postgresql://u:p@h:5432/loom_bench_paired?sslmode=disable').problem, null);
  assert.equal(pairedDatabaseFromDsn('postgresql://u:p@h:5432/loom_bench_paired?sslmode=disable').database, 'loom_bench_paired');
  assert.match(pairedDatabaseFromDsn('postgresql://u:p@h:5432/loom_test?sslmode=disable').problem, /loom_bench_/);
  assert.match(pairedDatabaseFromDsn('postgresql://u:p@h:5432/loom_bench_ci?sslmode=disable').problem, /4,48/, 'two-char suffix must fail like benchcli does');
});

test('paired: admin DSN drives drop-pg then setup-pg before the benchmark', () => {
  const dir = '/tmp/paired-prov-test';
  const calls = [];
  const flags = parseArgs(['--paired', '--binary', '/bin/loom', '--benchcli', '/bin/benchcli', '--out', dir, '--source-commit', SHA40, ...CH_ARGS]);
  const exec = (cmd, args) => { calls.push([cmd, args.join(' ')]); return { status: 0 }; };
  const logs = [];
  run(flags, {
    exec,
    log: (l) => logs.push(l),
    env: {
      LOOM_TEST_ARANGO_URL: 'http://h:8529',
      LOOM_TEST_POSTGRES_URL: 'postgresql://u:p@h:5432/loom_bench_paired?sslmode=disable',
      LOOM_BENCH_ADMIN_DSN: 'postgresql://u:p@h:5432/postgres?sslmode=disable',
    },
  });
  assert.match(calls[0][1], /^drop-pg --database loom_bench_paired$/, `first call was: ${calls[0]?.[1]}`);
  assert.match(calls[1][1], /^setup-pg --database loom_bench_paired$/);
  assert.match(calls[2][1], /run --scope=both-arms/);
});

test('paired: failed setup-pg fails the check before any timing attempt', () => {
  const calls = [];
  const flags = parseArgs(['--paired', '--binary', '/bin/loom', '--benchcli', '/bin/benchcli', '--out', '/tmp/paired-fail', '--source-commit', SHA40, ...CH_ARGS]);
  const logs = [];
  const failures = run(flags, {
    exec: (cmd, args) => {
      calls.push(args.join(' '));
      if (args[0] === 'setup-pg') return { status: 1 };
      return { status: 0 };
    },
    log: (l) => logs.push(l),
    env: {
      LOOM_TEST_ARANGO_URL: 'http://h:8529',
      LOOM_TEST_POSTGRES_URL: 'postgresql://u:p@h:5432/loom_bench_paired?sslmode=disable',
      LOOM_BENCH_ADMIN_DSN: 'postgresql://u:p@h:5432/postgres',
    },
  });
  assert.equal(failures, 1);
  assert.equal(calls.some((c) => c.includes('both-arms')), false, 'no benchmark may start when provisioning failed');
  assert.match(logs.join('\n'), /setup-pg exited 1/);
});
