#!/usr/bin/env node
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ROOT, evidenceForRoute, readJson, routeMode, uniqueSorted } from './lib/builder-route-evidence.mjs';

const LEDGER = 'verification/builder-interactions/route-contract.json';
const OBLIGATIONS = '.cache/builder-verification/test-obligations.json';
const BROWSER = 'verification/builder-interactions/browser-evidence.json';
const TRACE = 'verification/builder-interactions/builder-trace-contract.json';
const OUTPUT_JSON = '.cache/builder-verification/route-action-proposals.json';
const OUTPUT_MD = '.cache/builder-verification/route-action-proposals.md';
const check = process.argv.includes('--check');

const ledger = readJson(LEDGER);
const obligations = readJson(OBLIGATIONS);
const browser = readJson(BROWSER);
const trace = readJson(TRACE);
const tests = obligations.tests ?? [];

const classify = (route) => {
  const path = route.path;
  if (path.endsWith('/builder')) return 'builder';
  if (routeMode(route) === 'read') return 'read';
  if (path.includes('/explicit-groups')) return 'write';
  if (path.endsWith('/publish')) return 'publish';
  if (path.endsWith('discoveries')) return 'discovery';
  if (path.includes('/row-change')) return 'row-change';
  if (path.includes('/population-')) return 'population';
  if (path.includes('/interpretation-')) return 'interpretation';
  if (path.includes('proposals') || path.endsWith('/suggestions')) return 'proposal';
  if (path.endsWith('/selections') || path.includes('/selections/')) return 'selection';
  if (/(capability|capabilities|choices|inputs|inventory|source|context|lineage|options|semantic-inventory)$/.test(path)) return 'read';
  if (path.endsWith('/explorers') || /\/explorers\/\{[^}]+\}$/.test(path)) return 'explorer';
  return 'other';
};

const filterPool = (pool, pattern) => uniqueSorted(pool.filter((action) => pattern.test(action.split('(')[0])));
const traceActions = (model) => trace.models?.[model]?.actions ?? [];

const propose = (route, kind, pool) => {
  const readReason = 'Route path is a read/search endpoint. No TLA transition belongs at the route boundary. The route should be contract-tested.';
  switch (kind) {
    case 'read':
      return { proposed: [], confidence: 'high', reason: readReason };
    case 'write':
      return { proposed: [], confidence: 'low', reason: 'Write route. No existing model action covers this mutation. Model it or add a route contract test.' };
    case 'publish':
      return { proposed: [], confidence: 'low', reason: 'Publish route. No existing model action covers publication; model it or add a route contract test.' };
    case 'builder':
      return { proposed: filterPool(pool, /^HydrateBuilder$/), confidence: 'high', reason: 'The GET builder route hydrates the Builder workspace. HydrateBuilder is the route-level transition.' };
    case 'discovery': {
      const matched = filterPool(pool, /Discover|Discovery|Inspect|Search|Browse|Lookup|Request/);
      const fallback = matched.length > 0 ? matched : filterPool(traceActions('BuilderPreview'), /^ScheduleDiscovery$|^DiscoveryComplete$/);
      return {
        proposed: fallback,
        confidence: fallback.length > 0 ? 'medium' : 'low',
        reason: 'Discovery route. The request and completion actions come from the discovery lifecycle; verify the response identity and stale handling.',
      };
    }
    case 'proposal': {
      const matched = filterPool(pool, /Proposal|Propose|Preview/);
      return {
        proposed: matched,
        confidence: matched.length > 0 && matched.length <= 4 ? 'medium' : 'low',
        reason: 'Proposal route. The proposal and preview actions are candidates. Apply actions usually belong to the command route.',
      };
    }
    case 'row-change': {
      const matched = filterPool(pool, /Assessment|Preview|BeginChange|Cancel|ApplyPreview|WorkspaceChanged/);
      return {
        proposed: matched,
        confidence: 'low',
        reason: 'Row-change route combines assessment, preview, and apply behavior. Review the handler to choose the exact route actions.',
      };
    }
    case 'population': {
      const matched = filterPool(pool, /Selection|Route|Coverage|Removal|Exclude|Attach|Replace|Lookup/);
      return {
        proposed: matched,
        confidence: matched.length > 0 && matched.length <= 6 ? 'medium' : 'low',
        reason: 'Population route. The selection, coverage, route, or removal actions are candidates. Review the handler to remove UI-only actions.',
      };
    }
    case 'interpretation': {
      const matched = filterPool(pool, /Review|Apply|Candidate|Create|Revision|Cancel/);
      return {
        proposed: matched,
        confidence: matched.length > 0 && matched.length <= 6 ? 'medium' : 'low',
        reason: 'Interpretation route. The review, revision, or apply actions are candidates. Review the handler for the exact transition.',
      };
    }
    case 'selection': {
      const matched = filterPool(pool, /Selection|Exclude|ParseRoute|SetFeatureFocus|ReplaceExplorer/);
      return {
        proposed: matched,
        confidence: matched.length > 0 && matched.length <= 6 ? 'medium' : 'low',
        reason: 'Selection route. The selection parsing and readiness actions are candidates. Review the handler for the exact transition.',
      };
    }
    case 'explorer': {
      const pattern = route.method === 'DELETE'
        ? /DeleteExplorer|DeleteCapability/
        : route.method === 'POST'
          ? /CreateExplorer|SelectExplorer|HydrateBuilder/
          : /Explorer|HydrateBuilder/;
      const matched = filterPool(pool, pattern);
      return {
        proposed: matched,
        confidence: matched.length > 0 && matched.length <= 8 ? 'medium' : 'low',
        reason: 'Explorer route. The list, create, delete, or select actions are candidates. Review the handler for the exact transition.',
      };
    }
    default:
      return { proposed: [], confidence: 'low', reason: 'No path rule matched. Review the handler and assign an action or contract test.' };
  }
};

