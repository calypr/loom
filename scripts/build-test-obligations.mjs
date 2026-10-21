#!/usr/bin/env node
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { basename, relative, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const MODEL_DIR = 'verification/builder-interactions';
const LEDGER = MODEL_DIR + '/route-contract.json';
const TRACE_CONTRACT = MODEL_DIR + '/builder-trace-contract.json';
const BROWSER_EVIDENCE = MODEL_DIR + '/browser-evidence.json';
const REGISTRY = 'scripts/verify-ui/registry.mjs';
const FORMAL_RUNNER = 'scripts/verify-builder-formal.mjs';
const UI_ROOT = 'ui/packages/loom-ui/src/features/ExplorerBuilder';
const GO_ROOT = 'internal';
const OUTPUT_JSON = MODEL_DIR + '/test-obligations.json';
const OUTPUT_MD = MODEL_DIR + '/test-obligations.md';

const args = process.argv.slice(2);
const jsonPath = valueFor('--json') ?? OUTPUT_JSON;
const markdownPath = valueFor('--markdown') ?? OUTPUT_MD;
const check = args.includes('--check');

function valueFor(name) {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
}

function fail(message) {
  console.error('FAIL ' + message);
  process.exit(1);
}

function readJson(path) {
  return JSON.parse(readFileSync(resolve(ROOT, path), 'utf8'));
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function walkFiles(root, predicate) {
  const absolute = resolve(ROOT, root);
  const files = [];
  const walk = (directory) => {
    for (const entry of readdirSync(directory)) {
      const path = resolve(directory, entry);
      if (statSync(path).isDirectory()) walk(path);
      else if (predicate(path)) files.push(path);
    }
  };
  walk(absolute);
  return files.sort();
}

function uniqueSorted(values) {
  return [...new Set(values)].sort();
}

function normalizeRoute(value) {
  return value
    .replace(/\{explorer\}/g, '{explorerId}')
    .replace(/\{encodedProject\}/g, '{project}')
    .replace(/\?.*$/, '')
    .replace(/\/+$/, '');
}

function routeKey(method, path) {
  return String(method).toUpperCase() + ' ' + normalizeRoute(String(path));
}

function parseEndpoint(value) {
  const match = String(value).match(/^([A-Z]+)\s+(.+)$/);
  return match ? routeKey(match[1], match[2]) : null;
}

function routePathPattern(path) {
  return path
    .split('/')
    .map((segment) => (/^\{[^}]+\}$/.test(segment) ? '[^/]+' : escapeRegExp(segment)))
    .join('/');
}

function routeSuffix(path) {
  const segments = path.split('/').filter(Boolean);
  const suffix = segments.at(-1) ?? '';
  return /^\{[^}]+\}$/.test(suffix) ? null : suffix;
}

function parseTlaText(text) {
  const definitions = new Set();
  for (const match of text.matchAll(/^([A-Za-z_][A-Za-z0-9_]*)(?:\([^)]*\))?\s*==/gm)) {
    definitions.add(match[1]);
  }
  const next = text.match(/^Next\s*==\s*([\s\S]*?)^Spec\s*==/m);
  if (!next) throw new Error('TLA model has no Next == ... Spec == block');
  const actions = [];
  for (const clause of next[1].split(/\n\s*\\\/\s*/).map((part) => part.replace(/^\\\/\s*/, '')).filter(Boolean)) {
    let target = clause.trim();
    const colon = target.lastIndexOf(':');
    if (target.includes('\\E') && colon >= 0) target = target.slice(colon + 1);
    const names = [...definitions].filter((name) => new RegExp('\\b' + escapeRegExp(name) + '\\b').test(target));
    const action = names.find((name) => !/^Inv_/.test(name) && !['TypeOK', 'Next', 'Spec', 'Init'].includes(name));
    if (action) actions.push(action);
  }
  if (actions.length === 0) throw new Error('TLA Next == block has no parseable action');
  return { actions: uniqueSorted(actions), invariants: [] };
}

function parseTla(modelFile) {
  try {
    return parseTlaText(readFileSync(modelFile, 'utf8'));
  } catch (error) {
    fail(modelFile + ': ' + error.message);
  }
}

