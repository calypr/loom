#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdir, open, readFile, writeFile, stat } from 'node:fs/promises';
import { resolve, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loadavg, availableParallelism } from 'node:os';

const options = { candidate: resolve(dirname(fileURLToPath(import.meta.url)), '../..'), runs: 5, cache: 'cold' };
for (let i = 2; i < process.argv.length; i += 2) {
  const key = { '--baseline': 'baseline', '--candidate': 'candidate', '--chrome': 'chrome', '--out': 'out', '--runs': 'runs', '--cache': 'cache' }[process.argv[i]];
  assert.ok(key && process.argv[i + 1], `Unknown/missing option ${process.argv[i]}`);
  options[key] = process.argv[i + 1];
}
assert.ok(options.baseline && options.chrome && options.out, '--baseline, --chrome and --out are required');
options.runs = Number(options.runs);
assert.ok(Number.isInteger(options.runs) && options.runs >= 1 && options.runs <= 30, '--runs must be 1..30');
assert.ok(['cold', 'warm'].includes(options.cache), '--cache must be cold or warm');
for (const key of ['baseline', 'candidate', 'chrome', 'out']) options[key] = resolve(options[key]);
await mkdir(options.out); // Every invocation owns a new evidence directory.
const receipt = { status: 'running', options, host: { cores: availableParallelism(), loadStart: loadavg() }, samples: [] };
const fingerprints = new Map();
let expected;

function observations(report) {
  assert.equal(report.status, 'passed', report.error?.message);
  assert.deepEqual(report.browserErrors, []);
  assert.deepEqual(report.cleanup, { chromiumStopped: true, fixtureServerStopped: true, temporaryProfileRemoved: true });
  assert.deepEqual(report.checks.map(check => check.feature), ['drag', 'download', 'route']);
  return report.checks.map(({ seconds, nativeEvents, ...check }) => ({ ...check,
    ...(nativeEvents ? { nativeEvents: [...new Set(nativeEvents.map(event => JSON.stringify(event)))].sort() } : {}),
  }));
}

async function run(side, index) {
  const out = join(options.out, `${index}-${side}`);
  const log = await open(`${out}.log`, 'wx');
  const started = performance.now();
  // The separate process group belongs exclusively to this trial, including Chrome.
  const child = spawn(process.execPath, ['scripts/browser/run.mjs', '--feature', 'all', '--chrome', options.chrome, '--out', out,
    ...(side === 'candidate' && options.cache === 'cold' ? ['--no-cache'] : [])],
    { cwd: options[side], detached: true, stdio: ['ignore', log.fd, log.fd] });
  let timedOut = false, forceStop;
  const deadline = setTimeout(() => {
    timedOut = true;
    process.kill(-child.pid, 'SIGTERM');
    forceStop = setTimeout(() => { try { process.kill(-child.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }, 2000);
  }, 60000);
  let code;
  try { code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', resolve); }); }
  finally {
    clearTimeout(deadline);
    if (forceStop) clearTimeout(forceStop);
    if (timedOut) { try { process.kill(-child.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
    await log.close();
  }
  assert.ok(!timedOut, `${side} trial timed out; see ${out}.log`);
  assert.equal(code, 0, `${side} trial failed; see ${out}.log`);
  const elapsedMs = performance.now() - started;
  const report = JSON.parse(await readFile(join(out, 'browser.json'), 'utf8'));
  if (side === 'candidate' && index !== 'warmup') {
    assert.equal(report.artifactCache.status, options.cache === 'cold' ? 'disabled' : 'hit', 'Keep cold builds separate from warm cache hits');
  }
  if (receipt.chrome) assert.equal(report.chrome, receipt.chrome, 'Chromium version must match across trials');
  receipt.chrome = report.chrome;
  const actual = observations(report);
  if (!expected) expected = actual;
  assert.deepEqual(actual, expected, 'Both sides must perform identical native checks and produce identical observations');
  for (const screenshot of ['drag.png', 'page.png']) assert.ok((await stat(join(out, screenshot))).size > 0);
  const prior = fingerprints.get(side);
  if (prior) assert.equal(report.sourceFingerprint, prior, `${side} source changed between trials`);
  fingerprints.set(side, report.sourceFingerprint);
  const sample = { side, index, elapsedMs, suiteMs: report.seconds * 1000,
    featuresMs: Object.fromEntries(report.checks.map(check => [check.feature, check.seconds * 1000])),
    sourceFingerprint: report.sourceFingerprint, chrome: report.chrome, checks: report.checks.length, downloads: report.checks[1].requests.length,
    artifactCache: report.artifactCache, preparationMs: report.preparationMs, documentNavigations: report.documentNavigations };
  receipt.samples.push(sample);
  console.log(JSON.stringify(sample));
}

try {
  if (options.cache === 'warm') await run('candidate', 'warmup');
  for (let index = 0; index < options.runs; index++) {
    for (const side of index % 2 ? ['candidate', 'baseline'] : ['baseline', 'candidate']) await run(side, index);
  }
  const median = values => { const sorted = [...values].sort((a, b) => a - b); const middle = Math.floor(sorted.length / 2); return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2; };
  receipt.summary = Object.fromEntries(['baseline', 'candidate'].map(side => {
    const rows = receipt.samples.filter(sample => sample.side === side && sample.index !== 'warmup');
    const times = rows.map(sample => sample.elapsedMs);
    return [side, { medianMs: median(times), rangeMs: [Math.min(...times), Math.max(...times)],
      featuresMs: Object.fromEntries(['drag', 'download', 'route'].map(feature => [feature, median(rows.map(sample => sample.featuresMs[feature]))])) }];
  }));
  receipt.speedup = receipt.summary.baseline.medianMs / receipt.summary.candidate.medianMs;
  receipt.observations = expected;
  receipt.status = 'passed';
} catch (error) { receipt.status = 'failed'; receipt.error = error.stack; process.exitCode = 1; }
finally {
  receipt.host.loadEnd = loadavg();
  await writeFile(join(options.out, 'comparison.json'), JSON.stringify(receipt, null, 2) + '\n');
  console.log(JSON.stringify({ status: receipt.status, summary: receipt.summary, speedup: receipt.speedup, error: receipt.error }));
}
