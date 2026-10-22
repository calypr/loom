#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { cpus, tmpdir } from 'node:os';
import { basename, dirname, join, resolve, sep } from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const PROBES = 'verification/builder-interactions/mutation-probes.json';
const EVIDENCE = 'verification/builder-interactions/mutation-evidence.json';
const args = process.argv.slice(2);
const requested = args.find((arg) => arg === '--all' || !arg.startsWith('--'));
if (!requested || requested === '--help') {
  console.log('usage: node scripts/run-builder-mutation-probe.mjs <probe-id|--all> [--force] [--jobs=N]');
  process.exit(requested ? 0 : 2);
}
const force = args.includes('--force');
const jobsArg = args.find((arg) => arg.startsWith('--jobs='));
const jobs = Math.max(1, Math.min(16, Number(jobsArg?.slice(7) ?? process.env.LOOM_MUTATION_JOBS ?? Math.min(4, cpus().length)) || 1));

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const fileHash = (path) => existsSync(path) ? sha256(readFileSync(path)) : null;
const payload = JSON.parse(readFileSync(resolve(ROOT, PROBES), 'utf8'));
const probes = requested === '--all' ? payload.probes : payload.probes.filter((probe) => probe.id === requested);
if (probes.length === 0) {
  console.error('unknown probe ' + requested);
  process.exit(2);
}

const sourceCommit = await spawnCaptured('git', ['rev-parse', 'HEAD'], { cwd: ROOT }).then((result) => result.output.trim() || null);
const evidence = existsSync(resolve(ROOT, EVIDENCE))
  ? JSON.parse(readFileSync(resolve(ROOT, EVIDENCE), 'utf8'))
  : { schemaVersion: 2, probes: [] };
evidence.schemaVersion = 2;
evidence.sourceCommit = sourceCommit;
evidence.probes ??= [];
const evidenceById = new Map(evidence.probes.map((entry) => [entry.id, entry]));

function spawnCaptured(command, commandArgs, options) {
  return new Promise((resolvePromise) => {
    const child = spawn(command, commandArgs, { ...options, stdio: ['ignore', 'pipe', 'pipe'] });
    const stdout = [];
    const stderr = [];
    let bytes = 0;
    const capture = (chunks) => (chunk) => {
      if (bytes >= 32 * 1024 * 1024) return;
      bytes += chunk.length;
      chunks.push(chunk);
    };
    child.stdout.on('data', capture(stdout));
    child.stderr.on('data', capture(stderr));
    child.on('error', (error) => resolvePromise({ status: null, error, output: Buffer.concat([...stdout, ...stderr]).toString('utf8') }));
    child.on('close', (status) => resolvePromise({ status, output: Buffer.concat([...stdout, ...stderr]).toString('utf8') }));
  });
}

function probeHash(probe) {
  return sha256(JSON.stringify({
    runner: probe.runner ?? 'vitest',
    file: probe.file,
    test: probe.test,
    testNamePattern: probe.testNamePattern ?? null,
    find: probe.find,
    replace: probe.replace,
    expectedFailure: probe.expectedFailure,
    equivalent: probe.equivalent ?? null,
  }));
}

function testHash(probe) {
  if ((probe.runner ?? 'vitest') === 'go' || probe.test.startsWith('./')) {
    const packagePath = resolve(ROOT, probe.test ?? './internal/server');
    if (!existsSync(packagePath)) return null;
    if (!statSync(packagePath).isDirectory()) return fileHash(packagePath);
    const files = readdirSync(packagePath).filter((name) => name.endsWith('.go')).sort();
    return sha256(files.map((name) => name + '\n' + readFileSync(join(packagePath, name), 'utf8')).join('\n') + '\n' + (probe.testNamePattern ?? ''));
  }
  const packageRoot = resolve(ROOT, 'ui/packages/loom-ui');
  const testPath = resolve(packageRoot, probe.test);
  if (!testPath.startsWith(packageRoot + sep)) return null;
  return sha256([
    fileHash(testPath) ?? '',
    fileHash(resolve(packageRoot, 'vitest.config.ts')) ?? '',
    fileHash(resolve(packageRoot, 'vitest.setup.ts')) ?? '',
    probe.testNamePattern ?? '',
  ].join('\n'));
}

function hashesFor(probe) {
  return {
    sourceHash: fileHash(resolve(ROOT, probe.file)),
    testHash: testHash(probe),
    probeHash: probeHash(probe),
  };
}

function isCached(entry, hashes) {
  if (force || !entry || !['killed', 'equivalent'].includes(entry.status)) return false;
  return entry.sourceHash === hashes.sourceHash
    && entry.testHash === hashes.testHash
    && entry.probeHash === hashes.probeHash;
}

