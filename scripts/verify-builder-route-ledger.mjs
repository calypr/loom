#!/usr/bin/env node
import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const LEDGER = 'verification/builder-interactions/route-contract.json';
const TRACE_CONTRACT = 'verification/builder-interactions/builder-trace-contract.json';
const BROWSER_EVIDENCE = 'verification/builder-interactions/browser-evidence.json';
const FORMAL_SCRIPT = 'scripts/verify-builder-formal.mjs';
const STATUSES = new Set(['modeled-and-traced', 'trace-tested', 'modeled-only', 'contract-tested', 'unproven', 'excluded']);

const fail = (message) => {
  console.error('FAIL ' + message);
  process.exit(1);
};

const reportOnly = process.argv.includes('--report-only');
const strict = process.argv.includes('--strict') || !reportOnly;
const routeFilterIndex = process.argv.indexOf('--route');
const routeFilter = routeFilterIndex >= 0 ? process.argv[routeFilterIndex + 1] : null;
const payload = JSON.parse(readFileSync(resolve(ROOT, LEDGER), 'utf8'));
if (payload.schemaVersion !== 2 || !Array.isArray(payload.routes)) fail('route ledger schema is invalid');
const selectedRoutes = routeFilter
  ? payload.routes.filter((route) => route.id === routeFilter || route.method + ' ' + route.path === routeFilter)
  : payload.routes;
if (routeFilter && selectedRoutes.length === 0) fail('route filter matched no routes: ' + routeFilter);
const traceContract = JSON.parse(readFileSync(resolve(ROOT, TRACE_CONTRACT), 'utf8'));
const browserEvidence = JSON.parse(readFileSync(resolve(ROOT, BROWSER_EVIDENCE), 'utf8'));
const formalScript = readFileSync(resolve(ROOT, FORMAL_SCRIPT), 'utf8');
const eventNames = new Set([
  ...Object.values(traceContract.actionBindings ?? {}).map((binding) => binding.event),
  ...(traceContract.phase1?.requiredEvents ?? []),
]);
const schedules = traceContract.schedules ?? {};
const browserCases = new Map((browserEvidence.cases ?? []).map((entry) => [entry.id, entry]));

