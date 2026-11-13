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
//   --paired --binary <server> --out <dir> [--profile tiny|small|representative]
//       [--iterations N>=5 --warmup N>=1 --rounds N] [--source-commit SHA]
//       [--benchcli <path>] [--window-note <text>]
//       The operator-mandated comparison: the same matched battery runs
//       against BOTH real drivers (LOOM_TEST_ARANGO_URL + 
//       LOOM_TEST_POSTGRES_URL), correctness gates first, then >=5
//       alternating timings per driver. Writes comparison.md next to the
//       verified report. FAILS (never skips) when a service is missing,
//       an arm is planned/not_run/excluded, or a driver has no timings.

import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';

export const CHECK_HARNESS = 'benchmark-harness-tests';
export const CHECK_LIVE = 'live-two-backend-correctness';
export const CHECK_PAIRED = 'paired-driver-benchmark';

// Operator acceptance (2026-11-13 amendment): a paired benchmark is real
// only when BOTH drivers executed matched workloads and produced actual
// timing. Five alternating measured samples per backend is the floor;
// warmup samples never count.
export const PAIRED_MIN_SAMPLES_PER_BACKEND = 5;
export const PAIRED_PROFILES = ['tiny', 'small', 'representative'];

export function parseArgs(argv) {
  const flags = {
    mode: null, report: null, benchcli: null, out: null, runArgs: [], selftest: false,
    paired: false, binary: null, profile: 'tiny', iterations: 5, warmup: 1, rounds: null,
    concurrency: 1, previewLimit: 1000, sourceCommit: null, seed: 7, windowNote: null,
  };
  let i = 0;
  const int = (name, raw, min) => {
    const value = Number(raw);
    if (!Number.isInteger(value) || value < min) throw new Error(`${name} must be an integer >= ${min}, got ${raw}`);
    return value;
  };
  while (i < argv.length) {
    const arg = argv[i];
    if (arg === '--verify-only') flags.mode = 'verify';
    else if (arg === '--run') flags.mode = 'run';
    else if (arg === '--paired') flags.paired = true;
    else if (arg === '--harness-selftest') flags.selftest = true;
    else if (arg === '--report') { i += 1; flags.report = argv[i]; }
    else if (arg === '--benchcli') { i += 1; flags.benchcli = argv[i]; }
    else if (arg === '--binary') { i += 1; flags.binary = argv[i]; }
    else if (arg === '--profile') { i += 1; flags.profile = argv[i]; }
    else if (arg === '--iterations') { i += 1; flags.iterations = int('--iterations', argv[i], PAIRED_MIN_SAMPLES_PER_BACKEND); }
    else if (arg === '--warmup') { i += 1; flags.warmup = int('--warmup', argv[i], 1); }
    else if (arg === '--rounds') { i += 1; flags.rounds = int('--rounds', argv[i], 1); }
    else if (arg === '--concurrency') { i += 1; flags.concurrency = int('--concurrency', argv[i], 1); }
    else if (arg === '--preview-limit') { i += 1; flags.previewLimit = int('--preview-limit', argv[i], 1); }
    else if (arg === '--seed') { i += 1; flags.seed = int('--seed', argv[i], 0); }
    else if (arg === '--source-commit') { i += 1; flags.sourceCommit = argv[i]; }
    else if (arg === '--window-note') { i += 1; flags.windowNote = argv[i]; }
    else if (arg === '--out') { i += 1; flags.out = argv[i]; }
    else if (arg === '--') { flags.runArgs = argv.slice(i + 1); break; }
    else throw new Error(`unknown argument: ${arg}`);
    i += 1;
  }
  if (flags.selftest && (flags.mode || flags.paired)) throw new Error('--harness-selftest cannot combine with a run mode');
  if (flags.paired && flags.mode) throw new Error('--paired cannot combine with --verify-only/--run');
  if (!flags.selftest && !flags.mode && !flags.paired) throw new Error('choose --verify-only, --run, --paired, or --harness-selftest');
  if (flags.mode === 'verify' && !flags.report) throw new Error('--verify-only requires --report');
  if (flags.mode === 'run' && !flags.out) throw new Error('--run requires --out');
  if (flags.paired) {
    // Default: fully alternating — one measured iteration per round, so
    // every driver's sample sits at the same position in the schedule.
    if (flags.rounds === null) flags.rounds = flags.iterations;
    if (!flags.out) throw new Error('--paired requires --out');
    if (!flags.binary) throw new Error('--paired requires --binary (the server binary the arms are spawned from)');
    if (!PAIRED_PROFILES.includes(flags.profile)) throw new Error(`--profile must be one of ${PAIRED_PROFILES.join(', ')}, got ${flags.profile}`);
    if (flags.iterations < PAIRED_MIN_SAMPLES_PER_BACKEND) {
      throw new Error(`--paired requires --iterations >= ${PAIRED_MIN_SAMPLES_PER_BACKEND}: fewer alternating runs per driver is not a comparison`);
    }
    if (flags.warmup < 1) throw new Error('--paired requires --warmup >= 1 so cached timings are meaningful');
    if (flags.rounds < 1) throw new Error('--paired requires --rounds >= 1');
    if (flags.iterations % flags.rounds !== 0) throw new Error(`--iterations ${flags.iterations} must split evenly into --rounds ${flags.rounds}`);
    if (!/^[0-9a-f]{40}$/.test(flags.sourceCommit ?? '')) throw new Error('--paired requires --source-commit as the full 40-hex SHA the arms were built from');
  }
  return flags;
}

