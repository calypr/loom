#!/usr/bin/env node
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ROOT, readJson, uniqueSorted, walkFiles } from './lib/builder-route-evidence.mjs';

const OBLIGATIONS = 'verification/builder-interactions/test-obligations.json';
const PROBES = 'verification/builder-interactions/mutation-probes.json';
const LEDGER = 'verification/builder-interactions/route-contract.json';
const OUTPUT_JSON = 'verification/builder-interactions/mutation-probe-proposals.json';
const OUTPUT_MD = 'verification/builder-interactions/mutation-probe-proposals.md';
const args = process.argv.slice(2);
const check = args.includes('--check');
const selfTest = args.includes('--self-test');

const obligations = readJson(OBLIGATIONS);
const probes = readJson(PROBES).probes ?? [];
const ledger = readJson(LEDGER);
const probedActions = new Set(probes.flatMap((probe) => [
  ...(probe.modelActions ?? []),
  ...(probe.modelAction ? [probe.modelAction] : []),
]));
const tests = obligations.tests ?? [];
const routesById = new Map((ledger.routes ?? []).map((route) => [route.id, route]));

const testScore = (action, test) => {
  if ((test.obligations ?? []).includes(action.id)) return 3;
  if ((test.suspectedModels ?? []).includes(action.model) && (test.suspectedActions ?? []).includes(action.action)) return 2;
  if ((test.routes ?? []).some((route) => (action.routes ?? []).includes(route))) return 1;
  return 0;
};

const candidateTests = (action) => tests
  .map((test) => ({ test, score: testScore(action, test) }))
  .filter((entry) => entry.score > 0)
  .sort((left, right) => right.score - left.score || left.test.id.localeCompare(right.test.id))
  .slice(0, 4)
  .map(({ test, score }) => ({ id: test.id, kind: test.kind, name: test.name, file: test.file, score }));

const candidateSourceFiles = (action) => uniqueSorted((action.routes ?? []).flatMap((routeKey) => {
  const route = [...routesById.values()].find((entry) => entry.method + ' ' + entry.path === routeKey);
  return route?.builderEvidence ?? [];
}));

const guardMutation = (line) => {
  const match = line.match(/^(\s*)if\s*\((.*)\)\s*\{\s*$/);
  if (!match) return null;
  const operator = match[2].includes(' && ') ? '&&' : match[2].includes(' || ') ? '||' : null;
  if (!operator) return null;
  const parts = match[2].split(operator).map((part) => part.trim());
  if (parts.length < 2) return null;
  const dropped = parts.pop();
  return {
    operator,
    dropped,
    find: line,
    replace: match[1] + 'if (' + parts.join(' ' + operator + ' ') + ') {',
  };
};

const sourceIndex = walkFiles('ui/packages/loom-ui/src/features/ExplorerBuilder', (path) => /\.(ts|tsx)$/.test(path) && !/\.(test|spec)\./.test(path))
  .map((file) => ({ file, lines: readFileSync(file, 'utf8').split('\n') }));

const guardCandidates = (action) => {
  const tokens = action.action.replace(/([a-z0-9])([A-Z])/g, '$1 $2').toLowerCase().split(/[^a-z0-9]+/).filter((token) => token.length > 2);
  const candidates = [];
  for (const { file, lines } of sourceIndex) {
    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index];
      const lower = line.toLowerCase();
      const hits = tokens.filter((token) => lower.includes(token)).length;
      const specific = lower.includes(action.action.toLowerCase());
      if (!specific && hits < 2) continue;
      const mutation = guardMutation(line);
      if (!mutation) continue;
      const occurrences = lines.filter((candidate) => candidate === line).length;
      candidates.push({
        file: file.slice(ROOT.length + 1),
        line: index + 1,
        score: hits,
        specific,
        occurrences,
        confidence: specific && occurrences === 1 ? 'medium' : 'low',
        ...mutation,
      });
    }
  }
  return candidates
    .sort((left, right) => right.score - left.score || left.file.localeCompare(right.file) || left.line - right.line)
    .slice(0, 3);
};

const proposals = (obligations.obligations ?? [])
  .filter((entry) => entry.kind === 'action' && !probedActions.has(entry.id))
  .map((action) => {
    const candidates = candidateTests(action);
    const exact = candidates.some((entry) => entry.score === 3);
    const strong = exact || candidates.some((entry) => entry.score === 2);
    const priority = strong ? 'high' : candidates.length > 0 ? 'medium' : 'low';
    return {
      modelAction: action.id,
      model: action.model,
      action: action.action,
      status: action.status,
      routeBinding: action.routeBinding,
      routeCount: (action.routes ?? []).length,
      candidateTests: candidates,
      candidateSourceFiles: candidateSourceFiles(action).slice(0, 8),
      guardCandidates: guardCandidates(action),
      priority,
      proposedProbeId: 'model-' + action.id.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, ''),
      reason: candidates.length > 0
        ? 'Pick a guard that implements this action and use one of the candidate tests for the mutation failure.'
        : 'No candidate test maps to this action. Add a focused test before adding a mutation probe.',
    };
  })
  .sort((left, right) => left.priority.localeCompare(right.priority) || left.modelAction.localeCompare(right.modelAction));

