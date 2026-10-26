#!/usr/bin/env node
import { readUiContractInventory } from './lib/ui-contract-inventory.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const INVENTORY = 'docs/ui-contract-inventory/ui-supported-affordances.json';
const OVERRIDES = 'verification/builder-interactions/builder-route-overrides.json';
const MODEL_MAP = 'verification/builder-interactions/affordance-model-map.json';
const GENERATED_ROUTES = 'generated/loomapi/api.gen.go';
const OUTPUT = 'verification/builder-interactions/route-contract.json';
const BUILDER_SOURCE_PREFIX = 'ui/packages/loom-ui/src/features/ExplorerBuilder/';

const TIER1_PATHS = [
  '/authoring/v2/builder',
  '/authoring/v2/commands',
  '/authoring/v2/reconcile',
  '/authoring/v2/preview',
  '/authoring/v2/construction-proposals',
  '/authoring/v2/construction-category-discoveries',
  '/authoring/v2/construction-choice-proposals',
  '/authoring/v2/row-change',
  '/authoring/v2/row-definition-proposals',
  '/authoring/v2/table-shape-proposals',
  '/authoring/v2/table-shape-resolutions',
  '/authoring/v2/table-shape-category-discoveries',
  '/authoring/v2/population-mapping',
  '/authoring/v2/population-member-proposals',
  '/authoring/v2/population-routes',
  '/authoring/v2/interpretation-preview',
  '/authoring/v2/interpretation-revisions',
  '/authoring/v2/publish',
  '/selections',
  '/explicit-groups',
];

