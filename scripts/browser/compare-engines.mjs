#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { readFile, writeFile, mkdir, mkdtemp, rm, readdir } from 'node:fs/promises';
import { dirname, resolve, join, extname } from 'node:path';
import { tmpdir, availableParallelism, loadavg, freemem } from 'node:os';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { cases } from './lightpanda-cases.mjs';
import { compareTrials } from './engine-verdict.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
if (process.argv.includes('--help')) {
  console.log('Usage: node scripts/browser/compare-engines.mjs --out <empty-directory> [--engine compare|chromium|lightpanda] [--runs 1..30] [--case preview-load|preview-identity|disclosure|saved-route] [--chrome <executable>] [--lightpanda <executable>] [--trace] [--fault preview-row] [--require-speedup]\nDefault: five alternating trials per engine. Exit 1 means a failed journey/setup; exit 2 means --require-speedup did not qualify. Trace and fault runs cannot qualify.');
  process.exit(0);
}
const options = { engine: 'compare', runs: 5, lightpanda: process.env.LIGHTPANDA_BIN || 'lightpanda' };
for (let i = 2; i < process.argv.length; i++) {
  if (process.argv[i] === '--trace') { options.trace = true; continue; }
  if (process.argv[i] === '--require-speedup') { options.requireSpeedup = true; continue; }
  const key = { '--engine': 'engine', '--runs': 'runs', '--out': 'out', '--lightpanda': 'lightpanda', '--chrome': 'chrome', '--case': 'case', '--fault': 'fault' }[process.argv[i]];
  assert.ok(key && process.argv[i + 1], `Unknown/missing option: ${process.argv[i]}`);
  options[key] = process.argv[++i];
}
assert.ok(['compare', 'chromium', 'lightpanda'].includes(options.engine), 'Unknown engine');
options.runs = Number(options.runs);
assert.ok(Number.isInteger(options.runs) && options.runs >= 1 && options.runs <= 30, '--runs must be 1..30');
assert.ok(options.out, '--out is required');
assert.ok(!options.fault || options.fault === 'preview-row', 'Unknown fault');
assert.ok(!options.requireSpeedup || options.engine === 'compare', '--require-speedup requires both engines');
const selected = options.case ? cases.filter(item => item.id === options.case) : cases;
assert.ok(selected.length, 'Unknown case');
const out = resolve(options.out);
await mkdir(out, { recursive: true });
assert.deepEqual(await readdir(out), [], 'Refusing to overwrite evidence');
const { chromium } = await import('playwright');
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
const report = {
  schemaVersion: 1, status: 'running', options, sourceCommit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
  sourceFingerprint: inputFingerprint,
  system: { platform: process.platform, arch: process.arch, node: process.version, cpus: availableParallelism(), load: loadavg(), freeMemory: freemem() },
  cases: selected.map(({ run, ...contract }) => contract), trials: [],
  claim: 'Adopt only if the complete equivalent suite is over 5x faster, with at least five alternating trials per engine and no failures.',
};

async function bounded(promise, label, ms = 5000) {
  const controller = new AbortController();
  try {
    return await Promise.race([promise, delay(ms, undefined, { signal: controller.signal }).then(() => { throw new Error(`${label} timed out after ${ms}ms`); })]);
  } finally { controller.abort(); }
}

