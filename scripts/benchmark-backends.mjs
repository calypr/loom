#!/usr/bin/env node
// benchmark-backends.mjs — node-side orchestrator for the Arango/AQL vs
// PostgreSQL/SQL matched benchmark. Verification authority is Go (benchcli
// verify): this script only builds invocations, runs them, and reports the
// evidence line for each check. It can never turn a live check green by
// itself (ruling ii): every live status comes from the Go verifier's exit.
//
// Subcommands:
//   --verify-only --report <report.json> [--benchcli <path>]
//       Verify an existing artifact. Exit 0 only if the Go verifier says OK.
//   --run -- <benchcli run flags...>
//       Run a live benchmark through benchcli (which itself fails closed
//       without the granted window), then verify the produced report.
//   --harness-selftest
//       Node-only sanity pass over this script's own logic. Labelled
//       selftest; it can never satisfy a live check.

import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';

export const CHECK_HARNESS = 'benchmark-harness-tests';
export const CHECK_LIVE = 'live-two-backend-correctness';

export function parseArgs(argv) {
  const flags = { mode: null, report: null, benchcli: null, out: null, runArgs: [], selftest: false };
  let i = 0;
  while (i < argv.length) {
    const arg = argv[i];
    if (arg === '--verify-only') flags.mode = 'verify';
    else if (arg === '--run') flags.mode = 'run';
    else if (arg === '--harness-selftest') flags.selftest = true;
    else if (arg === '--report') { i += 1; flags.report = argv[i]; }
    else if (arg === '--benchcli') { i += 1; flags.benchcli = argv[i]; }
    else if (arg === '--out') { i += 1; flags.out = argv[i]; }
    else if (arg === '--') { flags.runArgs = argv.slice(i + 1); break; }
    else throw new Error(`unknown argument: ${arg}`);
    i += 1;
  }
  if (flags.selftest && flags.mode) throw new Error('--harness-selftest cannot combine with --verify-only/--run');
  if (!flags.selftest && !flags.mode) throw new Error('choose --verify-only, --run, or --harness-selftest');
  if (flags.mode === 'verify' && !flags.report) throw new Error('--verify-only requires --report');
  if (flags.mode === 'run' && !flags.out) throw new Error('--run requires --out');
  return flags;
}

export function buildVerifyInvocation(flags) {
  const bin = flags.benchcli ?? 'go';
  const args = flags.benchcli ? ['verify', '--report', flags.report]
    : ['run', './internal/dataframe/benchmark/cmd/benchcli', 'verify', '--report', flags.report];
  return { cmd: bin, args };
}

export function buildRunInvocation(flags) {
  const bin = flags.benchcli ?? 'go';
  const args = flags.benchcli ? ['run', ...flags.runArgs]
    : ['run', './internal/dataframe/benchmark/cmd/benchcli', 'run', ...flags.runArgs];
  return { cmd: bin, args };
}

export function formatCheckLine(id, status, detail) {
  const text = detail ? ` ${detail}` : '';
  return `check ${id}: ${status}${text}`;
}

// A run blocked by preflight (exit 3) is disclosed as not_run with the
// reason, never as a pass and never as a silent skip. Exit 1 is a failure.
export function classifyRunExit(code) {
  if (code === 0) return { status: 'pass', disclosure: null };
  if (code === 3) return { status: 'not_run', disclosure: 'preflight refused the live run' };
  return { status: 'fail', disclosure: `benchcli run exited ${code}` };
}

export function classifyVerifyExit(code) {
  if (code === 0) return { status: 'pass', disclosure: null };
  return { status: 'fail', disclosure: `benchcli verify rejected the artifact (exit ${code})` };
}

// readReportSummary extracts only disclosure facts for the console; the
// authoritative recomputation lives in Go.
export function readReportSummary(path) {
  const raw = JSON.parse(readFileSync(path, 'utf8'));
  return {
    runId: raw.identity?.runId ?? null,
    scope: raw.plan?.scope ?? null,
    status: raw.status ?? null,
    legs: (raw.plan?.legs ?? []).map((leg) => `${leg.backend}:${leg.mode}${leg.capability ? `(${leg.capability})` : ''}`),
    plannedNotRun: (raw.plan?.plannedNotRun ?? []).length,
    claims: (raw.claims ?? []).length,
    samples: (raw.samples ?? []).length,
    summaries: (raw.summaries ?? []).length,
  };
}