async function runVitest(probe, target, mutated) {
  const temporary = mkdtempSync(join(tmpdir(), 'loom-mutation-vitest-'));
  try {
    const mutant = join(temporary, basename(probe.file));
    writeFileSync(mutant, mutated);
    const testArgs = ['run', probe.test];
    if (probe.testNamePattern) testArgs.push('-t', probe.testNamePattern);
    const result = await spawnCaptured(process.execPath, [
      resolve(ROOT, 'ui/node_modules/vitest/vitest.mjs'),
      ...testArgs,
    ], {
      cwd: resolve(ROOT, 'ui/packages/loom-ui'),
      env: {
        ...process.env,
        LOOM_MUTATION_TARGET: target,
        LOOM_MUTATION_SOURCE: mutant,
        LOOM_VITEST_CACHE_DIR: join(temporary, 'vite-cache'),
      },
    });
    return { ...result, command: 'vitest ' + testArgs.join(' ') };
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}

async function runGo(probe, target, mutated) {
  const temporary = mkdtempSync(join(tmpdir(), 'loom-mutation-go-'));
  try {
    const replacement = join(temporary, basename(probe.file));
    writeFileSync(replacement, mutated);
    const overlay = join(temporary, 'overlay.json');
    writeFileSync(overlay, JSON.stringify({ Replace: { [target]: replacement } }));
    const goCache = process.env.LOOM_MUTATION_GO_CACHE
      ?? (existsSync(resolve(ROOT, 'ui/node_modules'))
        ? resolve(ROOT, 'ui/node_modules/.cache/loom-mutation-go')
        : join(tmpdir(), 'loom-mutation-go-cache'));
    mkdirSync(goCache, { recursive: true });
    const testArgs = ['test', '-count=1', '-overlay', overlay];
    if (probe.testNamePattern) testArgs.push('-run', '^(' + probe.testNamePattern + ')$');
    testArgs.push(probe.test ?? './internal/server');
    const result = await spawnCaptured('go', testArgs, {
      cwd: ROOT,
      env: { ...process.env, GOCACHE: goCache },
    });
    return { ...result, command: 'GOCACHE=<shared> go ' + testArgs.join(' ') };
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}

async function runProbe(probe) {
  const runner = probe.runner ?? 'vitest';
  if (!['vitest', 'go'].includes(runner)) return { probe, error: 'unknown runner ' + runner };
  const target = resolve(ROOT, probe.file);
  if (!existsSync(target)) return { probe, error: 'target does not exist: ' + probe.file };
  const source = readFileSync(target, 'utf8');
  if (!source.includes(probe.find)) return { probe, error: 'mutation target not found' };
  const mutated = source.replace(probe.find, probe.replace);
  if (mutated === source) return { probe, error: 'mutation did not change the source' };
  if (!probe.expectedFailure) return { probe, error: 'expectedFailure is required' };

  const result = runner === 'go'
    ? await runGo(probe, target, mutated)
    : await runVitest(probe, target, mutated);
  if (result.error) return { probe, error: result.error.message };
  const output = result.output;
  const killed = result.status !== 0 && output.includes(probe.expectedFailure);
  if (result.status !== 0 && !killed) {
    return { probe, error: 'test failed without the expected failure `' + probe.expectedFailure + '`; output:\n' + output.split('\n').slice(0, 40).join('\n') };
  }
  const status = killed ? 'killed' : probe.equivalent ? 'equivalent' : 'survived';
  const failureLine = output.split('\n').find((line) => line.includes(probe.expectedFailure)) ?? '';
  const hashes = hashesFor(probe);
  return {
    probe,
    entry: {
      id: probe.id,
      status,
      runner,
      source: probe.file,
      sourceHash: hashes.sourceHash,
      testHash: hashes.testHash,
      probeHash: hashes.probeHash,
      sourceCommit,
      modelAction: probe.modelAction ?? null,
      ...(probe.modelActionRationale ? { modelActionRationale: probe.modelActionRationale } : {}),
      mutation: 'Replace `' + probe.find.replaceAll('\n', '\\n') + '` with `' + probe.replace.replaceAll('\n', '\\n') + '`.',
      command: result.command,
      expectedFailure: probe.expectedFailure,
      exitCode: result.status,
      failure: failureLine.trim(),
      ...(probe.equivalent ? { equivalent: probe.equivalent } : {}),
    },
  };
}

const pending = [];
const skipped = [];
for (const probe of probes) {
  const hashes = hashesFor(probe);
  const cached = evidenceById.get(probe.id);
  if (isCached(cached, hashes)) skipped.push(probe);
  else pending.push(probe);
}

const results = new Array(pending.length);
let next = 0;
await Promise.all(Array.from({ length: Math.min(jobs, pending.length) }, async () => {
  while (true) {
    const index = next++;
    if (index >= pending.length) return;
    results[index] = await runProbe(pending[index]);
  }
}));

let failures = 0;
for (const result of results) {
  if (result.error) {
    failures += 1;
    console.error('FAIL ' + result.probe.id + ': ' + result.error);
  } else {
    evidenceById.set(result.probe.id, result.entry);
    console.log(result.entry.status.toUpperCase() + ' ' + result.probe.id);
  }
}
for (const probe of skipped) console.log('SKIP ' + probe.id + ' (unchanged evidence)');
evidence.probes = [...evidenceById.values()];
writeFileSync(resolve(ROOT, EVIDENCE), JSON.stringify(evidence, null, 2) + '\n');
process.exit(failures === 0 ? 0 : 1);
