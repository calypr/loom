#!/usr/bin/env node
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const LEDGER = 'verification/builder-interactions/route-contract.json';
const OBLIGATIONS = 'verification/builder-interactions/test-obligations.json';
const BROWSER = 'verification/builder-interactions/browser-evidence.json';
const TRACE = 'verification/builder-interactions/builder-trace-contract.json';
const INVENTORY = 'docs/ui-contract-inventory/ui-supported-affordances.json';
const MODEL_MAP = 'verification/builder-interactions/affordance-model-map.json';
const ROUTE_ACTION_PROPOSALS = 'verification/builder-interactions/route-action-proposals.json';
const CONTRACT_PROOF_PROPOSALS = 'verification/builder-interactions/contract-proof-proposals.json';
const BUG_DISCOVERY = 'verification/builder-interactions/bug-discovery.json';
const OUTPUT_JSON = 'verification/builder-interactions/readiness.json';
const OUTPUT_MD = 'verification/builder-interactions/readiness.md';
const check = process.argv.includes('--check');

const readJson = (path) => JSON.parse(readFileSync(resolve(ROOT, path), 'utf8'));
const routeKey = (route) => route.method + ' ' + route.path;
const proofStatus = (route) => route.proof?.status ?? 'missing';
const routeActions = (route) => route.proof?.routeActions ?? [];
const modelActions = (route) => route.proof?.modelActions ?? [];

const ledger = readJson(LEDGER);
const obligations = readJson(OBLIGATIONS);
const browser = readJson(BROWSER);
const trace = readJson(TRACE);
const inventory = readJson(INVENTORY);
const modelMap = readJson(MODEL_MAP);
const routeActionProposals = existsSync(resolve(ROOT, ROUTE_ACTION_PROPOSALS)) ? readJson(ROUTE_ACTION_PROPOSALS) : null;
const contractProofProposals = existsSync(resolve(ROOT, CONTRACT_PROOF_PROPOSALS)) ? readJson(CONTRACT_PROOF_PROPOSALS) : null;
const bugDiscovery = existsSync(resolve(ROOT, BUG_DISCOVERY)) ? readJson(BUG_DISCOVERY) : null;
const browserRoutes = new Set((browser.cases ?? []).flatMap((entry) => entry.coveredRoutes ?? []));

const gapOrder = {
  'audit route action': 0,
  'choose contract-tested with a route test or unproven': 1,
  'add trace, schedule, and executable test': 2,
  'assign a model action or exclude': 3,
  'add browser evidence': 4,
};

const nextGap = (route) => {
  const status = proofStatus(route);
  if (status === 'modeled-and-traced' || status === 'contract-tested' || status === 'excluded') return null;
  if (status === 'trace-tested') return 'add browser evidence';
  if (status === 'unproven') return 'assign a model action or exclude';
  if (status === 'modeled-only') {
    if (!route.proof?.routeActionReason) return 'audit route action';
    if (routeActions(route).length === 0) return 'choose contract-tested with a route test or unproven';
    return 'add trace, schedule, and executable test';
  }
  return 'assign a proof status';
};

const routeActionState = (route) => {
  const status = proofStatus(route);
  if (status === 'modeled-and-traced') return 'proven';
  if (status === 'trace-tested') return 'traced';
  if (routeActions(route).length > 0) return 'candidate';
  if (route.proof?.routeActionReason) return 'empty';
  return 'missing';
};

const routes = (ledger.routes ?? []).map((route) => {
  const status = proofStatus(route);
  const gap = nextGap(route);
  return {
    id: route.id,
    tier: route.tier,
    method: route.method,
    path: route.path,
    status,
    routeActionState: routeActionState(route),
    routeActions: routeActions(route),
    modelActions: modelActions(route),
    routeActionReason: route.proof?.routeActionReason ?? null,
    model: route.proof?.model ?? null,
    traceEvent: route.proof?.traceEvent ?? null,
    schedule: route.proof?.schedule ?? null,
    browserCase: route.proof?.browserCase ?? null,
    browserCovered: browserRoutes.has(routeKey(route)),
    goTest: route.proof?.goTest ?? null,
    uiTest: route.proof?.uiTest ?? null,
    nextGap: gap,
  };
});