// Skip-capture discipline: a planned disclosure artifact must carry zero
// measurement evidence — any sample, summary row, or claim that escaped a
// not_run leg is a FAIL, never a disclosure. Null for non-planned reports;
// the Go verifier stays the authority that the disclosure is consistent.
export function classifyPlannedDisclosure(summary) {
  if (summary.scope !== 'planned') return null;
  if (!summary.legs.every((leg) => leg.includes(':not_run'))) {
    return { status: 'fail', disclosure: 'planned artifact declares a leg that ran' };
  }
  if (summary.claims !== 0 || summary.samples !== 0 || summary.summaries !== 0) {
    return {
      status: 'fail',
      disclosure: `planned artifact captured measurement evidence (samples=${summary.samples}, summaries=${summary.summaries}, claims=${summary.claims})`,
    };
  }
  return { status: 'not_run', disclosure: `planned artifact disclosed: ${summary.legs.length} not_run legs, zero timing captured` };
}

export function run(flags, deps = {}) {
  const exec = deps.exec ?? spawnSync;
  const log = deps.log ?? ((line) => console.log(line));
  let failures = 0;
  const live = (invocation) => exec(invocation.cmd, invocation.args, { stdio: 'inherit' });

  if (flags.mode === 'verify') {
    const result = live(buildVerifyInvocation(flags));
    const outcome = classifyVerifyExit(result.status);
    log(formatCheckLine(CHECK_LIVE, outcome.status, outcome.disclosure ?? `verified ${flags.report}`));
    if (outcome.status !== 'pass') failures += 1;
    return failures;
  }
  if (flags.mode === 'run') {
    const result = live(buildRunInvocation(flags));
    const outcome = classifyRunExit(result.status);
    log(formatCheckLine('live-run', outcome.status, outcome.disclosure ?? undefined));
    if (outcome.status === 'not_run') {
      const reportPath = `${flags.out}/report.json`;
      if (existsSync(reportPath)) {
        // A planned run writes its disclosure artifact and still exits 3.
        // Classify it here and let the Go verifier keep authority.
        const summary = readReportSummary(reportPath);
        const planned = classifyPlannedDisclosure(summary);
        if (planned && planned.status === 'fail') {
          log(formatCheckLine(CHECK_LIVE, 'fail', planned.disclosure));
          failures += 1;
          return failures;
        }
        const verify = live(buildVerifyInvocation({ ...flags, report: reportPath }));
        const verified = classifyVerifyExit(verify.status);
        log(formatCheckLine(CHECK_LIVE, 'not_run',
          `${(planned ?? { disclosure: 'disclosure artifact produced without preflight' }).disclosure}; go verify ${verified.status}`));
        if (verified.status !== 'pass') {
          log(formatCheckLine(CHECK_LIVE, 'fail', verified.disclosure));
          failures += 1;
        }
        return failures;
      }
      log(formatCheckLine(CHECK_LIVE, 'not_run', 'no artifact produced; run stays planned/not_run until the granted window'));
      return failures; // a refused preflight is disclosed, not a failure
    }
    if (outcome.status !== 'pass') { failures += 1; return failures; }
    const reportPath = `${flags.out}/report.json`;
    const verify = live(buildVerifyInvocation({ ...flags, report: reportPath }));
    const verified = classifyVerifyExit(verify.status);
    log(formatCheckLine(CHECK_LIVE, verified.status, verified.disclosure ?? `verified ${reportPath}`));
    if (verified.status !== 'pass') failures += 1;
    else if (existsSync(reportPath)) log(`report summary: ${JSON.stringify(readReportSummary(reportPath))}`);
    return failures;
  }
  // selftest: node-only logic sanity, clearly labelled.
  const sample = { status: 3 };
  const classified = classifyRunExit(sample.status);
  if (classified.status !== 'not_run') throw new Error('selftest: exit 3 must disclose not_run');
  log(formatCheckLine('harness-selftest', 'pass', 'node-only; live checks come from the Go verifier only'));
  return failures;
}

const isMain = process.argv[1] && import.meta.url.endsWith(process.argv[1].split('/').pop());
if (isMain) {
  try {
    const flags = parseArgs(process.argv.slice(2));
    const failures = run(flags);
    process.exit(failures === 0 ? 0 : 1);
  } catch (err) {
    console.error(`benchmark-backends: ${err.message}`);
    process.exit(2);
  }
}
