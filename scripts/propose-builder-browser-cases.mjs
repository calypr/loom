#!/usr/bin/env node
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ROOT, readJson, routeMode } from './lib/builder-route-evidence.mjs';

const LEDGER = 'verification/builder-interactions/route-contract.json';
const BROWSER = 'verification/builder-interactions/browser-evidence.json';
const OUTPUT_JSON = 'verification/builder-interactions/browser-case-proposals.json';
const OUTPUT_MD = 'verification/builder-interactions/browser-case-proposals.md';
const check = process.argv.includes('--check');

const ledger = readJson(LEDGER);
const browser = readJson(BROWSER);
const coveredRoutes = new Set((browser.cases ?? []).flatMap((entry) => entry.coveredRoutes ?? []));
const routeKey = (route) => route.method + ' ' + route.path;
const slug = (route) => (route.method + '-' + route.path.split('/').filter(Boolean).at(-1))
  .toLowerCase()
  .replace(/[^a-z0-9]+/g, '-')
  .replace(/^-|-$/g, '');

const proposals = (ledger.routes ?? [])
  .filter((route) => !coveredRoutes.has(routeKey(route)))
  .map((route) => {
    const mode = routeMode(route);
    const priority = route.tier === 1 && mode === 'stateful'
      ? 'high'
      : route.tier === 1 || mode === 'stateful'
        ? 'medium'
        : 'low';
    return {
      routeId: route.id,
      tier: route.tier,
      method: route.method,
      path: route.path,
      status: route.proof?.status ?? 'missing',
      routeMode: mode,
      routeActions: route.proof?.routeActions ?? [],
      uiCallers: route.uiCallers ?? [],
      builderEvidence: route.builderEvidence ?? [],
      proposedCaseId: 'builder-' + slug(route),
      priority,
      reason: 'No recorded browser case covers this route. Add a browser lifecycle that exercises it, then regenerate browser-evidence.json from the passing report.',
      reviewRequired: true,
    };
  })
  .sort((left, right) => left.tier - right.tier || left.path.localeCompare(right.path) || left.method.localeCompare(right.method));

const summary = {
  routes: proposals.length,
  tier1: proposals.filter((entry) => entry.tier === 1).length,
  tier2: proposals.filter((entry) => entry.tier === 2).length,
  stateful: proposals.filter((entry) => entry.routeMode === 'stateful').length,
  readLike: proposals.filter((entry) => entry.routeMode === 'read').length,
  high: proposals.filter((entry) => entry.priority === 'high').length,
  medium: proposals.filter((entry) => entry.priority === 'medium').length,
  low: proposals.filter((entry) => entry.priority === 'low').length,
};
const payload = {
  schemaVersion: 1,
  generatedFrom: { ledger: LEDGER, browser: BROWSER },
  summary,
  proposals,
};
const serialized = JSON.stringify(payload, null, 2) + '\n';
const lines = [];
lines.push('# Builder browser case proposals');
lines.push('');
lines.push('Status: generated review queue. These proposals are not browser evidence until a passing report exists.');
lines.push('');
lines.push('## Summary');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(summary)) lines.push('| ' + key + ' | ' + value + ' |');
lines.push('');
lines.push('## Review queue');
lines.push('');
lines.push('| Tier | Route | Status | Mode | Priority | Proposed case | Callers |');
lines.push('| ---: | --- | --- | --- | --- | --- | --- |');
for (const entry of proposals) {
  const callers = entry.uiCallers.length === 0 ? 'none' : entry.uiCallers.map((caller) => '`' + caller + '`').join(', ');
  lines.push('| ' + [entry.tier, '`' + entry.method + ' ' + entry.path + '`', entry.status, entry.routeMode, entry.priority, '`' + entry.proposedCaseId + '`', callers].join(' | ') + ' |');
}
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('A browser proposal is a target, not proof. Run the lifecycle and regenerate `browser-evidence.json` from the passing Playwright report before changing the route proof.');
const markdown = lines.join('\n') + '\n';

if (check) {
  const currentJson = existsSync(resolve(ROOT, OUTPUT_JSON)) ? readFileSync(resolve(ROOT, OUTPUT_JSON), 'utf8') : null;
  const currentMd = existsSync(resolve(ROOT, OUTPUT_MD)) ? readFileSync(resolve(ROOT, OUTPUT_MD), 'utf8') : null;
  if (currentJson !== serialized || currentMd !== markdown) {
    console.error('FAIL ' + OUTPUT_JSON + ' or ' + OUTPUT_MD + ' is stale; run node scripts/propose-builder-browser-cases.mjs');
    process.exit(1);
  }
  console.log('PASS ' + OUTPUT_JSON + ' is current: ' + proposals.length + ' routes without browser coverage');
} else {
  writeFileSync(resolve(ROOT, OUTPUT_JSON), serialized);
  writeFileSync(resolve(ROOT, OUTPUT_MD), markdown);
  console.log('wrote ' + OUTPUT_JSON + ' and ' + OUTPUT_MD + ' with ' + proposals.length + ' browser case proposals');
}