const statusCounts = Object.fromEntries(
  ['modeled-and-traced', 'trace-tested', 'modeled-only', 'contract-tested', 'unproven', 'excluded']
    .map((status) => [status, routes.filter((route) => route.status === status).length]),
);
const actionStateCounts = Object.fromEntries(
  ['proven', 'traced', 'candidate', 'empty', 'missing']
    .map((state) => [state, routes.filter((route) => route.routeActionState === state).length]),
);
const worklist = routes
  .filter((route) => route.nextGap)
  .sort((left, right) => {
    const gap = gapOrder[left.nextGap] - gapOrder[right.nextGap];
    if (gap !== 0) return gap;
    if (left.tier !== right.tier) return left.tier - right.tier;
    return left.path.localeCompare(right.path) || left.method.localeCompare(right.method);
  });

const summary = obligations.summary ?? {};
const affordances = inventory.affordances ?? [];
const mappedAffordances = modelMap.affordances ?? [];
const featureMapReady = affordances.length === mappedAffordances.length
  && mappedAffordances.every((entry) => entry.modelStatus === 'modeled');
const strictBlockers = routes.filter((route) => !['modeled-and-traced', 'contract-tested', 'excluded'].includes(route.status));
const missingRouteActions = routes.filter((route) => route.routeActionState === 'missing');
const emptyRouteActions = routes.filter((route) => route.routeActionState === 'empty');
const traceTested = routes.filter((route) => route.status === 'trace-tested');
const modeledAndTracedWithoutBrowser = routes.filter((route) => route.status === 'modeled-and-traced' && !route.browserCovered);
const bugDiscoveryReady = bugDiscovery?.summary?.strictPass === true;
const readiness = {
  endToEndReady: strictBlockers.length === 0
    && (summary.missingCandidate ?? 0) === 0
    && (summary.missingRouteBound ?? 0) === 0
    && featureMapReady
    && bugDiscoveryReady,
  routeActionAuditReady: missingRouteActions.length === 0 && emptyRouteActions.length === 0,
  obligationAttributionReady: (summary.missingRouteBound ?? 0) === 0 && (summary.missingCandidate ?? 0) === 0,
  browserEvidenceReady: traceTested.length === 0 && modeledAndTracedWithoutBrowser.length === 0,
  featureMapReady,
  bugDiscoveryReady,
};

const payload = {
  schemaVersion: 1,
  generatedFrom: { ledger: LEDGER, obligations: OBLIGATIONS, browser: BROWSER, trace: TRACE, inventory: INVENTORY, modelMap: MODEL_MAP },
  readiness,
  summary: {
    routes: routes.length,
    tier1: routes.filter((route) => route.tier === 1).length,
    tier2: routes.filter((route) => route.tier === 2).length,
    strictRouteBlockers: strictBlockers.length,
    routeActionAudited: routes.filter((route) => route.routeActionState !== 'missing').length,
    routeActionMissing: missingRouteActions.length,
    routeActionEmpty: emptyRouteActions.length,
    routeActionCandidate: routes.filter((route) => route.routeActionState === 'candidate').length,
    traceTested: traceTested.length,
    modeledAndTracedWithoutBrowser: modeledAndTracedWithoutBrowser.length,
    browserCases: (browser.cases ?? []).length,
    browserCoveredRoutes: browserRoutes.size,
    ledgerRoutesWithBrowserCoverage: routes.filter((route) => route.browserCovered).length,
    obligations: summary.obligations ?? 0,
    covered: summary.covered ?? 0,
    partial: summary.partial ?? 0,
    missing: summary.missing ?? 0,
    missingRouteBound: summary.missingRouteBound ?? 0,
    missingCandidate: summary.missingCandidate ?? 0,
    missingUnbound: summary.missingUnbound ?? 0,
    tests: summary.tests ?? 0,
    testsWithoutExactObligations: summary.testsWithoutObligations ?? 0,
    models: summary.models ?? 0,
    traceModels: Object.keys(trace.models ?? {}).length,
    schedules: Object.keys(trace.schedules ?? {}).length,
    affordances: affordances.length,
    modeledAffordances: mappedAffordances.filter((entry) => entry.modelStatus === 'modeled').length,
    testedAffordances: mappedAffordances.filter((entry) => entry.implementationStatus === 'tested').length,
  },
  statusCounts,
  actionStateCounts,
  lever: {
    routeActionProposals: routeActionProposals?.summary?.routes ?? 0,
    routeActionProposalLow: routeActionProposals?.summary?.low ?? 0,
    contractProofProposals: contractProofProposals?.summary?.routes ?? 0,
    contractProofProposalLow: contractProofProposals?.summary?.low ?? 0,
    mutationProbes: bugDiscovery?.summary?.probes ?? 0,
    mutationKilled: bugDiscovery?.summary?.killed ?? 0,
    mutationSurvived: bugDiscovery?.summary?.survived ?? 0,
    mutationEquivalent: bugDiscovery?.summary?.equivalent ?? 0,
    mutationUnprobedActions: bugDiscovery?.summary?.unprobedActions ?? 0,
    mutationStrictPass: bugDiscovery?.summary?.strictPass ?? null,
  },
  blockerRoutes: strictBlockers
    .map(({ id, tier, method, path, status, nextGap: gap }) => ({ id, tier, method, path, status, nextGap: gap })),
  worklist,
};

