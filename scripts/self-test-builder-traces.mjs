#!/usr/bin/env node
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const args = process.argv.slice(2);
const valueFor = (name) => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
};
const tracesPath = valueFor('--traces');
const contractPath = valueFor('--contract') ?? 'verification/builder-interactions/builder-trace-contract.json';
if (!tracesPath) {
  console.error('usage: node scripts/self-test-builder-traces.mjs --traces <traces.json> [--contract <contract.json>]');
  process.exit(1);
}

const payload = JSON.parse(readFileSync(resolve(ROOT, tracesPath), 'utf8'));
const contract = JSON.parse(readFileSync(resolve(ROOT, contractPath), 'utf8'));
if (!Array.isArray(payload.traces) || payload.traces.length === 0) {
  console.error('FAIL trace corpus is empty');
  process.exit(1);
}
const requiredEvent = (contract.phase1?.requiredEvents ?? []).find((event) =>
  payload.traces.some((trace) => trace.actions?.some((action) => action.event === event)),
);
if (!requiredEvent) {
  console.error('FAIL no required event is present in the trace corpus');
  process.exit(1);
}

const workDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-trace-self-test-'));
const runMutation = (label, mutate, expectedText) => {
  const mutated = JSON.parse(JSON.stringify(payload));
  mutate(mutated);
  if (JSON.stringify(mutated) === JSON.stringify(payload)) {
    console.error('FAIL ' + label + ': mutation did not change the trace corpus');
    return false;
  }
  const path = join(workDirectory, label + '.json');
  writeFileSync(path, JSON.stringify(mutated, null, 2) + '\n');
  const result = spawnSync(process.execPath, [
    'scripts/verify-builder-traces.mjs',
    '--traces',
    path,
    '--contract',
    contractPath,
  ], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  const output = (result.stdout || '') + (result.stderr || '');
  if (result.status === 0) {
    console.error('FAIL ' + label + ': validator accepted the mutation');
    return false;
  }
  if (expectedText && !output.includes(expectedText)) {
    console.error('FAIL ' + label + ': validator failed for an unexpected reason');
    console.error(output.split('\n').slice(-20).join('\n'));
    return false;
  }
  console.log('PASS ' + label + ': validator rejected the mutation');
  return true;
};

let passed = true;
try {
  passed = runMutation('observation-mismatch', (mutated) => {
    for (const trace of mutated.traces) {
      for (const action of trace.actions ?? []) {
        if (action.observation?.status === 'ready') {
          action.observation.status = 'needs-repair';
          return;
        }
      }
    }
  }, 'trace validation failed') && passed;

  passed = runMutation('wrong-model', (mutated) => {
    const trace = mutated.traces.find((candidate) => candidate.model === 'BuilderPreview');
    if (trace) trace.model = 'ConstructionLifecycle';
  }, 'is not an action of ConstructionLifecycle') && passed;

  passed = runMutation('missing-required-event', (mutated) => {
    for (const trace of mutated.traces) {
      trace.actions = (trace.actions ?? []).filter((action) => action.event !== requiredEvent);
    }
  }, 'required events missing: ' + requiredEvent) && passed;
} finally {
  rmSync(workDirectory, { recursive: true, force: true });
}

if (!passed) process.exit(1);
console.log('PASS builder trace validator rejected every mutation');