const summary = {
  actions: proposals.length,
  high: proposals.filter((entry) => entry.priority === 'high').length,
  medium: proposals.filter((entry) => entry.priority === 'medium').length,
  low: proposals.filter((entry) => entry.priority === 'low').length,
  withCandidateTests: proposals.filter((entry) => entry.candidateTests.length > 0).length,
  withoutCandidateTests: proposals.filter((entry) => entry.candidateTests.length === 0).length,
  guardCandidates: proposals.reduce((sum, entry) => sum + entry.guardCandidates.length, 0),
  withGuardCandidates: proposals.filter((entry) => entry.guardCandidates.length > 0).length,
};
const payload = {
  schemaVersion: 1,
  generatedFrom: { obligations: OBLIGATIONS, probes: PROBES, ledger: LEDGER },
  summary,
  proposals,
};
const serialized = JSON.stringify(payload, null, 2) + '\n';
const lines = [];
lines.push('# Builder mutation probe proposals');
lines.push('');
lines.push('Status: model-derived review queue. These are candidate actions and tests, not executable mutants.');
lines.push('');
lines.push('## Summary');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(summary)) lines.push('| ' + key + ' | ' + value + ' |');
lines.push('');
lines.push('## Review queue');
lines.push('');
lines.push('| Priority | Model action | Routes | Candidate tests | Candidate source |');
lines.push('| --- | --- | ---: | --- | --- |');
for (const entry of proposals.slice(0, 60)) {
  const candidate = entry.candidateTests[0];
  const tests = candidate ? '`' + candidate.name + '` ' + candidate.file : 'none';
  lines.push('| ' + entry.priority + ' | `' + entry.modelAction + '` | ' + entry.routeCount + ' | ' + tests + ' | ' + (entry.candidateSourceFiles[0] ?? 'none') + ' |');
}
if (proposals.length > 60) lines.push('| ... | ' + (proposals.length - 60) + ' more in the JSON report | | | |');
lines.push('');
lines.push('## Guard candidates');
lines.push('');
const guardRows = proposals.flatMap((entry) => entry.guardCandidates.map((guard) => ({ action: entry.modelAction, ...guard })));
if (guardRows.length === 0) lines.push('No syntactic guard candidate matched an unprobed action.');
else {
  lines.push('| Model action | File | Line | Mutation |');
  lines.push('| --- | --- | ---: | --- |');
  for (const guard of guardRows.slice(0, 30)) lines.push('| `' + guard.action + '` | `' + guard.file + '` | ' + guard.line + ' | `' + guard.find.trim() + '` -> `' + guard.replace.trim() + '` |');
  if (guardRows.length > 30) lines.push('| ... | | | ' + (guardRows.length - 30) + ' more in the JSON report |');
}
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('The queue selects unprobed model actions and ranks existing tests by exact obligation, suspected action/model, then route overlap. Guard candidates are syntactic removals of the last `&&` or `||` operand in an `if` guard; a reviewer must confirm that the guard implements the action before adding it as an executable probe.');
const markdown = lines.join('\n') + '\n';

function runSelfTest() {
  const assert = (condition, message) => {
    if (!condition) {
      console.error('FAIL mutation proposal self-test: ' + message);
      process.exit(1);
    }
  };
  const conjunction = guardMutation('  if (ready && receiptMatches) {');
  assert(conjunction?.operator === '&&', 'did not detect a conjunction');
  assert(conjunction?.replace === '  if (ready) {', 'did not drop the last conjunction');
  const disjunction = guardMutation('  if (ready || stale) {');
  assert(disjunction?.operator === '||', 'did not detect a disjunction');
  assert(disjunction?.replace === '  if (ready) {', 'did not drop the last disjunct');
  assert(guardMutation('  const ready = check();') === null, 'treated a non-guard as a guard');
  console.log('PASS mutation proposal self-test: conjunction, disjunction, and non-guard fixtures');
}

if (selfTest) {
  runSelfTest();
  process.exit(0);
}

if (check) {
  const currentJson = existsSync(resolve(ROOT, OUTPUT_JSON)) ? readFileSync(resolve(ROOT, OUTPUT_JSON), 'utf8') : null;
  const currentMd = existsSync(resolve(ROOT, OUTPUT_MD)) ? readFileSync(resolve(ROOT, OUTPUT_MD), 'utf8') : null;
  if (currentJson !== serialized || currentMd !== markdown) {
    console.error('FAIL ' + OUTPUT_JSON + ' or ' + OUTPUT_MD + ' is stale; run node scripts/propose-builder-mutation-probes.mjs');
    process.exit(1);
  }
  console.log('PASS ' + OUTPUT_JSON + ' is current: ' + summary.actions + ' unprobed actions');
} else {
  writeFileSync(resolve(ROOT, OUTPUT_JSON), serialized);
  writeFileSync(resolve(ROOT, OUTPUT_MD), markdown);
  console.log('wrote ' + OUTPUT_JSON + ' and ' + OUTPUT_MD + ' with ' + summary.actions + ' candidates');
}
