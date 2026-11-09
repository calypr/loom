#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { readFile, writeFile, mkdir, mkdtemp, rm, readdir } from 'node:fs/promises';
import { dirname, resolve, join, extname } from 'node:path';
import { tmpdir, availableParallelism, loadavg, freemem } from 'node:os';
import { fileURLToPath } from 'node:url';
import { cases, semanticCases } from './lightpanda-cases.mjs';
import { compareTrials, chooseConcurrency } from './engine-verdict.mjs';
import { openBrowserPool, bounded } from './browser-pool.mjs';
import { runJobs } from './batch-scheduler.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
if (process.argv.includes('--help')) {
  console.log('Usage: node scripts/browser/compare-engines.mjs --out <empty-directory> [--mode throughput|compatibility] [--engine compare|chromium|lightpanda] [--runs 1..30] [--jobs 1..512] [--concurrency 1,2,4,8] [--case <id>] [--chrome <executable>] [--lightpanda <executable>] [--trace] [--profile] [--fault preview-row|job-identity] [--require-speedup]\nDefault: tune bounded reusable worker pools, then five alternating batch trials per engine. Compatibility mode runs the four native checks serially. Exit 1: failed check/setup. Exit 2: speed gate did not qualify.');
  process.exit(0);
}
const maxWorkers = Math.max(1, Math.min(8, availableParallelism() - 2));
const options = { engine: 'compare', mode: 'throughput', runs: 5, jobs: 64,
  concurrency: [1, 2, 4, 8].filter(value => value <= maxWorkers).join(','),
  lightpanda: process.env.LIGHTPANDA_BIN || 'lightpanda' };
for (let i = 2; i < process.argv.length; i++) {
  if (process.argv[i] === '--trace') { options.trace = true; continue; }
  if (process.argv[i] === '--profile') { options.profile = true; continue; }
  if (process.argv[i] === '--require-speedup') { options.requireSpeedup = true; continue; }
  const key = { '--engine': 'engine', '--mode': 'mode', '--runs': 'runs', '--jobs': 'jobs', '--concurrency': 'concurrency', '--out': 'out', '--lightpanda': 'lightpanda', '--chrome': 'chrome', '--case': 'case', '--fault': 'fault' }[process.argv[i]];
  assert.ok(key && process.argv[i + 1], `Unknown/missing option: ${process.argv[i]}`);
  options[key] = process.argv[++i];
}
assert.ok(['compare', 'chromium', 'lightpanda'].includes(options.engine), 'Unknown engine');
assert.ok(['throughput', 'compatibility'].includes(options.mode), 'Unknown mode');
options.runs = Number(options.runs); options.jobs = Number(options.jobs);
options.concurrency = [...new Set(String(options.concurrency).split(',').map(Number))].sort((a, b) => a - b);
assert.ok(Number.isInteger(options.runs) && options.runs >= 1 && options.runs <= 30, '--runs must be 1..30');
assert.ok(Number.isInteger(options.jobs) && options.jobs >= 1 && options.jobs <= 512, '--jobs must be 1..512');
assert.ok(options.concurrency.length && options.concurrency.every(value => Number.isInteger(value) && value >= 1 && value <= 32 && value <= options.jobs), 'Concurrency must be 1..32 and cannot exceed jobs');
assert.ok(options.out, '--out is required');
assert.ok(!options.fault || ['preview-row', 'job-identity'].includes(options.fault), 'Unknown fault');
assert.ok(options.fault !== 'job-identity' || options.mode === 'throughput', 'Job-identity fault requires throughput mode');
assert.ok(!options.requireSpeedup || options.engine === 'compare', '--require-speedup requires both engines');
const contracts = options.mode === 'throughput' ? semanticCases : cases;
const selected = options.case ? contracts.filter(item => item.id === options.case) : contracts;
assert.ok(selected.length, 'Unknown case for this mode');
assert.ok(options.mode !== 'throughput' || options.jobs >= selected.length, 'Batch must exercise every selected contract');
const jobs = options.mode === 'throughput'
  ? Array.from({ length: options.jobs }, (_, index) => ({ id: `job-${index}`, contract: selected[index % selected.length] }))
  : selected.map(contract => ({ id: contract.id, contract }));
