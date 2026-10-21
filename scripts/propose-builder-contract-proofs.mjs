#!/usr/bin/env node
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ROOT, evidenceForRoute, readJson } from './lib/builder-route-evidence.mjs';

const LEDGER = 'verification/builder-interactions/route-contract.json';
const OBLIGATIONS = 'verification/builder-interactions/test-obligations.json';
const BROWSER = 'verification/builder-interactions/browser-evidence.json';
const ROUTE_ACTIONS = 'verification/builder-interactions/route-action-proposals.json';
const OUTPUT_JSON = 'verification/builder-interactions/contract-proof-proposals.json';
const OUTPUT_MD = 'verification/builder-interactions/contract-proof-proposals.md';
const check = process.argv.includes('--check');

const ledger = readJson(LEDGER);
const obligations = readJson(OBLIGATIONS);
const browser = readJson(BROWSER);
const routeActions = readJson(ROUTE_ACTIONS);
const tests = obligations.tests ?? [];
const actionProposalByRoute = new Map((routeActions.proposals ?? []).map((entry) => [entry.routeId, entry]));

const proposals = [];
for (const route of ledger.routes ?? []) {
  const status = route.proof?.status ?? 'missing';
  if (status === 'modeled-and-traced' || status === 'contract-tested' || status === 'excluded') continue;
  if ((route.proof?.routeActions ?? []).length > 0) continue;
  const actionProposal = actionProposalByRoute.get(route.id);
  if (actionProposal && actionProposal.proposedRouteActions.length > 0) {
    const browserCase = actionProposal.evidence.find((entry) => entry.kind === 'browser')?.id ?? null;
    const goTest = actionProposal.evidence.find((entry) => entry.kind === 'go')?.name ?? null;
    const uiTest = actionProposal.evidence.find((entry) => entry.kind === 'ui')?.file ?? null;
    proposals.push({
      routeId: route.id,
      tier: route.tier,
      method: route.method,
      path: route.path,
      currentStatus: status,
      proposedStatus: 'modeled-only',
      proposedRouteActions: actionProposal.proposedRouteActions,
      confidence: actionProposal.confidence,
      browserCase,
      goTest,
      uiTest,
      reason: 'Route action proposal is non-empty. Apply the route actions and add a trace before a contract proof.',
      evidence: actionProposal.evidence,
      reviewRequired: true,
    });
    continue;
  }
  const evidence = evidenceForRoute(route, tests, browser.cases ?? []);
  const browserEvidence = evidence.browser[0]?.id ?? null;
  const goEvidence = evidence.tests.find((entry) => entry.kind === 'go');
  const uiEvidence = evidence.tests.find((entry) => entry.kind === 'ui');
  const isWrite = route.method !== 'GET' && (route.path.includes('explicit-groups') || route.path.endsWith('/publish'));
  const hasTest = Boolean(goEvidence || uiEvidence);
  const confidence = browserEvidence && hasTest ? 'high' : browserEvidence || hasTest ? 'medium' : 'low';
  const proposedStatus = isWrite ? 'unproven' : 'contract-tested';
  proposals.push({
    routeId: route.id,
    tier: route.tier,
    method: route.method,
    path: route.path,
    currentStatus: status,
    proposedStatus,
    proposedRouteActions: [],
    confidence,
    browserCase: browserEvidence,
    goTest: goEvidence?.name ?? null,
    uiTest: uiEvidence?.file ?? null,
    reason: proposedStatus === 'contract-tested'
      ? 'Read or pure proposal route with no route-level TLA action. A browser case or route test is the correct proof.'
      : 'Write route with no route-level TLA action. Model it or add a route contract test.',
    evidence: evidence.evidence,
    reviewRequired: true,
  });
}

proposals.sort((left, right) => left.tier - right.tier || left.path.localeCompare(right.path) || left.method.localeCompare(right.method));
const summary = {
  routes: proposals.length,
  contractTested: proposals.filter((entry) => entry.proposedStatus === 'contract-tested').length,
  modeledOnly: proposals.filter((entry) => entry.proposedStatus === 'modeled-only').length,
  unproven: proposals.filter((entry) => entry.proposedStatus === 'unproven').length,
  high: proposals.filter((entry) => entry.confidence === 'high').length,
  medium: proposals.filter((entry) => entry.confidence === 'medium').length,
  low: proposals.filter((entry) => entry.confidence === 'low').length,
};
const payload = {
  schemaVersion: 1,
  generatedFrom: { ledger: LEDGER, obligations: OBLIGATIONS, browser: BROWSER, routeActions: ROUTE_ACTIONS },
  summary,
  proposals,
};
const serialized = JSON.stringify(payload, null, 2) + '\n';
const lines = [];
lines.push('# Builder contract proof proposals');
lines.push('');
lines.push('Status: generated review queue. These proposals do not change the route ledger.');
lines.push('');
lines.push('## Summary');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(summary)) lines.push('| ' + key + ' | ' + value + ' |');
lines.push('');
lines.push('## Review queue');
lines.push('');
lines.push('| Tier | Route | Proposed status | Confidence | Browser | Test | Evidence |');
lines.push('| ---: | --- | --- | --- | --- | --- | --- |');
for (const entry of proposals) {
  const rendered = entry.evidence.slice(0, 3).map((item) => item.kind + (item.name ? ':' + item.name : '') + (item.file ? ' ' + item.file : ''));
  const evidence = entry.evidence.length === 0
    ? 'none'
    : rendered.join(', ') + (entry.evidence.length > rendered.length ? ' +' + (entry.evidence.length - rendered.length) + ' more' : '');
  lines.push('| ' + [entry.tier, '`' + entry.method + ' ' + entry.path + '`', entry.proposedStatus, entry.confidence, entry.browserCase ?? 'none', entry.goTest ?? entry.uiTest ?? 'none', evidence].join(' | ') + ' |');
}
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('A proposal is evidence, not proof. Review the test or browser case before adding it to `builder-route-overrides.json`.');
const markdown = lines.join('\n') + '\n';

if (check) {
  const currentJson = existsSync(resolve(ROOT, OUTPUT_JSON)) ? readFileSync(resolve(ROOT, OUTPUT_JSON), 'utf8') : null;
  const currentMd = existsSync(resolve(ROOT, OUTPUT_MD)) ? readFileSync(resolve(ROOT, OUTPUT_MD), 'utf8') : null;
  if (currentJson !== serialized || currentMd !== markdown) {
    console.error('FAIL ' + OUTPUT_JSON + ' or ' + OUTPUT_MD + ' is stale; run node scripts/propose-builder-contract-proofs.mjs');
    process.exit(1);
  }
  console.log('PASS ' + OUTPUT_JSON + ' is current: ' + proposals.length + ' contract proof proposals');
} else {
  writeFileSync(resolve(ROOT, OUTPUT_JSON), serialized);
  writeFileSync(resolve(ROOT, OUTPUT_MD), markdown);
  console.log('wrote ' + OUTPUT_JSON + ' and ' + OUTPUT_MD + ' with ' + proposals.length + ' proposals');
}
