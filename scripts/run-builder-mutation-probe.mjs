#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const PROBES = 'verification/builder-interactions/mutation-probes.json';
const EVIDENCE = 'verification/builder-interactions/mutation-evidence.json';
const requested = process.argv[2];
if (!requested || requested === '--help') {
  console.log('usage: node scripts/run-builder-mutation-probe.mjs <probe-id|--all>');
  process.exit(requested ? 0 : 2);
}

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const fail = (message) => {
  console.error('FAIL ' + message);
  process.exit(1);
};
const payload = JSON.parse(readFileSync(resolve(ROOT, PROBES), 'utf8'));
const probes = requested === '--all' ? payload.probes : payload.probes.filter((probe) => probe.id === requested);
if (probes.length === 0) {
  console.error('unknown probe ' + requested);
  process.exit(2);
}

const sourceCommit = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: ROOT, encoding: 'utf8' }).stdout.trim() || null;
const evidence = existsSync(resolve(ROOT, EVIDENCE))
  ? JSON.parse(readFileSync(resolve(ROOT, EVIDENCE), 'utf8'))
  : { schemaVersion: 2, probes: [] };
evidence.schemaVersion = 2;
evidence.sourceCommit = sourceCommit;
evidence.probes ??= [];

for (const probe of probes) {
  const runner = probe.runner ?? 'vitest';
  if (!['vitest', 'go'].includes(runner)) fail('probe ' + probe.id + ': unknown runner ' + runner);
  const target = resolve(ROOT, probe.file);
  if (!existsSync(target)) fail('probe ' + probe.id + ': target does not exist: ' + probe.file);
  const source = readFileSync(target, 'utf8');
  if (!source.includes(probe.find)) fail('probe ' + probe.id + ': mutation target not found');
  const mutated = source.replace(probe.find, probe.replace);
  if (mutated === source) fail('probe ' + probe.id + ': mutation did not change the source');
  if (!probe.expectedFailure) fail('probe ' + probe.id + ': expectedFailure is required');

  let result;
  let command;
  if (runner === 'go') {
    const copy = mkdtempSync(join(tmpdir(), 'loom-mutation-go-'));
    const replacement = join(copy, basename(probe.file));
    const overlay = join(copy, 'overlay.json');
    writeFileSync(replacement, mutated);
    writeFileSync(overlay, JSON.stringify({ Replace: { [target]: replacement } }));
    const packagePath = probe.test ?? './internal/server';
    const args = ['test', '-count=1', '-overlay', overlay];
    if (probe.testNamePattern) args.push('-run', '^(' + probe.testNamePattern + ')$');
    args.push(packagePath);
    result = spawnSync('go', args, {
      cwd: ROOT,
      encoding: 'utf8',
      maxBuffer: 32 * 1024 * 1024,
      env: { ...process.env, GOCACHE: join(copy, 'go-cache') },
    });
    command = 'GOCACHE=<tmp> go ' + args.join(' ');
    rmSync(copy, { recursive: true, force: true });
  } else {
    const copy = mkdtempSync(join(tmpdir(), 'loom-mutation-'));
    cpSync(ROOT, copy, {
      recursive: true,
      filter: (sourcePath) => !sourcePath.includes('/.git') && !sourcePath.includes('/node_modules'),
    });
    symlinkSync(resolve(ROOT, 'ui/node_modules'), resolve(copy, 'ui/node_modules'), 'dir');
    const targetCopy = resolve(copy, probe.file);
    writeFileSync(targetCopy, mutated);
    const testArgs = ['run', probe.test];
    if (probe.testNamePattern) testArgs.push('-t', probe.testNamePattern);
    result = spawnSync(process.execPath, [
      resolve(ROOT, 'ui/node_modules/vitest/vitest.mjs'),
      ...testArgs,
    ], {
      cwd: resolve(copy, 'ui/packages/loom-ui'),
      encoding: 'utf8',
      maxBuffer: 32 * 1024 * 1024,
    });
    command = 'vitest run ' + probe.test + (probe.testNamePattern ? ' -t "' + probe.testNamePattern + '"' : '');
    rmSync(copy, { recursive: true, force: true });
  }
  if (result.error) fail('probe ' + probe.id + ': ' + result.error.message);
  const output = (result.stdout || '') + (result.stderr || '');
  if (result.status !== 0 && !output.includes(probe.expectedFailure)) {
    fail('probe ' + probe.id + ': test failed without the expected failure `' + probe.expectedFailure + '`; first output: ' + (output.split('\n').find(Boolean) ?? 'none'));
  }
  const killed = result.status !== 0 && output.includes(probe.expectedFailure);
  const status = killed ? 'killed' : probe.equivalent ? 'equivalent' : 'survived';
  const failureLine = output.split('\n').find((line) => line.includes(probe.expectedFailure)) ?? '';
  const entry = {
    id: probe.id,
    status,
    runner,
    source: probe.file,
    sourceHash: sha256(source),
    sourceCommit,
    modelAction: probe.modelAction ?? null,
    ...(probe.modelActionRationale ? { modelActionRationale: probe.modelActionRationale } : {}),
    mutation: 'Replace `' + probe.find.replaceAll('\n', '\\n') + '` with `' + probe.replace.replaceAll('\n', '\\n') + '`.',
    command,
    expectedFailure: probe.expectedFailure,
    exitCode: result.status,
    failure: failureLine.trim(),
    ...(probe.equivalent ? { equivalent: probe.equivalent } : {}),
  };
  const existing = evidence.probes.findIndex((item) => item.id === probe.id);
  if (existing === -1) evidence.probes.push(entry);
  else evidence.probes[existing] = entry;
  console.log(status.toUpperCase() + ' ' + probe.id);
}

writeFileSync(resolve(ROOT, EVIDENCE), JSON.stringify(evidence, null, 2) + '\n');