const out = resolve(options.out);
await mkdir(out, { recursive: true });
assert.deepEqual(await readdir(out), [], 'Refusing to overwrite evidence');
const driverStarted = performance.now();
await import('playwright');
const driverSetupMs = performance.now() - driverStarted;
const { build, stop } = await import('../../ui/node_modules/esbuild/lib/main.js');

async function fingerprint() {
  const hash = createHash('sha256');
  async function visit(relative) {
    for (const entry of (await readdir(join(root, relative), { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
      const path = join(relative, entry.name);
      if (entry.isDirectory()) await visit(path);
      else if (entry.isFile()) hash.update(path).update('\0').update(await readFile(join(root, path))).update('\0');
    }
  }
  for (const dir of ['scripts/browser', 'ui/packages/loom-ui/src', 'ui/packages/loom-ui/testSupport/browser']) await visit(dir);
  for (const path of ['scripts/package-lock.json', 'ui/package-lock.json', 'scripts/verify-ui/helpers/verify-browser-disclosure.mjs'])
    hash.update(path).update(await readFile(join(root, path)));
  return hash.digest('hex');
}
const inputFingerprint = await fingerprint();
const temporary = await mkdtemp(join(tmpdir(), 'loom-engine-comparison-'));
let server;
const served = { html: 0, js: 0, css: 0 };
const requestDelta = before => Object.fromEntries(Object.keys(served).map(key => [key, served[key] - before[key]]));
const report = {
  schemaVersion: 2, status: 'running', options, sourceCommit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
  sourceFingerprint: inputFingerprint,
  driverSetupMs,
  system: { platform: process.platform, arch: process.arch, node: process.version, cpus: availableParallelism(), load: loadavg(), freeMemory: freemem() },
  cases: selected.map(({ run, ...contract }) => contract), trials: [], tuning: [],
  resourcePolicy: { lightpanda: options.mode === 'compatibility' ? 'stylesheet' : 'none', chromium: 'normal-browser-loading',
    cache: 'Private Lightpanda HTTP cache per worker; isolated Chromium context cache. Immutable assets; no-store job HTML.' },
  claim: 'Compare complete identical assertion-checked batches at independently tuned concurrency; over-5x gate requires five alternating confirmation trials and no failures.',
};

async function executeJob(job, worker, phase, index, concurrency) {
  const { page, context } = worker;
  const before = performance.now();
  const record = { id: job.id, caseId: job.contract.id, worker: worker.id, status: 'running', errors: [], responses: [], failedRequests: [] };
  const onError = error => record.errors.push(error.message);
  const onFailed = request => record.failedRequests.push({ url: request.url(), error: request.failure()?.errorText });
  const onResponse = response => record.responses.push({ url: response.url(), status: response.status() });
  page.on('pageerror', onError); page.on('requestfailed', onFailed); page.on('response', onResponse);
  try {
    if (options.mode === 'throughput') await page.evaluate(() => { sessionStorage.clear(); localStorage.clear(); });
    if (options.trace) await context.tracing.start({ snapshots: true, screenshots: false, sources: false });
    record.observation = await bounded(job.contract.run(page, report.origin, { jobId: job.id }), 'complete journey', 30000);
    assert.deepEqual(record.errors, [], 'Unexpected page errors');
    assert.deepEqual(record.failedRequests, [], 'Unexpected failed requests');
    assert.ok(record.responses.every(response => response.status >= 200 && response.status < 400), 'Unexpected HTTP errors');
    record.status = 'passed';
  } catch (error) { record.status = 'failed'; record.error = error.stack; }
  finally {
    if (options.trace) {
      try { await bounded(context.tracing.stop({ path: join(out, `${phase}-${concurrency}-${index}-${worker.engine}-${job.id}-${worker.id}.zip`) }), 'trace cleanup'); }
      catch (error) { record.status = 'failed'; record.traceError = error.message; }
    }
    page.off('pageerror', onError); page.off('requestfailed', onFailed); page.off('response', onResponse);
    record.durationMs = performance.now() - before;
  }
  return record;
}

async function warmPool(pool) {
  const before = performance.now();
  await Promise.all(pool.workers.map(({ page }) => page.goto(`${report.origin}/?feature=drag&job=warmup`, { waitUntil: 'load' })));
  return performance.now() - before;
}

async function trial(engine, index, concurrency, phase = 'confirm', existingPool) {
  const result = { engine, index, concurrency, phase, cases: [], status: 'running', expectedJobs: jobs.length };
  const started = performance.now();
  let pool, profiler;
  const beforeRequests = { ...served };
  try {
    pool = existingPool ?? await openBrowserPool({ engine, concurrency, chrome: options.chrome, lightpanda: options.lightpanda, nativeInput: options.mode === 'compatibility' });
    result.version = pool.version; result.topology = pool.topology; result.startupMs = existingPool ? 0 : pool.startupMs;
    if (options.profile) {
      result.resourceSamples = [];
      const sample = () => {
        try {
          const processes = execFileSync('ps', ['-Ao', 'pid=,ppid=,pcpu=,rss=,comm='], { encoding: 'utf8' }).trim().split('\n').map(line => {
            const match = /^\s*(\d+)\s+(\d+)\s+([\d.]+)\s+(\d+)\s+(.+)$/.exec(line);
            return match && { pid: Number(match[1]), parent: Number(match[2]), cpuPercent: Number(match[3]), rssKb: Number(match[4]), command: match[5] };
          }).filter(Boolean);
          const owned = new Set([process.pid]);
          for (let previous = -1; previous !== owned.size;) {
            previous = owned.size;
            for (const item of processes) if (owned.has(item.parent)) owned.add(item.pid);
          }
          result.resourceSamples.push({ atMs: performance.now() - started, processes: processes.filter(item => owned.has(item.pid)) });
        } catch (error) { result.profileError = error.message; }
      };
      sample(); profiler = setInterval(sample, 500);
    }
    if (options.mode === 'throughput') {
      // Warm each reused context/cache identically. Blank startup and shader/cache
      // warmup are outside steady time, but inside the complete batch duration.
      result.warmupMs = existingPool ? 0 : await warmPool(pool);
      const before = performance.now(), cpu = process.cpuUsage(), beforeWorkRequests = { ...served };
      result.cases = await runJobs(jobs, pool.workers, (job, worker) => executeJob(job, worker, phase, index, concurrency));
      result.steadyMs = performance.now() - before;
      const consumed = process.cpuUsage(cpu);
      result.driverCpuMs = (consumed.user + consumed.system) / 1000;
      result.driverCoreUtilization = result.driverCpuMs / result.steadyMs;
      result.workRequests = requestDelta(beforeWorkRequests);
    } else {
      // Native compatibility obligations retain fresh contexts and all original
      // visibility, typing and pointer checks. They are not semantic throughput.
      await bounded(pool.workers[0].context.close(), 'release unused worker context');
      for (const job of jobs) {
        const browser = pool.workers[0].browser;
        const context = await bounded(browser.newContext({ viewport: { width: 1440, height: 1000 } }), 'compatibility context');
        const page = await bounded(context.newPage(), 'compatibility page');
        page.setDefaultTimeout(5000); page.setDefaultNavigationTimeout(10000);
        try { result.cases.push(await executeJob(job, { id: 0, engine, page, context }, phase, index, 1)); }
        finally { await bounded(context.close(), 'compatibility context cleanup'); }
      }
    }
    result.completedJobs = result.cases.filter(record => record.status === 'passed').length;
    result.failedJobs = result.cases.filter(record => record.status !== 'passed').length;
    result.status = result.cases.length === jobs.length && result.failedJobs === 0 ? 'passed' : 'failed';
    if (result.status === 'passed' && options.mode === 'throughput')
      assert.equal(result.workRequests.html, jobs.reduce((sum, job) => sum + job.contract.navigations, 0), 'Every job navigation/reload must reach the no-store fixture server');
    if (result.status === 'passed' && result.steadyMs) result.jobsPerSecond = jobs.length * 1000 / result.steadyMs;
  } catch (error) { result.status = 'failed'; result.error = error.stack; }
  finally {
    clearInterval(profiler);
    if (pool && !existingPool) {
      const before = performance.now();
      result.cleanup = await pool.close(); result.shutdownMs = performance.now() - before;
      if (result.cleanup.errors.length || result.cleanup.browserConnected || !result.cleanup.lightpandaStopped) result.status = 'failed';
      const logs = pool.logs();
      if (logs) await writeFile(join(out, `${phase}-${concurrency}-${index}-${engine}.stderr`), logs);
    } else if (existingPool) result.cleanup = { pending: true, browserConnected: true, lightpandaStopped: false, errors: [] };
    else result.cleanup = { browserConnected: false, lightpandaStopped: false, errors: ['Pool failed to start'] };
    result.durationMs = performance.now() - started;
    result.servedRequests = requestDelta(beforeRequests);
    if (result.status !== 'passed') delete result.jobsPerSecond;
    await writeFile(join(out, `${phase}-${concurrency}-${index}-${engine}.json`), JSON.stringify(existingPool ? { ...result, executionStatus: result.status, status: 'awaiting-pool-cleanup' } : result, null, 2) + '\n');
    console.log(JSON.stringify({ engine, phase, index, concurrency, status: result.status, completed: result.completedJobs, failed: result.failedJobs, durationMs: result.durationMs, steadyMs: result.steadyMs, jobsPerSecond: result.jobsPerSecond, driverCoreUtilization: result.driverCoreUtilization }));
  }
  return result;
}

try {
  if (options.engine !== 'chromium') report.lightpandaVersion = execFileSync(options.lightpanda, ['version'], { encoding: 'utf8', timeout: 3000 }).trim();
  const buildStart = performance.now();
  const plugins = options.fault === 'preview-row' ? [{ name: 'wrong-fixture-row', setup(build) {
    build.onLoad({ filter: /testSupport\/browser\/fixture\.tsx$/ }, async ({ path }) => {
      const source = await readFile(path, 'utf8');
      assert.equal(source.split("'patient-A'").length, 2, 'Fault must replace exactly one fixture row');
      return { contents: source.replace("'patient-A'", "'wrong-patient'"), loader: 'tsx', resolveDir: dirname(path) };
    });
  } }] : [];
  await build({ absWorkingDir: root, entryPoints: ['ui/packages/loom-ui/testSupport/browser/fixture.tsx'], bundle: true, format: 'esm', outdir: temporary, entryNames: 'app', jsx: 'automatic', logLevel: 'silent', define: { 'process.env.NODE_ENV': '"production"' }, nodePaths: [join(root, 'ui/node_modules')], minify: options.mode === 'throughput', plugins });
  stop();
  const [{ default: postcss }, { default: tailwind }] = await Promise.all([
    import('../../ui/node_modules/postcss/lib/postcss.mjs'), import('../../ui/node_modules/tailwindcss/lib/index.js'),
  ]);
  const css = await postcss([tailwind({ content: [join(root, 'ui/packages/loom-ui/src/**/*.tsx'), join(root, 'ui/packages/loom-ui/testSupport/browser/fixture.tsx')], theme: { extend: {} }, plugins: [] })])
    .process(await readFile(join(temporary, 'app.css'), 'utf8'), { from: join(temporary, 'app.css') });
  await writeFile(join(temporary, 'app.css'), css.css);
  report.buildMs = performance.now() - buildStart;
  report.preparationMs = report.buildMs + driverSetupMs;
  report.assets = {};
  for (const name of ['app.js', 'app.css']) { const bytes = await readFile(join(temporary, name)); report.assets[name] = { bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') }; }
  const assetBytes = Object.fromEntries(await Promise.all(['app.js', 'app.css'].map(async name => [name, await readFile(join(temporary, name))])));
  server = createServer(async (req, res) => {
    try {
      const requestURL = new URL(req.url, 'http://localhost');
      const path = requestURL.pathname;
      if (['/app.js', '/app.css'].includes(path)) {
        served[path.endsWith('.css') ? 'css' : 'js']++;
        res.setHeader('Content-Type', extname(path) === '.css' ? 'text/css' : 'text/javascript');
        res.setHeader('Cache-Control', 'public, max-age=31536000, immutable');
        res.end(assetBytes[path.slice(1)]);
      } else if (path === '/') {
        served.html++;
        res.setHeader('Content-Type', 'text/html');
        res.setHeader('Cache-Control', 'no-store');
        const job = requestURL.searchParams.get('job') || 'compatibility';
        assert.match(job, /^[a-z0-9-]{1,80}$/);
        const documentJob = options.fault === 'job-identity' && job === 'job-0' ? 'wrong-job' : job;
        res.end('<!doctype html><html><head><meta name="loom-job" content="' + documentJob + '"><meta charset="utf-8"><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>');
      } else { res.statusCode = 404; res.end('not found'); }
    } catch (error) { res.statusCode = 500; res.end(error.message); }
  });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  report.origin = `http://127.0.0.1:${server.address().port}`;
  const engines = options.engine === 'compare' ? ['chromium', 'lightpanda'] : [options.engine];
  const chosen = {};
  if (options.mode === 'throughput' && options.concurrency.length > 1) {
    for (const [index, concurrency] of options.concurrency.entries()) {
      for (const engine of index % 2 ? [...engines].reverse() : engines)
        report.tuning.push(await trial(engine, index, concurrency, 'tune'));
    }
    for (const engine of engines) {
      const valid = report.tuning.filter(result => result.engine === engine && result.status === 'passed');
      assert.ok(valid.length, `No correct ${engine} tuning configuration; cannot compare throughput`);
      chosen[engine] = chooseConcurrency(valid);
    }
  } else for (const engine of engines) chosen[engine] = options.mode === 'throughput' ? options.concurrency[0] : 1;
  report.chosenConcurrency = chosen;
  const confirmationPools = {};
  report.poolLifecycle = {};
  try {
    if (options.mode === 'throughput' && engines.length === 1) for (const engine of engines) {
      const before = performance.now();
      const pool = confirmationPools[engine] = await openBrowserPool({ engine, concurrency: chosen[engine], chrome: options.chrome, lightpanda: options.lightpanda });
      const warmupMs = await warmPool(pool);
      report.poolLifecycle[engine] = { setupMs: performance.now() - before, startupMs: pool.startupMs, warmupMs };
    }
    for (let index = 0; index < options.runs; index++) {
      for (const engine of index % 2 ? [...engines].reverse() : engines)
        report.trials.push(await trial(engine, index, chosen[engine], 'confirm', confirmationPools[engine]));
    }
  } finally {
    const finalized = [];
    for (const [engine, pool] of Object.entries(confirmationPools)) {
      const before = performance.now();
      let cleanup;
      try { cleanup = await pool.close(); }
      catch (error) { cleanup = { browserConnected: true, lightpandaStopped: false, errors: [error.message] }; }
      const lifecycle = report.poolLifecycle[engine] ??= { setupMs: pool.startupMs };
      lifecycle.shutdownMs = performance.now() - before;
      lifecycle.cleanup = cleanup;
      const group = report.trials.filter(trial => trial.engine === engine);
      const overhead = lifecycle.setupMs + lifecycle.shutdownMs;
      lifecycle.suiteMs = overhead + group.reduce((sum, trial) => sum + trial.durationMs, 0);
      lifecycle.completedJobs = group.reduce((sum, trial) => sum + (trial.completedJobs ?? 0), 0);
      for (const result of group) {
        result.processingMs = result.durationMs;
        result.amortizedPoolMs = overhead / group.length;
        result.durationMs += result.amortizedPoolMs;
        result.cleanup = cleanup;
        result.cleanupScope = 'Shared pool verified after all confirmation batches';
        if (cleanup.errors.length || cleanup.browserConnected || !cleanup.lightpandaStopped) { result.status = 'failed'; delete result.jobsPerSecond; }
      }
      finalized.push({ engine, pool, group });
    }
    // Close every pool before persisting receipts. A full/read-only evidence
    // volume must not strand another engine's live workers.
    for (const { engine, pool, group } of finalized) {
      for (const result of group)
        await writeFile(join(out, `confirm-${result.concurrency}-${result.index}-${engine}.json`), JSON.stringify(result, null, 2) + '\n');
      const logs = pool.logs();
      if (logs) await writeFile(join(out, `confirm-pool-${engine}.stderr`), logs);
    }
  }
  if (options.mode === 'throughput' && engines.length === 2) for (const engine of engines) {
    const group = report.trials.filter(trial => trial.engine === engine);
    report.poolLifecycle[engine] = {
      poolInstances: group.length,
      startupMs: group.reduce((sum, trial) => sum + (trial.startupMs ?? 0), 0),
      warmupMs: group.reduce((sum, trial) => sum + (trial.warmupMs ?? 0), 0),
      shutdownMs: group.reduce((sum, trial) => sum + (trial.shutdownMs ?? 0), 0),
      suiteMs: group.reduce((sum, trial) => sum + trial.durationMs, 0),
      completedJobs: group.reduce((sum, trial) => sum + (trial.completedJobs ?? 0), 0),
      cleanup: { browserConnected: group.some(trial => trial.cleanup.browserConnected),
        lightpandaStopped: group.every(trial => trial.cleanup.lightpandaStopped),
        errors: group.flatMap(trial => trial.cleanup.errors) },
    };
    report.poolLifecycle[engine].setupMs = report.poolLifecycle[engine].startupMs + report.poolLifecycle[engine].warmupMs;
  }
  report.poolPolicy = engines.length === 2
    ? 'Independent comparative trials: only the measured engine occupies browser memory; each pool persists across all jobs in its batch.'
    : 'Single-engine execution: retain the pool across all batches and close once.';
  assert.equal(await fingerprint(), inputFingerprint, 'Sources changed during comparison');
  report.status = report.trials.every(item => item.status === 'passed') ? 'passed' : 'failed';
  report.preparationPerBatchMs = options.mode === 'throughput' ? report.preparationMs / options.runs : report.preparationMs;
  report.comparison = compareTrials({ trials: report.trials, caseIds: jobs.map(job => job.id), buildMs: report.preparationPerBatchMs, completeSelection: !options.case });
  if (options.mode === 'throughput') {
    report.steadyComparison = compareTrials({ trials: report.trials.map(trial => ({ ...trial, durationMs: trial.steadyMs })), caseIds: jobs.map(job => job.id), buildMs: 0, completeSelection: !options.case });
    report.coverageBoundary = 'Two DOM semantic contracts repeated for throughput, not 64 distinct coverage cases or native input acceptance.';
    if (engines.length === 2 && report.status === 'passed' && report.comparison.caseResults.every(result => result.equivalent)) {
      const chromium = report.poolLifecycle.chromium, lightpanda = report.poolLifecycle.lightpanda;
      assert.equal(chromium.completedJobs, jobs.length * options.runs, 'Chromium suite job count');
      assert.equal(lightpanda.completedJobs, jobs.length * options.runs, 'Lightpanda suite job count');
      report.suiteComparison = { jobsPerEngine: jobs.length * options.runs, chromiumMs: chromium.suiteMs, lightpandaMs: lightpanda.suiteMs,
        speedup: chromium.suiteMs / lightpanda.suiteMs,
        withBuildSpeedup: (chromium.suiteMs + report.preparationMs) / (lightpanda.suiteMs + report.preparationMs) };
      if (report.comparison.verdict === 'CANDIDATE' && (report.suiteComparison.speedup <= 5 || report.suiteComparison.withBuildSpeedup <= 5))
        report.comparison = { ...report.comparison, verdict: 'REJECTED', reason: 'The complete suite totals did not exceed 5x.' };
    }
  }
  if (options.trace) report.comparison = { verdict: 'INCONCLUSIVE', adopted: false, reason: 'Diagnostic tracing changes timing. These samples must not decide adoption.' };
  if (options.fault) report.comparison = { verdict: 'INCONCLUSIVE', adopted: false, reason: 'Injected test-owned fixture fault; assertion self-check, not a performance sample or production fault.' };
  if (options.profile) report.comparison = { verdict: 'INCONCLUSIVE', adopted: false, reason: 'Diagnostic profiling changes timing. These samples cannot qualify.' };
  if ((options.trace || options.profile || options.fault) && options.mode === 'throughput') report.steadyComparison = report.comparison;
  if (options.trace || options.profile || options.fault) delete report.suiteComparison;
} catch (error) { report.status = 'failed'; report.error = error.stack; process.exitCode = 1; }
finally {
  stop();
  if (server) await new Promise(resolve => { server.closeAllConnections(); server.close(resolve); });
  await rm(temporary, { recursive: true, force: true });
  report.system.loadAfter = loadavg();
  report.cleanup = { serverStopped: !server?.listening };
  await writeFile(join(out, 'comparison.json'), JSON.stringify(report, null, 2) + '\n');
  const { caseResults, ...comparison } = report.comparison ?? {};
  console.log(JSON.stringify({ status: report.status, comparison, steadySpeedup: report.steadyComparison?.speedup,
    chosenConcurrency: report.chosenConcurrency, report: join(out, 'comparison.json') }));
  if (report.status !== 'passed') process.exitCode = 1;
  else if (options.requireSpeedup && report.comparison?.verdict !== 'CANDIDATE') process.exitCode = 2;
}