const ids = new Set();
const problems = [];
const hasTest = (proof) => Boolean(proof?.contractTest || proof?.goTest || proof?.uiTest);
for (const route of selectedRoutes) {
  if (!route.id || typeof route.id !== 'string') problems.push('route without id');
  else if (ids.has(route.id)) problems.push('duplicate route id ' + route.id);
  else ids.add(route.id);
  if (![1, 2, 3].includes(route.tier)) problems.push(route.id + ': tier must be 1, 2, or 3');
  if (typeof route.method !== 'string' || typeof route.path !== 'string') problems.push(route.id + ': method and path are required');
  if (route.usedByBuilder !== true) problems.push(route.id + ': Builder route must have usedByBuilder=true');
  if (!Array.isArray(route.builderEvidence) || route.builderEvidence.length === 0) {
    problems.push(route.id + ': Builder route needs mounted Builder evidence');
  } else {
    for (const evidence of route.builderEvidence) {
      if (typeof evidence !== 'string' || !existsSync(resolve(ROOT, evidence))) {
        problems.push(route.id + ': Builder evidence does not exist: ' + evidence);
      }
    }
  }
  if (!Array.isArray(route.uiCallers) || route.uiCallers.length === 0) problems.push(route.id + ': UI callers are required');
  if (!route.proof || typeof route.proof !== 'object') {
    problems.push(route.id + ': proof object is required');
    continue;
  }
  if (!STATUSES.has(route.proof.status)) problems.push(route.id + ': unknown proof status ' + route.proof.status);
  if (!Array.isArray(route.proof.modelActions)) problems.push(route.id + ': proof.modelActions must be an array');
  if (route.proof.status === 'modeled-and-traced' || route.proof.status === 'trace-tested') {
    if (!route.proof.scope) problems.push(route.id + ': modeled-and-traced route needs a scope');
    if (!Array.isArray(route.proof.coveredActions) || route.proof.coveredActions.length === 0) {
      problems.push(route.id + ': modeled-and-traced route needs covered actions');
    } else {
      const modelName = route.proof.model?.replace(/\.tla$/, '');
      const modelActions = traceContract.models?.[modelName]?.actions ?? [];
      for (const action of route.proof.coveredActions) {
        if (!modelActions.includes(action)) {
          problems.push(route.id + ': covered action is not in the model: ' + modelName + '.' + action);
        }
      }
    }
    if (!route.proof.model) problems.push(route.id + ': modeled-and-traced route needs a model');
    else if (!existsSync(resolve(ROOT, 'verification/builder-interactions', route.proof.model))) {
      problems.push(route.id + ': model file does not exist: ' + route.proof.model);
    }
    if (!route.proof.traceEvent) problems.push(route.id + ': modeled-and-traced route needs a trace event');
    else if (!eventNames.has(route.proof.traceEvent)) {
      problems.push(route.id + ': trace event is not in the trace contract: ' + route.proof.traceEvent);
    }
    if (!route.proof.schedule) problems.push(route.id + ': modeled-and-traced route needs a schedule');
    else if (!(route.proof.schedule in schedules)) {
      problems.push(route.id + ': schedule is not in the trace contract: ' + route.proof.schedule);
    } else {
      const schedule = schedules[route.proof.schedule];
      if (schedule.model && route.proof.model && schedule.model !== route.proof.model.replace(/\.tla$/, '')) {
        problems.push(route.id + ': schedule model does not match proof model: ' + route.proof.schedule);
      }
      if (schedule.test && route.proof.uiTest) {
        const uiTest = readFileSync(resolve(ROOT, route.proof.uiTest), 'utf8');
        if (!uiTest.includes(schedule.test)) problems.push(route.id + ': schedule test is not in the UI test: ' + schedule.test);
      }
    }
    if (!hasTest(route.proof)) problems.push(route.id + ': trace route needs an executable test');
    if (route.proof.status === 'modeled-and-traced') {
      if (!route.proof.browserCase) problems.push(route.id + ': modeled-and-traced route needs a browser case');
      else {
        const browserCase = browserCases.get(route.proof.browserCase);
        if (!browserCase) problems.push(route.id + ': browser case is not recorded: ' + route.proof.browserCase);
        else if (!['pass', 'passed'].includes(browserCase.status)) problems.push(route.id + ': browser case is not passing: ' + route.proof.browserCase);
        else if (!Array.isArray(browserCase.coveredRoutes) || !browserCase.coveredRoutes.includes(route.method + ' ' + route.path)) {
          problems.push(route.id + ': browser case does not cover the route: ' + route.proof.browserCase);
        }
      }
    }
    if (route.proof.goTest && !formalScript.includes(route.proof.goTest)) {
      problems.push(route.id + ': Go test is not in the formal runner: ' + route.proof.goTest);
    }
    if (route.proof.uiTest && !existsSync(resolve(ROOT, route.proof.uiTest))) {
      problems.push(route.id + ': UI test does not exist: ' + route.proof.uiTest);
    }
  }
  if (route.proof.status === 'modeled-only' && (!Array.isArray(route.proof.modelActions) || route.proof.modelActions.length === 0)) {
    problems.push(route.id + ': modeled-only route needs model actions');
  }
  if (route.proof.status === 'contract-tested' && !hasTest(route.proof)) {
    problems.push(route.id + ': contract-tested route needs a contract, Go, or UI test');
  }
  if (route.proof.status === 'unproven' && Array.isArray(route.proof.modelActions) && route.proof.modelActions.length > 0) {
    problems.push(route.id + ': unproven route has model actions; use modeled-only');
  }
  if (route.proof.status === 'excluded' && !route.proof.reason) problems.push(route.id + ': excluded proof needs a reason');
}

for (const route of payload.excludedAuthoringRoutes ?? []) {
  if (!route.method || !route.path) problems.push('excluded route needs method and path');
  if (route.usedByBuilder !== false) problems.push((route.method ?? 'UNKNOWN') + ' ' + (route.path ?? 'UNKNOWN') + ': excluded route must have usedByBuilder=false');
  if (route.proof?.status !== 'excluded') problems.push((route.method ?? 'UNKNOWN') + ' ' + (route.path ?? 'UNKNOWN') + ': excluded route needs proof.status=excluded');
  if (!route.reason) problems.push((route.method ?? 'UNKNOWN') + ' ' + (route.path ?? 'UNKNOWN') + ': excluded route needs a reason');
}

