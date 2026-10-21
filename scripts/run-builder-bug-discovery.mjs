#!/usr/bin/env node
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { ROOT, readJson } from './lib/builder-route-evidence.mjs';

const PROBES = 'verification/builder-interactions/mutation-probes.json';
const EVIDENCE = 'verification/builder-interactions/mutation-evidence.json';
const OBLIGATIONS = 'verification/builder-interactions/test-obligations.json';
const OUTPUT_JSON = 'verification/builder-interactions/bug-discovery.json';
const OUTPUT_MD = 'verification/builder-interactions/bug-discovery.md';

const args = process.argv.slice(2);
const run = args.includes('--run');
const strict = args.includes('--strict');
const check = args.includes('--check');

if (run) {
  const result = spawnSync(process.execPath, ['scripts/run-builder-mutation-probe.mjs', '--all'], {
    cwd: ROOT,
    stdio: 'inherit',
  });
  if (result.status !== 0) process.exit(result.status ?? 1);
}

const probes = readJson(PROBES).probes ?? [];
const evidence = readJson(EVIDENCE);
const obligations = readJson(OBLIGATIONS);
const evidenceById = new Map((evidence.probes ?? []).map((entry) => [entry.id, entry]));
const results = probes.map((probe) => {
  const observed = evidenceById.get(probe.id);
  return {
    id: probe.id,
    modelAction: probe.modelAction ?? null,
    source: probe.file,
    expectedFailure: probe.expectedFailure,
    status: observed?.status ?? 'missing',
    mutation: observed?.mutation ?? null,
    failure: observed?.failure ?? null,
    equivalent: probe.equivalent ?? observed?.equivalent ?? null,
  };
});
const survivors = results.filter((entry) => entry.status === 'survived');
const missing = results.filter((entry) => entry.status === 'missing');
const killed = results.filter((entry) => entry.status === 'killed');
const equivalent = results.filter((entry) => entry.status === 'equivalent');
const actionObligations = (obligations.obligations ?? []).filter((entry) => entry.kind === 'action');
const probedActions = new Set(results.map((entry) => entry.modelAction).filter(Boolean));
const unprobedActions = actionObligations
  .filter((obligation) => !probedActions.has(obligation.id))
  .map((obligation) => obligation.id)
  .sort();
const summary = {
  probes: probes.length,
  evidence: results.filter((entry) => entry.status !== 'missing').length,
  killed: killed.length,
  survived: survivors.length,
  equivalent: equivalent.length,
  missing: missing.length,
  strictPass: survivors.length === 0 && missing.length === 0,
  actionObligations: actionObligations.length,
  probedActions: probedActions.size,
  unprobedActions: unprobedActions.length,
};
const payload = {
  schemaVersion: 1,
  generatedFrom: { probes: PROBES, evidence: EVIDENCE, obligations: OBLIGATIONS },
  summary,
  probes: results,
  survivors,
  unprobedActions,
};
const serialized = JSON.stringify(payload, null, 2) + '\n';
const lines = [];
lines.push('# Builder bug discovery');
lines.push('');
lines.push('Status: generated from the mutation probes and retained evidence.');
lines.push('');
lines.push('## Summary');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(summary)) lines.push('| ' + key + ' | ' + (typeof value === 'boolean' ? (value ? 'yes' : 'no') : value) + ' |');
lines.push('');
lines.push('## Probes');
lines.push('');
lines.push('| Probe | Model action | Status | Expected failure |');
lines.push('| --- | --- | --- | --- |');
for (const entry of results) lines.push('| `' + entry.id + '` | `' + (entry.modelAction ?? 'unmapped') + '` | ' + entry.status + ' | ' + entry.expectedFailure + ' |');
lines.push('');
lines.push('## Survivors');
lines.push('');
if (survivors.length === 0) lines.push('No surviving probe.');
for (const entry of survivors) lines.push('- `' + entry.id + '` mutated `' + entry.source + '`. The focused test did not fail with `' + entry.expectedFailure + '`.');
lines.push('');
lines.push('## Actions without a mutation probe');
lines.push('');
for (const action of unprobedActions.slice(0, 40)) lines.push('- `' + action + '`');
if (unprobedActions.length > 40) lines.push('- ' + (unprobedActions.length - 40) + ' more in the JSON report.');
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('The probes are hand-authored frontend guards. The report proves which guards have retained mutation evidence. It does not generate model-derived mutants or backend probes yet.');
const markdown = lines.join('\n') + '\n';

if (check) {
  const currentJson = existsSync(resolve(ROOT, OUTPUT_JSON)) ? readFileSync(resolve(ROOT, OUTPUT_JSON), 'utf8') : null;
  const currentMd = existsSync(resolve(ROOT, OUTPUT_MD)) ? readFileSync(resolve(ROOT, OUTPUT_MD), 'utf8') : null;
  if (currentJson !== serialized || currentMd !== markdown) {
    console.error('FAIL ' + OUTPUT_JSON + ' or ' + OUTPUT_MD + ' is stale; run node scripts/run-builder-bug-discovery.mjs');
    process.exit(1);
  }
  console.log('PASS ' + OUTPUT_JSON + ' is current: ' + summary.killed + ' killed, ' + summary.survived + ' survived, ' + summary.unprobedActions + ' actions without a probe');
} else {
  writeFileSync(resolve(ROOT, OUTPUT_JSON), serialized);
  writeFileSync(resolve(ROOT, OUTPUT_MD), markdown);
  console.log('wrote ' + OUTPUT_JSON + ' and ' + OUTPUT_MD + ' with ' + summary.killed + ' killed, ' + summary.survived + ' survived, and ' + summary.unprobedActions + ' unprobed actions');
}
if (strict && !summary.strictPass) process.exit(1);
