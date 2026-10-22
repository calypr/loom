#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { ROOT, readJson } from './lib/builder-route-evidence.mjs';

const PROBES = 'verification/builder-interactions/mutation-probes.json';
const EVIDENCE = 'verification/builder-interactions/mutation-evidence.json';
const ALLOWLIST = 'verification/builder-interactions/mutation-strict-allowlist.json';
const OBLIGATIONS = 'verification/builder-interactions/test-obligations.json';
const OUTPUT_JSON = 'verification/builder-interactions/bug-discovery.json';
const OUTPUT_MD = 'verification/builder-interactions/bug-discovery.md';

const args = process.argv.slice(2);
const run = args.includes('--run');
const strict = args.includes('--strict');
const check = args.includes('--check');
const sha256 = (value) => createHash('sha256').update(value).digest('hex');

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
const allowlist = existsSync(resolve(ROOT, ALLOWLIST)) ? readJson(ALLOWLIST) : { survivors: [] };
const evidenceById = new Map((evidence.probes ?? []).map((entry) => [entry.id, entry]));
const allowlisted = new Map((allowlist.survivors ?? []).map((entry) => [entry.id, entry]));
const triageErrors = [];

for (const entry of allowlist.survivors ?? []) {
  if (!probes.some((probe) => probe.id === entry.id)) triageErrors.push('allowlist entry has no probe: ' + entry.id);
  if (!entry.reason) triageErrors.push('allowlist entry has no reason: ' + entry.id);
}

const results = probes.map((probe) => {
  const observed = evidenceById.get(probe.id);
  const modelActions = [...new Set([
    ...(probe.modelActions ?? []),
    ...(probe.modelAction ? [probe.modelAction] : []),
    ...(observed?.modelActions ?? []),
    ...(observed?.modelAction ? [observed.modelAction] : []),
  ])];
  const sourcePath = resolve(ROOT, probe.file);
  const currentHash = existsSync(sourcePath) ? sha256(readFileSync(sourcePath)) : null;
  const sourceHash = observed?.sourceHash ?? null;
  const sourceMatches = sourceHash ? (currentHash ? sourceHash === currentHash : false) : null;
  const status = observed?.status ?? 'missing';
  if (status === 'equivalent' && !observed?.equivalent) triageErrors.push('equivalent probe has no justification: ' + probe.id);
  if (status === 'survived' && observed?.equivalent) triageErrors.push('survivor carries an equivalent justification: ' + probe.id);
  if (status === 'killed' && !observed?.failure) triageErrors.push('killed probe has no retained failure line: ' + probe.id);
  return {
    id: probe.id,
    runner: observed?.runner ?? probe.runner ?? 'vitest',
    modelAction: modelActions[0] ?? null,
    modelActions,
    modelActionRationale: probe.modelActionRationale ?? observed?.modelActionRationale ?? null,
    source: probe.file,
    sourceHash,
    sourceMatches,
    expectedFailure: probe.expectedFailure,
    status,
    mutation: observed?.mutation ?? null,
    failure: observed?.failure ?? null,
    equivalent: observed?.equivalent ?? probe.equivalent ?? null,
    allowlisted: allowlisted.has(probe.id),
  };
});