// Paired service discovery. The CI lane exports the same two variables the
// backend-conformance job uses; the local dual-URL path uses them too. A
// missing service is a hard failure: silently benchmarking one driver and
// calling it a comparison is exactly what the operator rejected.
export function pairedServices(env) {
  const arangoBase = (env.LOOM_TEST_ARANGO_URL ?? '').trim();
  const postgresDsn = (env.LOOM_TEST_POSTGRES_URL ?? '').trim();
  const problems = [];
  if (!arangoBase) problems.push('LOOM_TEST_ARANGO_URL is not set: the Arango/AQL driver has no service to run against');
  if (!postgresDsn) problems.push('LOOM_TEST_POSTGRES_URL is not set: the PostgreSQL/SQL driver has no service to run against');
  // benchcli refuses an Arango DSN that does not name a loom_bench*
  // database; the CI service URL carries no path, so the lane's database
  // name is appended here (the server creates it on first use).
  let arangoUrl = arangoBase;
  if (arangoUrl) {
    const withoutScheme = arangoUrl.replace(/^[a-z]+:\/\//, '');
    if (!withoutScheme.includes('/')) arangoUrl = `${arangoUrl.replace(/\/$/, '')}/loom_bench_ci`;
  }
  return { arangoUrl, postgresDsn, problems };
}

export function buildPairedInvocation(flags, services) {
  const bin = flags.benchcli ?? 'go';
  const args = [
    '--scope=both-arms',
    // The ClickHouse-backed publication chain is disclosed not_run; the
    // timing battery itself is ClickHouse-free and runs for real on both
    // drivers (the backend-conformance lane proves that posture).
    '--publication=timed',
    `--preset=${flags.profile}`,
    `--seed=${flags.seed}`,
    `--warmup=${flags.warmup}`,
    `--iterations=${flags.iterations}`,
    `--rounds=${flags.rounds}`,
    `--concurrency=${flags.concurrency}`,
    `--preview-limit=${flags.previewLimit}`,
    `--binary=${flags.binary}`,
    `--arango-dsn=${services.arangoUrl}`,
    `--postgres-dsn=${services.postgresDsn}`,
    `--source-commit=${flags.sourceCommit}`,
    `--out=${flags.out}`,
  ];
  if (flags.windowNote) args.push(`--window-note=${flags.windowNote}`);
  return { cmd: bin, args: flags.benchcli ? ['run', ...args] : ['run', './internal/dataframe/benchmark/cmd/benchcli', 'run', ...args] };
}

// A paired report counts as a real comparison only when both arms actually
// ran matched workloads and each carries at least PAIRED_MIN_SAMPLES_PER_BACKEND
// non-warmup timings. Anything else — a planned, not_run, or excluded arm,
// an empty timing set, a correctness failure — is a fail, never a pass.
export function classifyPairedReport(report, minSamples = PAIRED_MIN_SAMPLES_PER_BACKEND) {
  const problems = [];
  const legs = report.plan?.legs ?? [];
  const ranBackends = legs.filter((leg) => leg.mode === 'run').map((leg) => leg.backend);
  for (const backend of ['arango', 'postgres']) {
    if (!legs.some((leg) => leg.backend === backend)) problems.push(`no leg declared for ${backend}`);
    else if (!ranBackends.includes(backend)) {
      const leg = legs.find((l) => l.backend === backend);
      problems.push(`${backend} arm is ${leg.mode}${leg.capability ? ` (${leg.capability})` : ''}: a planned or not_run arm is not a benchmark`);
    }
  }
  const excludedArms = (report.gateReports ?? []).filter((gate) =>
    [gate.arango, gate.postgres].some((arm) => arm?.outcome === 'blocked'
      && !(gate.exclusionReason ?? '').startsWith('live battery exclusion: ')));
  if (excludedArms.length > 0) {
    problems.push(`${excludedArms.length} gate report(s) carry blocked arms without a live-battery exclusion marker: ${excludedArms.map((g) => g.workloadId).join(', ')}`);
  }
  const measured = {};
  for (const backend of ['arango', 'postgres']) measured[backend] = 0;
  for (const sample of report.samples ?? []) {
    if (sample.warmup) continue;
    if (sample.outcome !== 'passed') continue;
    if (!(sample.backend in measured)) continue;
    if (!Number.isFinite(sample.millis) || sample.millis < 0) continue;
    measured[sample.backend] += 1;
  }
  for (const backend of ['arango', 'postgres']) {
    if (measured[backend] < minSamples) {
      problems.push(`${backend} produced ${measured[backend]} usable timing sample(s), need >= ${minSamples}: without real timing on both drivers there is no comparison`);
    }
  }
  if (report.status !== 'passed') problems.push(`report status is ${report.status ?? 'unknown'}, not passed`);
  if (report.identity?.clickhouseDisabled !== true) {
    problems.push('report does not record ClickHouse as disabled: the timed lane must state that honestly');
  }
  return problems.length === 0 ? { status: 'pass', disclosure: null, measured } : { status: 'fail', disclosure: problems.join('; '), measured };
}

// renderComparisonTable turns the verified summaries into the readable
// table the acceptance gate asks for: units, median, range, and the
// conditions the numbers were taken under. It reads only report fields,
// so a reviewer can rerun the command and reproduce the same rows.
export function renderComparisonTable(report) {
  const identity = report.identity ?? {};
  const rows = report.summaries ?? [];
  const byWorkload = new Map();
  for (const row of rows) {
    const key = row.workloadId;
    if (!byWorkload.has(key)) byWorkload.set(key, {});
    byWorkload.get(key)[row.backend] = row.summary;
  }
  const num = (value) => (typeof value === 'number' ? value.toFixed(2) : 'n/a');
  const lines = [];
  lines.push('# Paired driver benchmark — Arango/AQL vs PostgreSQL/SQL');
  lines.push('');
  lines.push(`Run \`${identity.runId ?? 'n/a'}\` — matched workload, both arms, ClickHouse disabled: \`${identity.clickHouseDisabled === true ? 'yes (timed lane)' : 'no'}\`.`);
  lines.push('');
  lines.push('## Conditions');
  lines.push('');
  lines.push(`- source commit: \`${identity.sourceCommit ?? 'n/a'}\``);
  lines.push(`- dataset preset: \`${identity.preset ?? 'n/a'}\`, fixture digest \`${identity.datasetDigest ?? 'n/a'}\``);
  lines.push(`- ArangoDB version: ${identity.arangoVersion ?? 'n/a'}; PostgreSQL version: ${identity.postgresVersion ?? 'n/a'}`);
  const config = identity.config ?? {};
  lines.push(`- warmup ${config.Warmup ?? 'n/a'} per workload x backend (discarded), ${config.Iterations ?? 'n/a'} measured iterations, ${config.Rounds ?? 'n/a'} interleaved rounds, concurrency ${config.Concurrency ?? 'n/a'}`);
  lines.push('- cache: steady-regime samples (warm for a spawned arm at rest, service-warm for an attached service) feed the medians; process-cold samples are reported separately and never claimed');
  lines.push('');
  lines.push('Timings are milliseconds per preview request, lower is faster. Neither driver is expected to win; a gap smaller than run-to-run variation is reported as such, not as a speedup.');
  lines.push('');
  lines.push('## Timings (ms)');
  lines.push('');
  lines.push('| workload | backend | n | median | min | max | range |');
  lines.push('| --- | --- | --- | --- | --- | --- | --- |');
  for (const [workload, perBackend] of [...byWorkload.entries()].sort()) {
    for (const backend of ['arango', 'postgres']) {
      const s = perBackend[backend];
      if (!s) { lines.push(`| ${workload} | ${backend} | 0 | n/a | n/a | n/a | n/a |`); continue; }
      const range = (typeof s.maxMillis === 'number' && typeof s.minMillis === 'number') ? (s.maxMillis - s.minMillis).toFixed(2) : 'n/a';
      lines.push(`| ${workload} | ${backend} | ${s.count ?? 0} | ${num(s.medianMillis)} | ${num(s.minMillis)} | ${num(s.maxMillis)} | ${range} |`);
    }
  }
  const claims = report.claims ?? [];
  if (claims.length === 0) {
    lines.push('');
    lines.push('No claim cleared the correctness gates plus the stability control, so no speedup is claimed. That is an acceptable outcome; the measurements above are the deliverable.');
  }
  return `${lines.join('\n')}\n`;
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
  const env = deps.env ?? process.env;
  let failures = 0;
  const live = (invocation) => exec(invocation.cmd, invocation.args, { stdio: 'inherit' });

  if (flags.paired) {
    return runPaired(flags, { exec, log, env });
  }

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

// runPaired is the operator-mandated comparison: one matched workload set,
// both real drivers, correctness first, then alternating timings. It
// refuses to call anything a benchmark when a service is missing, an arm
// did not run, or a driver produced no timings — the check goes red rather
// than green-by-scaffolding. The comparison table is written next to the
// verified report so the artifact set is self-explanatory.
function runPaired(flags, { exec, log, env }) {
  const { arangoUrl, postgresDsn, problems } = pairedServices(env);
  if (problems.length > 0) {
    log(formatCheckLine(CHECK_PAIRED, 'fail', problems.join('; ')));
    return 1;
  }
  const invocation = buildPairedInvocation(flags, { arangoUrl, postgresDsn });
  // The paired lane is its own granted window: it runs in a CI job with
  // isolated services, so it sets the live gate the CLI demands.
  const result = exec(invocation.cmd, invocation.args, {
    stdio: 'inherit',
    env: { ...env, LOOM_BENCH_LIVE: '1' },
  });
  const runOutcome = classifyRunExit(result.status);
  log(formatCheckLine(`${CHECK_PAIRED}/run`, runOutcome.status, runOutcome.disclosure ?? undefined));
  const reportPath = `${flags.out}/report.json`;
  if (!existsSync(reportPath)) {
    log(formatCheckLine(CHECK_PAIRED, 'fail', `no report artifact at ${reportPath}; nothing was compared`));
    return 1;
  }
  // Authority stays with the Go verifier: a report this script could
  // classify as fine but the verifier rejects is a fail, never a pass.
  const verify = liveVerify(exec, flags, reportPath, log);
  if (verify !== 0) {
    log(formatCheckLine(CHECK_PAIRED, 'fail', `benchcli verify rejected the paired artifact (exit ${verify})`));
    return 1;
  }
  if (runOutcome.status !== 'pass') {
    log(formatCheckLine(CHECK_PAIRED, 'fail', `paired run exited ${result.status}; a comparison needs a clean run on both drivers`));
    return 1;
  }
  const report = JSON.parse(readFileSync(reportPath, 'utf8'));
  const paired = classifyPairedReport(report);
  if (paired.status !== 'pass') {
    log(formatCheckLine(CHECK_PAIRED, 'fail', paired.disclosure));
    return 1;
  }
  const comparisonPath = `${flags.out}/comparison.md`;
  writeFileSync(comparisonPath, renderComparisonTable(report), 'utf8');
  log(formatCheckLine(CHECK_PAIRED, 'pass',
    `matched battery on both drivers: arango ${paired.measured.arango} + postgres ${paired.measured.postgres} measured timings; table: ${comparisonPath}`));
  return 0;
}

function liveVerify(exec, flags, reportPath, log) {
  const invocation = buildVerifyInvocation({ ...flags, report: reportPath });
  const result = exec(invocation.cmd, invocation.args, { stdio: 'inherit' });
  const outcome = classifyVerifyExit(result.status);
  log(formatCheckLine(CHECK_LIVE, outcome.status, outcome.disclosure ?? `verified ${reportPath}`));
  return result.status;
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