async function trial(engine, index) {
  const result = { engine, index, cases: [], status: 'running' };
  const started = performance.now();
  let browser, child;
  let stderr = '';
  try {
    if (engine === 'chromium') {
      browser = await chromium.launch({ headless: true, ...(options.chrome ? { executablePath: options.chrome } : {}), timeout: 10000 });
    } else {
      // Let the OS choose a free loopback port, then hand it to the owned process.
      const reservation = createServer();
      await new Promise((resolve, reject) => { reservation.once('error', reject); reservation.listen(0, '127.0.0.1', resolve); });
      const port = reservation.address().port;
      await new Promise(resolve => reservation.close(resolve));
      child = spawn(options.lightpanda, ['serve', '--host', '127.0.0.1', '--port', String(port)], { stdio: ['ignore', 'ignore', 'pipe'] });
      let spawnError;
      child.on('error', error => { spawnError = error; });
      child.stderr.on('data', chunk => { stderr += chunk; });
      const endpoint = `http://127.0.0.1:${port}`;
      const deadline = performance.now() + 10000;
      let ready = false;
      while (performance.now() < deadline) {
        if (spawnError) throw spawnError;
        assert.equal(child.exitCode, null, `Lightpanda exited during startup: ${stderr}`);
        try { ready = (await fetch(`${endpoint}/json/version`, { signal: AbortSignal.timeout(500) })).ok; } catch { /* Not listening yet. */ }
        if (ready) break;
        await delay(10);
      }
      assert.ok(ready, `Lightpanda startup timed out: ${stderr}`);
      browser = await chromium.connectOverCDP(`ws://127.0.0.1:${port}`, { timeout: 10000 });
    }
    result.version = browser.version();
    result.startupMs = performance.now() - started;
    for (const contract of selected) {
      const before = performance.now();
      const record = { id: contract.id, status: 'running', errors: [], responses: [], failedRequests: [] };
      let context;
      try {
        context = await bounded(browser.newContext({ viewport: { width: 1440, height: 1000 } }), 'new context');
        const page = await bounded(context.newPage(), 'new page');
        if (options.trace) await context.tracing.start({ snapshots: true, screenshots: false, sources: false });
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(10000);
        page.on('pageerror', error => record.errors.push(error.message));
        page.on('requestfailed', request => record.failedRequests.push({ url: request.url(), error: request.failure()?.errorText }));
        page.on('response', response => record.responses.push({ url: response.url(), status: response.status() }));
        record.observation = await bounded(contract.run(page, report.origin), 'complete journey', 30000);
        assert.deepEqual(record.errors, [], 'Unexpected page errors');
        assert.deepEqual(record.failedRequests, [], 'Unexpected failed requests');
        assert.ok(record.responses.every(response => response.status < 400), 'Unexpected HTTP errors');
        record.status = 'passed';
      } catch (error) {
        record.status = 'failed'; record.error = error.stack;
      } finally {
        if (context) {
          if (options.trace) {
            try { await bounded(context.tracing.stop({ path: join(out, `${index}-${engine}-${contract.id}.zip`) }), 'trace cleanup'); }
            catch (error) { record.status = 'failed'; record.traceError = error.message; }
          }
          try { await bounded(context.close(), 'context cleanup'); }
          catch (error) { record.status = 'failed'; record.cleanupError = error.message; }
        }
        record.durationMs = performance.now() - before;
        result.cases.push(record);
      }
    }
    result.status = result.cases.every(item => item.status === 'passed') ? 'passed' : 'failed';
  } catch (error) { result.status = 'failed'; result.error = error.stack; }
  finally {
    if (browser) {
      try { await bounded(browser.close(), 'browser cleanup'); }
      catch (error) { result.status = 'failed'; result.cleanupError = error.message; }
    }
    if (child && child.exitCode === null && child.signalCode === null) {
      const exited = new Promise(resolve => child.once('exit', resolve));
      child.kill('SIGTERM');
      try { await bounded(exited, 'Lightpanda shutdown', 2000); }
      catch { child.kill('SIGKILL'); await bounded(exited, 'Lightpanda forced shutdown'); }
    }
    result.durationMs = performance.now() - started;
    result.cleanup = { browserConnected: browser?.isConnected() ?? false, lightpandaStopped: !child || child.exitCode !== null || child.signalCode !== null };
    if (result.cleanup.browserConnected || !result.cleanup.lightpandaStopped) result.status = 'failed';
    await writeFile(join(out, `${index}-${engine}.json`), JSON.stringify(result, null, 2) + '\n');
    if (stderr) await writeFile(join(out, `${index}-${engine}.stderr`), stderr);
    report.trials.push(result);
    console.log(JSON.stringify({ engine, index, status: result.status, durationMs: result.durationMs, cases: result.cases.map(({ id, status, durationMs }) => ({ id, status, durationMs })) }));
  }
}