function parseInvariantsText(text) {
  return uniqueSorted([...text.matchAll(/^INVARIANT\s+([A-Za-z_][A-Za-z0-9_]*)/gm)]
    .map((match) => match[1])
    .filter((name) => name !== 'TypeOK'));
}

function parseInvariants(cfgFile) {
  return parseInvariantsText(readFileSync(cfgFile, 'utf8'));
}

function selfTestParser() {
  const assert = (condition, message) => {
    if (!condition) fail('parser self-test: ' + message);
  };
  const expectThrow = (label, text) => {
    try {
      parseTlaText(text);
    } catch {
      return;
    }
    fail('parser self-test: ' + label + ' did not fail');
  };
  const twoActions = `---- MODULE TwoActions ----
EXTENDS Naturals
VARIABLES x
Init == x = 0
FirstAction == x = 0 /\\ x' = 1
SecondAction == x = 1 /\\ x' = 0
Next ==
  \\/ FirstAction
  \\/ SecondAction
Spec == Init /\\ [][Next]_x
====`;
  const parsed = parseTlaText(twoActions);
  assert(parsed.actions.includes('FirstAction'), 'dropped the first Next action');
  assert(parsed.actions.includes('SecondAction'), 'dropped the second Next action');
  assert(parsed.actions.length === 2, 'expected 2 actions, got ' + parsed.actions.length);

  const existsBound = `---- MODULE Exists ----
EXTENDS Naturals
VARIABLES x
Requests == {1}
Init == x = 0
Handle(request) == x' = request
Stop == x' = 0
Next ==
  \\/ \\E request \\in Requests :
       Handle(request)
  \\/ Stop
Spec == Init /\\ [][Next]_x
====`;
  const existsParsed = parseTlaText(existsBound);
  assert(existsParsed.actions.includes('Handle'), 'dropped the exists-bound action');
  assert(existsParsed.actions.includes('Stop'), 'dropped the action after the exists bound');

  const singleAction = `---- MODULE Single ----
EXTENDS Naturals
VARIABLES x
Init == x = 0
OnlyAction == x' = 1
Next == OnlyAction
Spec == Init /\\ [][Next]_x
====`;
  assert(parseTlaText(singleAction).actions.join(',') === 'OnlyAction', 'did not parse a single Next action');

  const commented = `---- MODULE Commented ----
EXTENDS Naturals
VARIABLES x
Init == x = 0
FirstAction == x = 0 /\\ x' = 1 \\* first branch
SecondAction == x = 1 /\\ x' = 0
Next ==
  \\/ FirstAction \\* selected branch
  \\/ SecondAction
Spec == Init /\\ [][Next]_x
====`;
  assert(parseTlaText(commented).actions.join(',') === 'FirstAction,SecondAction', 'did not ignore TLA comments');

  const filtered = `---- MODULE Filtered ----
EXTENDS Naturals
VARIABLES x
Init == x = 0
Inv_Bad == x = 1
GoodAction == x' = 1
Next ==
  \\/ Inv_Bad
  \\/ GoodAction
Spec == Init /\\ [][Next]_x
====`;
  assert(parseTlaText(filtered).actions.join(',') === 'GoodAction', 'did not filter Inv_ actions');

  expectThrow('missing Next/Spec', '---- MODULE Missing ----\n====');
  expectThrow('missing Spec', `---- MODULE MissingSpec ----
EXTENDS Naturals
VARIABLES x
Action == x' = 1
Next == Action
====`);
  expectThrow('unknown action', `---- MODULE Unknown ----
EXTENDS Naturals
VARIABLES x
Next == MissingAction
Spec == [][Next]_x
====`);
  expectThrow('only invariant actions', `---- MODULE InvOnly ----
EXTENDS Naturals
VARIABLES x
Inv_Bad == x = 1
Next == Inv_Bad
Spec == [][Next]_x
====`);

  const invariants = parseInvariantsText('INVARIANT TypeOK\nINVARIANT Safety\nINVARIANT Safety\n');
  assert(invariants.join(',') === 'Safety', 'invariant parser did not filter TypeOK or deduplicate');
  console.log('PASS obligation parser self-test: 10 fixtures');
}