const proposals = [];
for (const route of ledger.routes ?? []) {
  const status = route.proof?.status ?? 'missing';
  if (status === 'modeled-and-traced' || status === 'contract-tested' || status === 'excluded') continue;
  if (route.proof?.routeActionReason) continue;
  const kind = classify(route);
  const mode = routeMode(route);
  const pool = uniqueSorted(route.proof?.modelActions ?? []);
  const result = propose(route, kind, pool);
  const evidence = evidenceForRoute(route, tests, browser.cases ?? []);
  proposals.push({
    routeId: route.id,
    tier: route.tier,
    method: route.method,
    path: route.path,
    status,
    routeMode: mode,
    kind,
    currentRouteActions: route.proof?.routeActions ?? [],
    currentModelActions: pool,
    proposedRouteActions: result.proposed,
    confidence: result.confidence,
    reason: result.reason,
    evidence: evidence.evidence,
    reviewRequired: true,
  });
}

proposals.sort((left, right) => left.tier - right.tier || left.path.localeCompare(right.path) || left.method.localeCompare(right.method));
const summary = {
  routes: proposals.length,
  high: proposals.filter((entry) => entry.confidence === 'high').length,
  medium: proposals.filter((entry) => entry.confidence === 'medium').length,
  low: proposals.filter((entry) => entry.confidence === 'low').length,
  readOnly: proposals.filter((entry) => entry.routeMode === 'read').length,
  stateful: proposals.filter((entry) => entry.routeMode === 'stateful').length,
  exactEvidence: proposals.filter((entry) => entry.evidence.some((item) => ['go-route', 'route-proof', 'route-pattern'].includes(item.mapping))).length,
  suffixOnlyEvidence: proposals.filter((entry) => entry.evidence.length > 0 && entry.evidence.every((item) => item.mapping === 'route-suffix' || item.mapping === 'go-route-suffix')).length,
};
const payload = {
  schemaVersion: 1,
  generatedFrom: { ledger: LEDGER, obligations: OBLIGATIONS, browser: BROWSER, trace: TRACE },
  summary,
  proposals,
};
const serialized = JSON.stringify(payload, null, 2) + '\n';
const lines = [];
lines.push('# Builder route action proposals');
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
lines.push('| Tier | Route | Mode | Kind | Confidence | Proposed actions | Evidence |');
lines.push('| ---: | --- | --- | --- | --- | --- | --- |');
for (const entry of proposals) {
  const rendered = entry.evidence.slice(0, 3).map((item) => item.kind + (item.name ? ':' + item.name : '') + (item.file ? ' ' + item.file : ''));
  const evidence = entry.evidence.length === 0
    ? 'none'
    : rendered.join(', ') + (entry.evidence.length > rendered.length ? ' +' + (entry.evidence.length - rendered.length) + ' more' : '');
  lines.push('| ' + [entry.tier, '`' + entry.method + ' ' + entry.path + '`', entry.routeMode, entry.kind, entry.confidence, entry.proposedRouteActions.length === 0 ? 'none' : entry.proposedRouteActions.map((action) => '`' + action + '`').join(', '), evidence].join(' | ') + ' |');
}
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('The proposal is a path-and-affordance heuristic. Every row needs a handler review before it enters `builder-route-overrides.json`.');
const markdown = lines.join('\n') + '\n';

if (check) {
  const currentJson = existsSync(resolve(ROOT, OUTPUT_JSON)) ? readFileSync(resolve(ROOT, OUTPUT_JSON), 'utf8') : null;
  const currentMd = existsSync(resolve(ROOT, OUTPUT_MD)) ? readFileSync(resolve(ROOT, OUTPUT_MD), 'utf8') : null;
  if (currentJson !== serialized || currentMd !== markdown) {
    console.error('FAIL ' + OUTPUT_JSON + ' or ' + OUTPUT_MD + ' is stale; run node scripts/propose-builder-route-actions.mjs');
    process.exit(1);
  }
  console.log('PASS ' + OUTPUT_JSON + ' is current: ' + proposals.length + ' route action proposals');
} else {
  writeFileSync(resolve(ROOT, OUTPUT_JSON), serialized);
  writeFileSync(resolve(ROOT, OUTPUT_MD), markdown);
  console.log('wrote ' + OUTPUT_JSON + ' and ' + OUTPUT_MD + ' with ' + proposals.length + ' proposals');
}