const normalizePath = (value) =>
  value
    .trim()
    .replace(/^[A-Z]+\s+/, '')
    .replace(/\?.*$/, '')
    .replace(/\[.*$/, '')
    .replace(/\{encodedProject\}/g, '{project}')
    .replace(/\/+$/, '');

const isPath = (value) => typeof value === 'string' && normalizePath(value).startsWith('/');
const tierFor = (path) => TIER1_PATHS.some((candidate) => path === candidate || path.includes(candidate)) ? 1 : 2;
const routeKey = (method, path) => method + ' ' + path;
const routeId = (method, path) => method.toLowerCase() + ':' + path.replace(/[^a-zA-Z0-9]+/g, '-').replace(/^-|-$/g, '');
const fail = (message) => {
  console.error('FAIL ' + message);
  process.exit(1);
};

const builderEvidenceFrom = (value) => {
  const paths = new Set();
  const visit = (node) => {
    if (typeof node === 'string') {
      if (node.startsWith(BUILDER_SOURCE_PREFIX)) {
        paths.add(node.replace(/:\d+(?:-\d+)?$/, ''));
      }
      return;
    }
    if (Array.isArray(node)) {
      for (const item of node) visit(item);
      return;
    }
    if (!node || typeof node !== 'object') return;
    for (const item of Object.values(node)) visit(item);
  };
  visit(value);
  return [...paths].sort();
};

const modelMap = JSON.parse(readFileSync(resolve(ROOT, MODEL_MAP), 'utf8'));
const modelByAffordance = new Map((modelMap.affordances ?? []).map((entry) => [entry.id, entry]));
const proofFor = (uiCallers) => {
  const modelActions = new Set();
  for (const caller of uiCallers) {
    const entry = modelByAffordance.get(caller);
    for (const action of entry?.modelActions ?? []) modelActions.add(action);
  }
  const actions = [...modelActions].sort();
  return {
    status: actions.length > 0 ? 'modeled-only' : 'unproven',
    model: null,
    modelActions: actions,
    routeActions: [],
    routeActionReason: null,
    traceEvent: null,
    schedule: null,
    contractTest: null,
    goTest: null,
    uiTest: null,
    browserCase: null,
    reason: actions.length > 0
      ? 'Affordance model actions exist; route-level trace, schedule, and executable test bindings are not assigned.'
      : 'No model action is mapped to this route; assign a model or exclude the route.',
  };
};

const found = [];
const walk = (value, caller, inheritedEvidence = []) => {
  if (Array.isArray(value)) {
    for (const item of value) walk(item, caller, inheritedEvidence);
    return;
  }
  if (!value || typeof value !== 'object') return;
  const localEvidence = builderEvidenceFrom({
    evidence: value.evidence,
    requestContract: value.requestContract,
    capabilityFixture: value.capabilityFixture,
    seed: value.seed,
  });
  const evidence = localEvidence.length > 0 ? localEvidence : inheritedEvidence;
  if (isPath(value.endpoint)) {
    found.push({
      path: normalizePath(value.endpoint),
      method: typeof value.method === 'string' && /^[A-Z]+$/.test(value.method) ? value.method : 'UNKNOWN',
      caller,
      builderEvidence: evidence,
    });
  }
  for (const item of Object.values(value)) walk(item, caller, evidence);
};
const inventory = readUiContractInventory(ROOT);
for (const affordance of inventory.affordances ?? []) {
  walk(affordance, affordance.id, builderEvidenceFrom(affordance));
}

const byPath = new Map();
for (const entry of found) {
  if (!byPath.has(entry.path)) {
    byPath.set(entry.path, { methods: new Set(), callers: new Set(), builderEvidence: new Set() });
  }
  const route = byPath.get(entry.path);
  route.methods.add(entry.method);
  if (entry.caller) route.callers.add(entry.caller);
  for (const evidence of entry.builderEvidence) route.builderEvidence.add(evidence);
}

const routes = new Map();
for (const [path, entry] of byPath) {
  const known = [...entry.methods].filter((method) => method !== 'UNKNOWN').sort();
  if (known.length === 0) continue;
  for (const method of known) {
    const key = routeKey(method, path);
    const builderEvidence = [...entry.builderEvidence].sort();
    if (builderEvidence.length === 0) fail('inventory route has no mounted Builder evidence: ' + key);
    const uiCallers = [...entry.callers].sort();
    routes.set(key, {
      id: routeId(method, path),
      tier: tierFor(path),
      method,
      path,
      usedByBuilder: true,
      builderEvidence,
      uiCallers,
      requestIdentity: [],
      responseBinding: [],
      staleRule: '',
      errorClasses: [],
      proof: proofFor(uiCallers),
    });
  }
}

const generatedText = readFileSync(resolve(ROOT, GENERATED_ROUTES), 'utf8');
const generatedAuthoringRoutes = new Set();
for (const match of generatedText.matchAll(/router\.(Get|Post|Put|Delete|Patch)\(options\.BaseURL\+"([^"]+)"/g)) {
  const method = match[1].toUpperCase();
  const path = match[2].replace(/:([A-Za-z]+)/g, '{$1}');
  if (path.includes('/authoring/v2/')) generatedAuthoringRoutes.add(routeKey(method, path));
}

const overrides = JSON.parse(readFileSync(resolve(ROOT, OVERRIDES), 'utf8'));
for (const route of overrides.routes ?? []) {
  const key = routeKey(route.method, route.path);
  if (!generatedAuthoringRoutes.has(key)) fail('override route is not in the generated authoring API: ' + key);
  if (routes.has(key)) fail('override route duplicates an inventory route: ' + key);
  for (const evidence of route.evidence ?? []) {
    const text = readFileSync(resolve(ROOT, evidence.path), 'utf8');
    if (!text.includes(evidence.needle)) {
      fail('override evidence ' + evidence.path + ' does not contain ' + evidence.needle + ' for ' + key);
    }
  }
  const builderEvidence = builderEvidenceFrom((route.evidence ?? []).map((evidence) => evidence.path));
  if (builderEvidence.length === 0) fail('override route has no mounted Builder evidence: ' + key);
  const uiCallers = [...route.uiCallers].sort();
  routes.set(key, {
    id: routeId(route.method, route.path),
    tier: route.tier,
    method: route.method,
    path: route.path,
    usedByBuilder: true,
    builderEvidence,
    uiCallers,
    requestIdentity: [],
    responseBinding: [],
    staleRule: '',
    errorClasses: [],
    proof: proofFor(uiCallers),
  });
}

for (const entry of overrides.proofs ?? []) {
  const key = routeKey(entry.method, entry.path);
  if (!routes.has(key)) fail('proof route is not in the ledger: ' + key);
  const route = routes.get(key);
  route.proof = { ...route.proof, ...entry.proof };
}

const excludedAuthoringRoutes = new Map();
for (const route of overrides.excludedAuthoringRoutes ?? []) {
  const key = routeKey(route.method, route.path);
  if (!generatedAuthoringRoutes.has(key)) fail('excluded route is not in the generated authoring API: ' + key);
  if (routes.has(key)) fail('excluded route is already in the ledger: ' + key);
  excludedAuthoringRoutes.set(key, {
    method: route.method,
    path: route.path,
    reason: route.reason,
    usedByBuilder: false,
    proof: {
      status: 'excluded',
      model: null,
      modelActions: [],
      routeActions: [],
      routeActionReason: null,
      traceEvent: null,
      schedule: null,
      contractTest: null,
      goTest: null,
      uiTest: null,
      browserCase: null,
      reason: route.reason,
    },
  });
}

const unaccountedAuthoringRoutes = [...generatedAuthoringRoutes]
  .filter((key) => !routes.has(key) && !excludedAuthoringRoutes.has(key))
  .sort();
const ledgerAuthoringRoutes = [...routes.keys()].filter((key) => key.includes('/authoring/v2/')).length;
const proofCounts = {
  modeledAndTraced: [...routes.values()].filter((route) => route.proof.status === 'modeled-and-traced').length,
  traceTested: [...routes.values()].filter((route) => route.proof.status === 'trace-tested').length,
  modeledOnly: [...routes.values()].filter((route) => route.proof.status === 'modeled-only').length,
  contractTested: [...routes.values()].filter((route) => route.proof.status === 'contract-tested').length,
  unproven: [...routes.values()].filter((route) => route.proof.status === 'unproven').length,
};

const payload = {
  schemaVersion: 2,
  generatedFrom: INVENTORY,
  overridesFrom: OVERRIDES,
  proofsFrom: OVERRIDES,
  modelMapFrom: MODEL_MAP,
  generatedRouteSource: GENERATED_ROUTES,
  status: 'generated-provisional; route-level proof pending',
  scope: {
    generatedAuthoringRoutes: generatedAuthoringRoutes.size,
    builderRoutes: routes.size,
    ledgerAuthoringRoutes,
    excludedAuthoringRoutes: excludedAuthoringRoutes.size,
    unaccountedAuthoringRoutes,
    ...proofCounts,
  },
  excludedAuthoringRoutes: [...excludedAuthoringRoutes.values()]
    .sort((left, right) => left.path.localeCompare(right.path) || left.method.localeCompare(right.method)),
  routes: [...routes.values()].sort((left, right) => left.tier - right.tier || left.path.localeCompare(right.path) || left.method.localeCompare(right.method)),
};
const serialized = JSON.stringify(payload, null, 2) + '\n';
if (process.argv.includes('--check')) {
  const current = readFileSync(resolve(ROOT, OUTPUT), 'utf8');
  if (current !== serialized) fail(OUTPUT + ' is stale; run node scripts/generate-builder-route-ledger.mjs');
  console.log('PASS ' + OUTPUT + ' is current: ' + payload.routes.length + ' Builder routes, ' +
    excludedAuthoringRoutes.size + ' excluded authoring routes, ' + unaccountedAuthoringRoutes.length + ' unaccounted authoring routes');
} else {
  writeFileSync(resolve(ROOT, OUTPUT), serialized);
  console.log('wrote ' + OUTPUT + ' with ' + payload.routes.length + ' Builder routes, ' +
    excludedAuthoringRoutes.size + ' excluded authoring routes, ' + unaccountedAuthoringRoutes.length + ' unaccounted authoring routes');
}