const survivors = results.filter((entry) => entry.status === 'survived');
const allowedSurvivors = survivors.filter((entry) => entry.allowlisted);
const unallowedSurvivors = survivors.filter((entry) => !entry.allowlisted);
const missing = results.filter((entry) => entry.status === 'missing');
const unhashedSources = results.filter((entry) => !entry.sourceHash);
const staleSources = results.filter((entry) => entry.sourceMatches === false);
const killed = results.filter((entry) => entry.status === 'killed');
const equivalent = results.filter((entry) => entry.status === 'equivalent');
const actionObligations = (obligations.obligations ?? []).filter((entry) => entry.kind === 'action');
const probesByAction = new Map();
for (const entry of results) {
  for (const modelAction of entry.modelActions) {
    if (!probesByAction.has(modelAction)) probesByAction.set(modelAction, []);
    probesByAction.get(modelAction).push(entry.id);
  }
}
const actionCoverage = actionObligations.map((obligation) => {
  const probeIds = [...new Set(probesByAction.get(obligation.id) ?? [])].sort();
  return {
    id: obligation.id,
    model: obligation.model,
    routeBinding: obligation.routeBinding,
    probes: probeIds,
    covered: probeIds.length > 0,
  };
});
const coveredActions = actionCoverage.filter((entry) => entry.covered);
const unprobedActions = actionCoverage.filter((entry) => !entry.covered).map((entry) => entry.id).sort();
const summary = {
  probes: probes.length,
  evidence: results.filter((entry) => entry.status !== 'missing').length,
  killed: killed.length,
  survived: survivors.length,
  allowedSurvivors: allowedSurvivors.length,
  unallowedSurvivors: unallowedSurvivors.length,
  equivalent: equivalent.length,
  missing: missing.length,
  unhashedSources: unhashedSources.length,
  staleSources: staleSources.length,
  triageErrors: triageErrors.length,
  strictPass: unallowedSurvivors.length === 0 && missing.length === 0 && unhashedSources.length === 0 && staleSources.length === 0 && triageErrors.length === 0,
  actionObligations: actionObligations.length,
  probedActions: coveredActions.length,
  unprobedActions: unprobedActions.length,
};
const payload = {
  schemaVersion: 2,
  generatedFrom: { probes: PROBES, evidence: EVIDENCE, allowlist: ALLOWLIST, obligations: OBLIGATIONS },
  sourceCommit: evidence.sourceCommit ?? null,
  summary,
  allowlist: [...allowlisted.values()],
  probes: results,
  survivors: {
    allowed: allowedSurvivors.map((entry) => ({ id: entry.id, reason: allowlisted.get(entry.id)?.reason ?? null })),
    unallowed: unallowedSurvivors.map((entry) => entry.id),
  },
  actionCoverage,
  unprobedActions,
  staleSources: staleSources.map((entry) => entry.id),
  triageErrors,
};
const serialized = JSON.stringify(payload, null, 2) + '\n';
const lines = [];
lines.push('# Builder bug discovery');
lines.push('');
lines.push('Status: generated from the mutation probes, retained evidence, source hashes, and the strict allowlist.');
lines.push('');
lines.push('## Summary');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(summary)) lines.push('| ' + key + ' | ' + (typeof value === 'boolean' ? (value ? 'yes' : 'no') : value) + ' |');
lines.push('');
lines.push('## Probes');
lines.push('');
lines.push('| Probe | Runner | Model action | Status | Source hash | Expected failure |');
lines.push('| --- | --- | --- | --- | --- | --- |');
for (const entry of results) {
  const source = entry.sourceMatches === true ? 'current' : entry.sourceMatches === false ? 'stale' : 'missing';
  lines.push('| `' + entry.id + '` | ' + entry.runner + ' | ' + (entry.modelActions.length > 0 ? entry.modelActions.map((action) => '`' + action + '`').join(', ') : 'unmapped') + ' | ' + entry.status + ' | ' + source + ' | ' + entry.expectedFailure + ' |');
}
lines.push('');
lines.push('## Allowlist');
lines.push('');
if ((allowlist.survivors ?? []).length === 0) lines.push('No survivor is allowlisted.');
for (const entry of allowlist.survivors ?? []) lines.push('- `' + entry.id + '`: ' + entry.reason);
lines.push('');
lines.push('## Survivors');
lines.push('');
if (survivors.length === 0) lines.push('No surviving probe.');
for (const entry of allowedSurvivors) lines.push('- `' + entry.id + '` is allowlisted: ' + (allowlisted.get(entry.id)?.reason ?? 'no reason'));
for (const entry of unallowedSurvivors) lines.push('- `' + entry.id + '` is not allowlisted and fails the strict gate.');
lines.push('');
lines.push('## Stale or unhashed evidence');
lines.push('');
if (staleSources.length === 0 && unhashedSources.length === 0) lines.push('Every retained probe has a current source hash.');
for (const entry of staleSources) lines.push('- `' + entry.id + '` source changed since the mutation run.');
for (const entry of unhashedSources) lines.push('- `' + entry.id + '` has no source hash.');
lines.push('');
lines.push('## Action coverage');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
lines.push('| actionObligations | ' + summary.actionObligations + ' |');
lines.push('| probedActions | ' + summary.probedActions + ' |');
lines.push('| unprobedActions | ' + summary.unprobedActions + ' |');
lines.push('');
if (coveredActions.length > 0) {
  lines.push('Covered actions:');
  lines.push('');
  for (const entry of coveredActions) lines.push('- `' + entry.id + '`: ' + entry.probes.map((id) => '`' + id + '`').join(', '));
  lines.push('');
}
lines.push('Actions without a mutation probe:');
lines.push('');
for (const action of unprobedActions.slice(0, 40)) lines.push('- `' + action + '`');
if (unprobedActions.length > 40) lines.push('- ' + (unprobedActions.length - 40) + ' more in the JSON report.');
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('The probes are hand-authored guards. The report proves which guards have fresh mutation evidence and which model actions have no probe. It does not generate model-derived mutants yet.');
const markdown = lines.join('\n') + '\n';

if (check) {
  if (staleSources.length > 0) {
    console.error('FAIL mutation evidence is stale for: ' + staleSources.map((entry) => entry.id).join(', '));
    process.exit(1);
  }
  if (unhashedSources.length > 0) {
    console.error('FAIL mutation evidence has no source hash for: ' + unhashedSources.map((entry) => entry.id).join(', '));
    process.exit(1);
  }
  if (triageErrors.length > 0) {
    console.error('FAIL mutation evidence triage: ' + triageErrors.join('; '));
    process.exit(1);
  }
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
if (strict && !summary.strictPass) {
  console.error('FAIL strict bug discovery: ' + summary.unallowedSurvivors + ' unallowlisted survivors, ' + summary.staleSources + ' stale sources, ' + summary.triageErrors + ' triage errors');
  process.exit(1);
}
