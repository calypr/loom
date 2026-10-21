#!/usr/bin/env node
import { cpSync, existsSync, mkdtempSync, readFileSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const PROBES = 'verification/builder-interactions/mutation-probes.json';
const EVIDENCE = 'verification/builder-interactions/mutation-evidence.json';
const requested = process.argv[2];
if (!requested || requested === '--help') {
  console.log('usage: node scripts/run-builder-mutation-probe.mjs <probe-id|--all>');
  process.exit(requested ? 0 : 2);
}

const payload = JSON.parse(readFileSync(resolve(ROOT, PROBES), 'utf8'));
const probes = requested === '--all' ? payload.probes : payload.probes.filter((probe) => probe.id === requested);
if (probes.length === 0) {
  console.error('unknown probe ' + requested);
  process.exit(2);
}

const evidence = existsSync(resolve(ROOT, EVIDENCE))
  ? JSON.parse(readFileSync(resolve(ROOT, EVIDENCE), 'utf8'))
  : { schemaVersion: 1, baselineCommit: 'cd02d1306', probes: [] };

for (const probe of probes) {
  const copy = mkdtempSync(join(tmpdir(), 'loom-mutation-'));
  cpSync(ROOT, copy, {
    recursive: true,
    filter: (source) => !source.includes('/.git') && !source.includes('/node_modules'),
  });
  symlinkSync(resolve(ROOT, 'ui/node_modules'), resolve(copy, 'ui/node_modules'), 'dir');
  const target = resolve(copy, probe.file);
  const source = readFileSync(target, 'utf8');
  if (!source.includes(probe.find)) {
    console.error('probe ' + probe.id + ': mutation target not found');
    process.exit(1);
  }
  writeFileSync(target, source.replace(probe.find, probe.replace));
  const testArgs = ['run', probe.test];
  if (probe.testNamePattern) testArgs.push('-t', probe.testNamePattern);
  const result = spawnSync(process.execPath, [
    resolve(ROOT, 'ui/node_modules/vitest/vitest.mjs'),
    ...testArgs,
  ], {
    cwd: resolve(copy, 'ui/packages/loom-ui'),
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  const output = (result.stdout || '') + (result.stderr || '');
  const killed = result.status !== 0 && output.includes(probe.expectedFailure);
  const status = killed ? 'killed' : probe.equivalent ? 'equivalent' : 'survived';
  const failureLine = output.split('\n').find((line) => line.includes(probe.expectedFailure)) ?? '';
  const entry = {
    id: probe.id,
    status,
    source: probe.file,
    mutation: 'Replace `' + probe.find.replaceAll('\n', '\\n') + '` with `' + probe.replace.replaceAll('\n', '\\n') + '`.',
    command: 'vitest run ' + probe.test + (probe.testNamePattern ? ' -t "' + probe.testNamePattern + '"' : ''),
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