const scope = payload.scope;
if (!scope || typeof scope !== 'object') {
  problems.push('route ledger scope is required');
} else {
  if (!Number.isInteger(scope.generatedAuthoringRoutes)) problems.push('scope.generatedAuthoringRoutes must be an integer');
  if (!Number.isInteger(scope.builderRoutes)) problems.push('scope.builderRoutes must be an integer');
  if (!Number.isInteger(scope.ledgerAuthoringRoutes)) problems.push('scope.ledgerAuthoringRoutes must be an integer');
  if (!Number.isInteger(scope.excludedAuthoringRoutes)) problems.push('scope.excludedAuthoringRoutes must be an integer');
  if (!Array.isArray(scope.unaccountedAuthoringRoutes)) problems.push('scope.unaccountedAuthoringRoutes must be an array');
  if (Array.isArray(scope.unaccountedAuthoringRoutes) && scope.unaccountedAuthoringRoutes.length > 0) {
    problems.push('unaccounted generated authoring routes: ' + scope.unaccountedAuthoringRoutes.join(', '));
  }
  if (
    Number.isInteger(scope.generatedAuthoringRoutes) &&
    Number.isInteger(scope.ledgerAuthoringRoutes) &&
    Number.isInteger(scope.excludedAuthoringRoutes) &&
    Array.isArray(scope.unaccountedAuthoringRoutes) &&
    scope.ledgerAuthoringRoutes + scope.excludedAuthoringRoutes + scope.unaccountedAuthoringRoutes.length !== scope.generatedAuthoringRoutes
  ) {
    problems.push('generated authoring route accounting does not add up');
  }
}

const proofStatus = (route) => route.proof?.status ?? 'missing';
const counts = {
  total: selectedRoutes.length,
  builder: selectedRoutes.filter((route) => route.usedByBuilder === true).length,
  tier1: selectedRoutes.filter((route) => route.tier === 1).length,
  tier2: selectedRoutes.filter((route) => route.tier === 2).length,
  tier3: selectedRoutes.filter((route) => route.tier === 3).length,
  modeledAndTraced: selectedRoutes.filter((route) => proofStatus(route) === 'modeled-and-traced').length,
  traceTested: selectedRoutes.filter((route) => proofStatus(route) === 'trace-tested').length,
  modeledOnly: selectedRoutes.filter((route) => proofStatus(route) === 'modeled-only').length,
  contractTested: selectedRoutes.filter((route) => proofStatus(route) === 'contract-tested').length,
  unproven: selectedRoutes.filter((route) => proofStatus(route) === 'unproven').length,
  excluded: routeFilter ? 0 : (payload.excludedAuthoringRoutes ?? []).filter((route) => proofStatus(route) === 'excluded').length,
  withBuilderEvidence: selectedRoutes.filter((route) => Array.isArray(route.builderEvidence) && route.builderEvidence.length > 0).length,
  withModelActions: selectedRoutes.filter((route) => Array.isArray(route.proof?.modelActions) && route.proof.modelActions.length > 0).length,
  withTrace: selectedRoutes.filter((route) => Boolean(route.proof?.traceEvent)).length,
  withTest: selectedRoutes.filter((route) => hasTest(route.proof)).length,
  withBrowserCase: selectedRoutes.filter((route) => Boolean(route.proof?.browserCase)).length,
};

if (!routeFilter) {
  if (Number.isInteger(scope?.builderRoutes) && scope.builderRoutes !== counts.builder) {
    problems.push('scope.builderRoutes does not match the route list');
  }
  if (Number.isInteger(scope?.excludedAuthoringRoutes) && scope.excludedAuthoringRoutes !== (payload.excludedAuthoringRoutes ?? []).length) {
    problems.push('scope.excludedAuthoringRoutes does not match the excluded route list');
  }
  if (Number.isInteger(scope?.modeledAndTraced) && scope.modeledAndTraced !== counts.modeledAndTraced) {
    problems.push('scope.modeledAndTraced does not match the route proof statuses');
  }
  if (Number.isInteger(scope?.traceTested) && scope.traceTested !== counts.traceTested) {
    problems.push('scope.traceTested does not match the route proof statuses');
  }
  if (Number.isInteger(scope?.modeledOnly) && scope.modeledOnly !== counts.modeledOnly) {
    problems.push('scope.modeledOnly does not match the route proof statuses');
  }
  if (Number.isInteger(scope?.contractTested) && scope.contractTested !== counts.contractTested) {
    problems.push('scope.contractTested does not match the route proof statuses');
  }
  if (Number.isInteger(scope?.unproven) && scope.unproven !== counts.unproven) {
    problems.push('scope.unproven does not match the route proof statuses');
  }
}

console.log(JSON.stringify(counts, null, 2));
for (const problem of problems) console.error('PROBLEM ' + problem);
if (problems.length > 0) fail('route ledger has ' + problems.length + ' structural problems');
if (strict && counts.traceTested > 0) {
  fail(counts.traceTested + ' routes are trace-tested but not browser-proven; add browser evidence');
}
if (strict && counts.unproven + counts.modeledOnly > 0) {
  fail(counts.unproven + ' routes are unproven and ' + counts.modeledOnly + ' routes are modeled-only; assign route-level proof or exclude them');
}
console.log(reportOnly ? 'PASS route ledger structure (proof report only)' : 'PASS route ledger structure and proof');