try {
  if (options.engine !== 'chromium') report.lightpandaVersion = execFileSync(options.lightpanda, ['version'], { encoding: 'utf8', timeout: 3000 }).trim();
  const buildStart = performance.now();
  const plugins = options.fault ? [{ name: 'wrong-fixture-row', setup(build) {
    build.onLoad({ filter: /testSupport\/browser\/fixture\.tsx$/ }, async ({ path }) => {
      const source = await readFile(path, 'utf8');
      assert.equal(source.split("'patient-A'").length, 2, 'Fault must replace exactly one fixture row');
      return { contents: source.replace("'patient-A'", "'wrong-patient'"), loader: 'tsx', resolveDir: dirname(path) };
    });
  } }] : [];
  await build({ absWorkingDir: root, entryPoints: ['ui/packages/loom-ui/testSupport/browser/fixture.tsx'], bundle: true, format: 'esm', outdir: temporary, entryNames: 'app', jsx: 'automatic', logLevel: 'silent', define: { 'process.env.NODE_ENV': '"production"' }, nodePaths: [join(root, 'ui/node_modules')], plugins });
  stop();
  const [{ default: postcss }, { default: tailwind }] = await Promise.all([
    import('../../ui/node_modules/postcss/lib/postcss.mjs'), import('../../ui/node_modules/tailwindcss/lib/index.js'),
  ]);
  const css = await postcss([tailwind({ content: [join(root, 'ui/packages/loom-ui/src/**/*.tsx'), join(root, 'ui/packages/loom-ui/testSupport/browser/fixture.tsx')], theme: { extend: {} }, plugins: [] })])
    .process(await readFile(join(temporary, 'app.css'), 'utf8'), { from: join(temporary, 'app.css') });
  await writeFile(join(temporary, 'app.css'), css.css);
  report.buildMs = performance.now() - buildStart;
  report.assets = {};
  for (const name of ['app.js', 'app.css']) { const bytes = await readFile(join(temporary, name)); report.assets[name] = { bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') }; }
  server = createServer(async (req, res) => {
    try {
      const path = new URL(req.url, 'http://localhost').pathname;
      if (['/app.js', '/app.css'].includes(path)) {
        res.setHeader('Content-Type', extname(path) === '.css' ? 'text/css' : 'text/javascript');
        res.end(await readFile(join(temporary, path.slice(1))));
      } else if (path === '/') {
        res.setHeader('Content-Type', 'text/html');
        res.end('<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>');
      } else { res.statusCode = 404; res.end('not found'); }
    } catch (error) { res.statusCode = 500; res.end(error.message); }
  });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  report.origin = `http://127.0.0.1:${server.address().port}`;
  for (let index = 0; index < options.runs; index++) {
    const engines = options.engine === 'compare' ? (index % 2 ? ['lightpanda', 'chromium'] : ['chromium', 'lightpanda']) : [options.engine];
    for (const engine of engines) await trial(engine, index);
  }
  assert.equal(await fingerprint(), inputFingerprint, 'Sources changed during comparison');
  const allPassed = report.trials.every(item => item.status === 'passed');
  report.status = allPassed ? 'passed' : 'failed';
  report.comparison = compareTrials({ trials: report.trials, caseIds: selected.map(item => item.id), buildMs: report.buildMs, completeSelection: !options.case });
  if (options.trace) report.comparison = { verdict: 'INCONCLUSIVE', adopted: false, reason: 'Diagnostic tracing changes timing. These samples must not decide adoption.' };
  if (options.fault) report.comparison = { verdict: 'INCONCLUSIVE', adopted: false, reason: 'Injected test-owned fixture fault; assertion self-check, not a performance sample or production fault.' };
} catch (error) { report.status = 'failed'; report.error = error.stack; process.exitCode = 1; }
finally {
  stop();
  if (server) await new Promise(resolve => { server.closeAllConnections(); server.close(resolve); });
  await rm(temporary, { recursive: true, force: true });
  report.system.loadAfter = loadavg();
  report.cleanup = { serverStopped: !server?.listening };
  await writeFile(join(out, 'comparison.json'), JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify({ status: report.status, comparison: report.comparison, report: join(out, 'comparison.json') }));
  if (report.status !== 'passed') process.exitCode = 1;
  else if (options.requireSpeedup && report.comparison?.verdict !== 'CANDIDATE') process.exitCode = 2;
}
