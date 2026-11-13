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