const serialized = JSON.stringify(payload, null, 2) + '\n';
const rows = (items) => items.map((item) => '| ' + item.join(' | ') + ' |');
const lines = [];
lines.push('# Builder readiness');
lines.push('');
lines.push('Status: generated from the route ledger, obligation report, browser evidence, trace contract, and feature map.');
lines.push('');
lines.push('End to end ready: ' + (readiness.endToEndReady ? 'yes' : 'no') + '.');
lines.push('');
lines.push('## Summary');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(payload.summary)) lines.push('| ' + key + ' | ' + value + ' |');
lines.push('');
lines.push('## Route proof status');
lines.push('');
lines.push('| Status | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(statusCounts)) lines.push('| ' + key + ' | ' + value + ' |');
lines.push('');
lines.push('## Route action audit');
lines.push('');
lines.push('| State | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(actionStateCounts)) lines.push('| ' + key + ' | ' + value + ' |');
lines.push('');
lines.push('## Lever inputs');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(payload.lever)) lines.push('| ' + key + ' | ' + (typeof value === 'boolean' ? (value ? 'yes' : 'no') : value) + ' |');
lines.push('');
lines.push('## Missing obligations');
lines.push('');
lines.push('| Binding | Count |');
lines.push('| --- | ---: |');
lines.push('| route | ' + (summary.missingRouteBound ?? 0) + ' |');
lines.push('| candidate | ' + (summary.missingCandidate ?? 0) + ' |');
lines.push('| unbound | ' + (summary.missingUnbound ?? 0) + ' |');
lines.push('');
lines.push('## Next worklist');
lines.push('');
lines.push('| Tier | Route | Status | Route action | Next gap |');
lines.push('| ---: | --- | --- | --- | --- |');
for (const route of worklist.slice(0, 40)) {
  lines.push('| ' + [route.tier, '`' + route.method + ' ' + route.path + '`', route.status, route.routeActionState, route.nextGap].join(' | ') + ' |');
}
if (worklist.length > 40) lines.push('| | ' + (worklist.length - 40) + ' more in the JSON report | | | |');
lines.push('');
lines.push('## Blockers');
lines.push('');
if (strictBlockers.length === 0) lines.push('- No strict route blockers.');
for (const route of strictBlockers) lines.push('- `' + route.id + '` is `' + route.status + '`. ' + route.nextGap + '.');
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('This report ranks work. It does not prove a route. The strict route check and the full formal runner remain the gates.');
const markdown = lines.join('\n') + '\n';

if (check) {
  const currentJson = existsSync(resolve(ROOT, OUTPUT_JSON)) ? readFileSync(resolve(ROOT, OUTPUT_JSON), 'utf8') : null;
  const currentMd = existsSync(resolve(ROOT, OUTPUT_MD)) ? readFileSync(resolve(ROOT, OUTPUT_MD), 'utf8') : null;
  if (currentJson !== serialized || currentMd !== markdown) {
    console.error('FAIL ' + OUTPUT_JSON + ' or ' + OUTPUT_MD + ' is stale; run node scripts/build-builder-readiness.mjs');
    process.exit(1);
  }
  console.log('PASS ' + OUTPUT_JSON + ' is current: ' + routes.length + ' routes, ' + worklist.length + ' work items, end-to-end ready=' + readiness.endToEndReady);
} else {
  writeFileSync(resolve(ROOT, OUTPUT_JSON), serialized);
  writeFileSync(resolve(ROOT, OUTPUT_MD), markdown);
  console.log('wrote ' + OUTPUT_JSON + ' and ' + OUTPUT_MD + ' with ' + routes.length + ' routes and ' + worklist.length + ' work items');
}