function testNameFromFile(text) {
  const names = [];
  const pattern = /\b(?:it|test)(?:\.each\s*\([^)]*\))?\s*\(\s*(['"`])([\s\S]*?)\1/g;
  for (const match of text.matchAll(pattern)) {
    const name = match[2].replace(/\s+/g, ' ').trim();
    if (name) names.push(name);
  }
  return names;
}

function mappingRank(mapping) {
  return {
    'browser-evidence': 6,
    'route-proof': 5,
    'registry-endpoints': 4,
    'go-route': 3,
    'go-route-suffix': 2,
    'keyword-model': 1,
    unmapped: 0,
  }[mapping] ?? 0;
}

function betterMapping(left, right) {
  return mappingRank(left) >= mappingRank(right) ? left : right;
}

if (args.includes('--self-test')) {
  selfTestParser();
  process.exit(0);
}

const ledger = readJson(LEDGER);
const traceContract = readJson(TRACE_CONTRACT);
const browserEvidence = existsSync(resolve(ROOT, BROWSER_EVIDENCE)) ? readJson(BROWSER_EVIDENCE) : { cases: [] };
const routeByKey = new Map();
const actionToRoutes = new Map();
const actionRouteBinding = new Map();
for (const route of ledger.routes ?? []) {
  const key = routeKey(route.method, route.path);
  routeByKey.set(key, route);
  const routeActions = route.proof?.routeActions ?? [];
  const actions = routeActions.length > 0 ? routeActions : route.proof?.modelActions ?? [];
  for (const action of actions) {
    if (!actionToRoutes.has(action)) actionToRoutes.set(action, new Set());
    actionToRoutes.get(action).add(key);
    if (routeActions.length === 0) actionRouteBinding.set(action, 'candidate');
    else if (actionRouteBinding.get(action) !== 'candidate') actionRouteBinding.set(action, 'route');
  }
}

const cfgFiles = walkFiles(MODEL_DIR, (path) => path.endsWith('.cfg'));
const modelFiles = walkFiles(MODEL_DIR, (path) => path.endsWith('.tla'));
const models = new Map();
for (const file of modelFiles) {
  const model = basename(file, '.tla');
  const parsed = parseTla(file);
  const exactCfg = cfgFiles.find((candidate) => basename(candidate) === model + '.cfg');
  const relatedCfg = exactCfg ?? cfgFiles.find((candidate) => basename(candidate).startsWith(model));
  const invariants = relatedCfg ? parseInvariants(relatedCfg) : [];
  models.set(model, { actions: parsed.actions, invariants, cfg: relatedCfg ? relative(ROOT, relatedCfg) : null });
}

const obligations = [];
const actionObligations = new Map();
const actionNameToObligationIds = new Map();
const actionNameToModels = new Map();
for (const [model, definition] of models) {
  const modelRoutes = uniqueSorted([...definition.actions].flatMap((action) => [...(actionToRoutes.get(action) ?? [])]));
  for (const action of definition.actions) {
    const id = model + '.' + action;
    const obligation = {
      id,
      model,
      kind: 'action',
      action,
      invariant: null,
      routes: uniqueSorted([...(actionToRoutes.get(action) ?? [])]),
      tests: [],
      suspectedModels: [],
      routeBinding: actionRouteBinding.get(action) ?? 'unbound',
      status: 'missing',
    };
    obligations.push(obligation);
    actionObligations.set(id, obligation);
    if (!actionNameToObligationIds.has(action)) actionNameToObligationIds.set(action, []);
    actionNameToObligationIds.get(action).push(id);
    if (!actionNameToModels.has(action)) actionNameToModels.set(action, new Set());
    actionNameToModels.get(action).add(model);
  }
  for (const invariant of definition.invariants) {
    const id = model + '.' + invariant;
    obligations.push({
      id,
      model,
      kind: 'invariant',
      action: null,
      invariant,
      routes: modelRoutes,
      tests: [],
      suspectedModels: [model],
      routeBinding: modelRoutes.length > 0 ? 'candidate' : 'unbound',
      status: 'missing',
    });
  }
}

const tests = new Map();
function addTest(record) {
  const key = record.id;
  if (!tests.has(key)) {
    tests.set(key, {
      ...record,
      routes: uniqueSorted(record.routes ?? []),
      obligations: uniqueSorted(record.obligations ?? []),
      suspectedModels: uniqueSorted(record.suspectedModels ?? []),
      suspectedActions: uniqueSorted(record.suspectedActions ?? []),
      checks: record.checks ?? [],
    });
    return tests.get(key);
  }
  const existing = tests.get(key);
  existing.routes = uniqueSorted([...existing.routes, ...(record.routes ?? [])]);
  existing.obligations = uniqueSorted([...existing.obligations, ...(record.obligations ?? [])]);
  existing.suspectedModels = uniqueSorted([...existing.suspectedModels, ...(record.suspectedModels ?? [])]);
  existing.suspectedActions = uniqueSorted([...existing.suspectedActions, ...(record.suspectedActions ?? [])]);
  existing.checks = uniqueSorted([...existing.checks, ...(record.checks ?? [])]);
  existing.mapping = betterMapping(existing.mapping, record.mapping);
  return existing;
}

function actionIdsFor(action) {
  const exact = actionNameToObligationIds.get(action) ?? [];
  if (exact.length) return exact;
  return actionNameToObligationIds.get(action.replace(/\(.*\)$/, '')) ?? [];
}

function obligationIdsForRoute(route) {
  if (!route?.proof?.model) return [];
  const model = route.proof.model.replace(/\.tla$/, '');
  const actions = route.proof.coveredActions?.length ? route.proof.coveredActions : route.proof.modelActions ?? [];
  return uniqueSorted(actions.flatMap((action) => {
    return actionIdsFor(action).filter((id) => id.startsWith(model + '.'));
  }));
}

function suspectedActionsForRoutes(routes) {
  return uniqueSorted(routes.flatMap((key) => {
    const route = routeByKey.get(key);
    return route?.proof?.routeActions?.length ? route.proof.routeActions : route?.proof?.modelActions ?? [];
  }));
}

function suspectedModelsForRoutes(routes) {
  return uniqueSorted(routes.flatMap((key) => {
    const route = routeByKey.get(key);
    return suspectedActionsForRoutes([key]).flatMap((action) => {
      const modelsForAction = actionNameToModels.get(action) ?? actionNameToModels.get(action.replace(/\(.*\)$/, '')) ?? [];
      return [...modelsForAction];
    });
  }));
}

function routeObligations(routes) {
  return uniqueSorted(routes.flatMap((key) => {
    const route = routeByKey.get(key);
    return route ? obligationIdsForRoute(route) : [];
  }));
}

const browserCases = new Map((browserEvidence.cases ?? []).map((entry) => [entry.id, entry]));
const { registry } = await import(pathToFileURL(resolve(ROOT, REGISTRY)).href);
for (const entry of registry) {
  const entryRoutes = uniqueSorted((entry.endpoints ?? []).map(parseEndpoint).filter(Boolean));
  for (const [caseName, caseData] of Object.entries(entry.cases ?? {})) {
    const id = entry.id + ':' + caseName;
    const evidence = browserCases.get(id);
    const routes = evidence?.coveredRoutes?.length ? uniqueSorted(evidence.coveredRoutes) : entryRoutes;
    addTest({
      id: 'playwright:' + id,
      kind: 'playwright',
      level: 'browser',
      name: caseData.playwrightGrep ?? caseName,
      caseName,
      file: caseData.playwrightTest ?? entry.script ?? REGISTRY,
      routes,
      obligations: evidence ? routeObligations(routes) : [],
      suspectedModels: suspectedModelsForRoutes(routes),
      checks: Array.isArray(caseData.requiredChecks) ? caseData.requiredChecks : [],
      focusedChecks: uniqueSorted((caseData.focusedChecks ?? []).map((check) => check.id)),
      mapping: evidence ? 'browser-evidence' : 'registry-endpoints',
      evidence: evidence ? BROWSER_EVIDENCE : REGISTRY,
    });
  }
}

for (const [caseId, evidence] of browserCases) {
  const existing = tests.get('playwright:' + caseId);
  if (existing) {
    existing.mapping = 'browser-evidence';
    existing.evidence = BROWSER_EVIDENCE;
    existing.checks = uniqueSorted([...existing.checks, ...(evidence.assertions ?? []).map((assertion) => assertion.name)]);
  }
}

const modelKeywords = [
  { model: 'ReconcileLifecycle', pattern: /reconcil/i },
  { model: 'BuilderPreview', pattern: /preview|proposal|apply|applyack|beginapply/i },
  { model: 'ConstructionLifecycle', pattern: /construction|proposal|apply/i },
  { model: 'RowDefinitionLifecycle', pattern: /row.?definition|row definition/i },
  { model: 'Population', pattern: /population|selection|cohort/i },
  { model: 'Interpretation', pattern: /interpretation|feature meaning/i },
  { model: 'RootChange', pattern: /root.?change|root change/i },
  { model: 'DraftRestore', pattern: /draft.?restore|undo/i },
  { model: 'ColumnPresentation', pattern: /column|presentation/i },
  { model: 'WorkspaceLifecycle', pattern: /explorer|hydration|workspace/i },
  { model: 'LifecycleEntry', pattern: /command|receipt|selection|route/i },
  { model: 'SupportingSurfaces', pattern: /lineage|frame|graph|review/i },
  { model: 'BuilderOperations', pattern: /step|operation|editor/i },
];

const vitestFiles = walkFiles(UI_ROOT, (path) => /\.(test|spec)\.(ts|tsx)$/.test(path));
for (const file of vitestFiles) {
  const relativeFile = relative(ROOT, file);
  const text = readFileSync(file, 'utf8');
  for (const name of testNameFromFile(text)) {
    const haystack = relativeFile + ' ' + name;
    const suspectedModels = uniqueSorted(modelKeywords.filter((entry) => entry.pattern.test(haystack)).map((entry) => entry.model));
    addTest({
      id: 'vitest:' + relativeFile + '::' + name,
      kind: 'vitest',
      level: 'unit',
      name,
      file: relativeFile,
      routes: [],
      obligations: [],
      suspectedModels,
      mapping: suspectedModels.length > 0 ? 'keyword-model' : 'unmapped',
      evidence: relativeFile,
    });
  }
}

const formalRunner = readFileSync(resolve(ROOT, FORMAL_RUNNER), 'utf8');
const formalGoTests = uniqueSorted([
  ...[...formalRunner.matchAll(/'(Test[A-Za-z0-9_]+)'/g)].map((match) => match[1]),
]);
const formalGoSet = new Set(formalGoTests);

const goTests = [];
for (const file of walkFiles(GO_ROOT, (path) => path.endsWith('_test.go'))) {
  const relativeFile = relative(ROOT, file);
  const text = readFileSync(file, 'utf8');
  const functions = [...text.matchAll(/^func (Test[A-Za-z0-9_]+)\(/gm)];
  for (let index = 0; index < functions.length; index += 1) {
    const match = functions[index];
    const start = match.index;
    const end = index + 1 < functions.length ? functions[index + 1].index : text.length;
    const body = text.slice(start, end);
    if (!body.includes('/authoring/v2/') && !formalGoSet.has(match[1])) continue;
    const routes = [];
    let mapping = 'unmapped';
    for (const route of ledger.routes ?? []) {
      if (new RegExp(routePathPattern(route.path)).test(body)) {
        routes.push(routeKey(route.method, route.path));
        mapping = 'go-route';
        continue;
      }
      const suffix = routeSuffix(route.path);
      if (!suffix || !route.path.includes('/authoring/v2/')) continue;
      if (new RegExp('["\'`]\\/' + escapeRegExp(suffix) + '["\'`]').test(body)) {
        routes.push(routeKey(route.method, route.path));
        mapping = betterMapping(mapping, 'go-route-suffix');
      }
    }
    goTests.push({
      id: 'go:' + relativeFile + '::' + match[1],
      kind: 'go',
      level: 'server',
      name: match[1],
      file: relativeFile,
      routes: uniqueSorted(routes),
      obligations: routeObligations(uniqueSorted(routes)),
      suspectedModels: suspectedModelsForRoutes(uniqueSorted(routes)),
      suspectedActions: suspectedActionsForRoutes(uniqueSorted(routes)),
      mapping,
      evidence: relativeFile,
    });
  }
}

for (const test of goTests) {
  addTest({
    ...test,
    id: 'go:' + test.file + '::' + test.name,
  });
}

for (const route of ledger.routes ?? []) {
  if (route.proof?.status !== 'modeled-and-traced') continue;
  const routeKeyValue = routeKey(route.method, route.path);
  const actionIds = obligationIdsForRoute(route);
  if (route.proof.goTest) {
    const match = [...tests.values()].find((test) => test.kind === 'go' && test.name === route.proof.goTest);
    if (match) {
      match.routes = uniqueSorted([...match.routes, routeKeyValue]);
      match.obligations = uniqueSorted([...match.obligations, ...actionIds]);
      match.mapping = betterMapping(match.mapping, 'route-proof');
      match.evidence = route.proof.goTest;
    } else {
      addTest({
        id: 'go:' + route.proof.goTest,
        kind: 'go',
        level: 'server',
        name: route.proof.goTest,
        file: route.proof.uiTest ?? FORMAL_RUNNER,
        routes: [routeKeyValue],
        obligations: actionIds,
        mapping: 'route-proof',
        evidence: route.proof.goTest,
      });
    }
  }
  if (route.proof.uiTest && route.proof.schedule) {
    const schedule = traceContract.schedules?.[route.proof.schedule];
    const name = schedule?.test ?? route.proof.schedule;
    const file = route.proof.uiTest;
    const existing = [...tests.values()].find((test) => test.kind === 'vitest' && test.file === file && test.name.includes(name));
    if (existing) {
      existing.routes = uniqueSorted([...existing.routes, routeKeyValue]);
      existing.obligations = uniqueSorted([...existing.obligations, ...actionIds]);
      existing.mapping = 'route-proof';
      existing.evidence = route.proof.schedule;
    } else {
      addTest({
        id: 'vitest:' + file + '::' + name,
        kind: 'vitest',
        level: 'unit',
        name,
        file,
        routes: [routeKeyValue],
        obligations: actionIds,
        mapping: 'route-proof',
        evidence: route.proof.schedule,
      });
    }
  }
  if (route.proof.browserCase) {
    const match = tests.get('playwright:' + route.proof.browserCase);
    if (match) {
      match.routes = uniqueSorted([...match.routes, routeKeyValue]);
      match.obligations = uniqueSorted([...match.obligations, ...actionIds]);
      match.mapping = 'route-proof';
    } else {
      addTest({
        id: 'playwright:' + route.proof.browserCase,
        kind: 'playwright',
        level: 'browser',
        name: route.proof.browserCase,
        file: BROWSER_EVIDENCE,
        routes: [routeKeyValue],
        obligations: actionIds,
        mapping: 'route-proof',
        evidence: BROWSER_EVIDENCE,
      });
    }
  }
}

for (const obligation of obligations) {
  for (const test of tests.values()) {
    if (test.obligations.includes(obligation.id)) {
      obligation.tests.push({
        id: test.id,
        kind: test.kind,
        level: test.level,
        name: test.name,
        file: test.file,
        mapping: test.mapping,
      });
    }
  }
  obligation.tests = obligation.tests.sort((left, right) => left.level.localeCompare(right.level) || left.name.localeCompare(right.name));
  if (obligation.tests.some((test) => mappingRank(test.mapping) >= mappingRank('go-route'))) {
    obligation.status = 'covered';
  } else if (obligation.tests.length > 0 || obligation.suspectedModels.length > 0) {
    obligation.status = 'partial';
  }
}

const obligationsById = new Map(obligations.map((obligation) => [obligation.id, obligation]));
for (const obligation of obligations) {
  if (obligation.kind !== 'invariant') continue;
  const modelActions = models.get(obligation.model)?.actions ?? [];
  const testsForModel = new Set();
  for (const action of modelActions) {
    for (const test of obligationsById.get(obligation.model + '.' + action)?.tests ?? []) {
      testsForModel.add(test.id);
    }
  }
  obligation.tests = [...testsForModel].map((id) => {
    const test = tests.get(id);
    return {
      id: test.id,
      kind: test.kind,
      level: test.level,
      name: test.name,
      file: test.file,
      mapping: 'model-action',
    };
  }).sort((left, right) => left.level.localeCompare(right.level) || left.name.localeCompare(right.name));
  if (obligation.tests.length > 0) obligation.status = 'partial';
}

const exactTests = [...tests.values()].filter((test) => test.obligations.length > 0);
const unmappedTests = [...tests.values()].filter((test) => test.obligations.length === 0);
const gaps = obligations.filter((obligation) => obligation.status === 'missing');
const partial = obligations.filter((obligation) => obligation.status === 'partial');
const covered = obligations.filter((obligation) => obligation.status === 'covered');

const obligationDuplicates = obligations
  .map((obligation) => ({
    obligation: obligation.id,
    byLevel: Object.fromEntries(['browser', 'unit', 'server', 'model'].map((level) => [
      level,
      obligation.tests.filter((test) => test.level === level).map((test) => test.id),
    ]).filter(([, ids]) => ids.length > 1)),
  }))
  .filter((entry) => Object.keys(entry.byLevel).length > 0);

const testNameGroups = new Map();
for (const test of tests.values()) {
  if (!['vitest', 'go'].includes(test.kind)) continue;
  const key = test.kind + ':' + test.name.toLowerCase().replace(/\s+/g, ' ').trim();
  if (!testNameGroups.has(key)) testNameGroups.set(key, []);
  testNameGroups.get(key).push(test);
}
const duplicateTestNames = [...testNameGroups.entries()]
  .filter(([, entries]) => entries.length > 1)
  .map(([name, entries]) => ({ name, tests: entries.map((test) => test.id) }));

const checkGroups = new Map();
for (const test of tests.values()) {
  if (test.kind !== 'playwright') continue;
  for (const check of test.checks) {
    if (!checkGroups.has(check)) checkGroups.set(check, []);
    checkGroups.get(check).push(test.id);
  }
}
const duplicateChecks = [...checkGroups.entries()]
  .filter(([, ids]) => ids.length > 1)
  .map(([check, ids]) => ({ check, tests: ids }));

const sharedRouteProofTests = new Map();
for (const route of ledger.routes ?? []) {
  if (route.proof?.status !== 'modeled-and-traced') continue;
  for (const [kind, value] of [['uiTest', route.proof.uiTest], ['goTest', route.proof.goTest], ['browserCase', route.proof.browserCase]]) {
    if (!value) continue;
    const key = kind + ':' + value;
    if (!sharedRouteProofTests.has(key)) sharedRouteProofTests.set(key, []);
    sharedRouteProofTests.get(key).push(route.id);
  }
}
const sharedProofs = [...sharedRouteProofTests.entries()]
  .filter(([, routes]) => routes.length > 1)
  .map(([test, routes]) => ({ test, routes }));

const summary = {
  models: models.size,
  actions: obligations.filter((obligation) => obligation.kind === 'action').length,
  invariants: obligations.filter((obligation) => obligation.kind === 'invariant').length,
  obligations: obligations.length,
  covered: covered.length,
  partial: partial.length,
  missing: gaps.length,
  missingRouteBound: gaps.filter((obligation) => obligation.routeBinding === 'route').length,
  missingCandidate: gaps.filter((obligation) => obligation.routeBinding === 'candidate').length,
  missingUnbound: gaps.filter((obligation) => obligation.routeBinding === 'unbound').length,
  tests: tests.size,
  testsWithObligations: exactTests.length,
  testsWithoutObligations: unmappedTests.length,
  obligationsWithMultipleTests: obligationDuplicates.length,
  duplicateTestNames: duplicateTestNames.length,
  repeatedPlaywrightChecks: duplicateChecks.length,
  sharedProofs: sharedProofs.length,
  candidateRouteBindings: obligations.filter((obligation) => obligation.routeBinding === 'candidate').length,
};

const report = {
  schemaVersion: 1,
  sources: {
    modelDir: MODEL_DIR,
    routeLedger: LEDGER,
    traceContract: TRACE_CONTRACT,
    browserEvidence: existsSync(resolve(ROOT, BROWSER_EVIDENCE)) ? BROWSER_EVIDENCE : null,
    registry: REGISTRY,
    formalRunner: FORMAL_RUNNER,
    uiRoot: UI_ROOT,
    goRoot: GO_ROOT,
  },
  summary,
  obligations,
  tests: [...tests.values()].sort((left, right) => left.kind.localeCompare(right.kind) || left.name.localeCompare(right.name)),
  duplicates: {
    multipleTests: obligationDuplicates,
    testNames: duplicateTestNames,
    checks: duplicateChecks,
    sharedProofs,
  },
  gaps: gaps.map((obligation) => obligation.id),
  partial: partial.map((obligation) => obligation.id),
};

const serialized = JSON.stringify(report, null, 2) + '\n';
if (check) {
  if (!existsSync(resolve(ROOT, jsonPath))) fail(jsonPath + ' does not exist');
  if (readFileSync(resolve(ROOT, jsonPath), 'utf8') !== serialized) {
    fail(jsonPath + ' is stale; run node scripts/build-test-obligations.mjs');
  }
  console.log('PASS ' + jsonPath + ' is current');
} else {
  writeFileSync(resolve(ROOT, jsonPath), serialized);
}

const lines = [];
lines.push('# Test obligations');
lines.push('');
lines.push('Status: generated from the TLA models and existing test metadata.');
lines.push('');
lines.push('## Summary');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(summary)) lines.push('| ' + key + ' | ' + value + ' |');
lines.push('');
lines.push('## Obligations with no exact test');
lines.push('');
for (const obligation of gaps.slice(0, 80)) lines.push('- `' + obligation.id + '` (' + obligation.routes.length + ' route(s))');
if (gaps.length > 80) lines.push('- ' + (gaps.length - 80) + ' more in the JSON report.');
lines.push('');
lines.push('## Obligations with exact tests');
lines.push('');
for (const obligation of covered.slice(0, 80)) {
  lines.push('- `' + obligation.id + '`: ' + obligation.tests.map((test) => test.kind + ' `' + test.name + '`').join(', '));
}
if (covered.length > 80) lines.push('- ' + (covered.length - 80) + ' more in the JSON report.');
lines.push('');
lines.push('## Obligations with multiple tests');
lines.push('');
for (const entry of obligationDuplicates.slice(0, 80)) {
  const tests = Object.entries(entry.byLevel).flatMap(([level, ids]) => ids.map((id) => level + ':' + id));
  lines.push('- `' + entry.obligation + '`: ' + Object.values(entry.byLevel).reduce((sum, ids) => sum + ids.length, 0) + ' tests (' + tests.join(', ') + ')');
}
if (obligationDuplicates.length === 0) lines.push('No obligation has more than one test at the same level.');
lines.push('');
lines.push('## Duplicate test names');
lines.push('');
for (const entry of duplicateTestNames.slice(0, 40)) lines.push('- `' + entry.name + '`: ' + entry.tests.length + ' tests (' + entry.tests.join(', ') + ')');
if (duplicateTestNames.length === 0) lines.push('No duplicate test names found.');
lines.push('');
lines.push('## Repeated Playwright checks');
lines.push('');
for (const entry of duplicateChecks.slice(0, 40)) lines.push('- `' + entry.check + '`: ' + entry.tests.length + ' cases (' + entry.tests.join(', ') + ')');
if (duplicateChecks.length === 0) lines.push('No Playwright check is reused across cases.');
lines.push('');
lines.push('## Tests with no exact obligation');
lines.push('');
for (const test of unmappedTests.slice(0, 80)) {
  const suspects = test.suspectedModels?.length ? '; suspected models: ' + test.suspectedModels.join(', ') : '';
  const actions = test.suspectedActions?.length ? '; suspected actions: ' + test.suspectedActions.join(', ') : '';
  lines.push('- `' + test.kind + '` `' + test.name + '` in `' + test.file + '`' + suspects + actions);
}
if (unmappedTests.length > 80) lines.push('- ' + (unmappedTests.length - 80) + ' more in the JSON report.');
lines.push('');
lines.push('## Shared route proof evidence');
lines.push('');
for (const entry of sharedProofs) lines.push('- `' + entry.test + '`: ' + entry.routes.join(', '));
if (sharedProofs.length === 0) lines.push('No shared route proof evidence found.');
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('The test index is heuristic. Registry endpoints, Go route strings, and route proofs are strong links. Vitest test names are keyword links. The report marks the mapping on every test.');
lines.push('');

if (check) {
  if (!existsSync(resolve(ROOT, markdownPath)) || readFileSync(resolve(ROOT, markdownPath), 'utf8') !== lines.join('\n')) {
    fail(markdownPath + ' is stale; run node scripts/build-test-obligations.mjs');
  }
} else {
  writeFileSync(resolve(ROOT, markdownPath), lines.join('\n'));
  console.log('wrote ' + jsonPath + ' and ' + markdownPath + ' with ' + obligations.length + ' obligations and ' + tests.size + ' tests');
}
