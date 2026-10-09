import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import test from 'node:test';
import { createReport, finishReport } from '../report.mjs';
import { builderAuthoringWorkflow, setAuthoringNativeRequestScope } from '../playwright-authoring.mjs';
import { installNativeAbortProbe } from '../native-abort-probe.mjs';

const retainedRequest136 = JSON.parse(readFileSync(
  new URL('./fixtures/builder-authoring-request-136-retained.json', import.meta.url), 'utf8',
));
const authoringNetworkCheck = 'no unexpected network, API, or browser errors';

const request136Report = () => {
  const report = createReport({
    scenario: 'builder-authoring',
    caseName: 'authoring',
    target: structuredClone(retainedRequest136.target),
    requiredChecks: [authoringNetworkCheck],
  });
  report.network = structuredClone(retainedRequest136.network);
  report.actions = structuredClone(retainedRequest136.actions);
  report.nativeAbortProbeEvents = structuredClone(retainedRequest136.nativeAbortProbeEvents);
  report.nativeRequestTerminalLedger = structuredClone(retainedRequest136.nativeRequestTerminalLedger);
  report.navigationTimings = structuredClone(retainedRequest136.navigationTimings);
  report.finalNetworkCheckName = authoringNetworkCheck;
  return {
    report,
    target: {
      fixtureProject: retainedRequest136.target.project,
      uiUrl: retainedRequest136.target.uiUrl,
    },
  };
};

test('Builder authoring caller installs its exact fixture scope before the first navigation', async () => {
  const calls = [];
  const browserContext = {
    async exposeBinding(name, callback) {
      calls.push({ kind: 'binding', name, callback });
    },
    async addInitScript(script) {
      calls.push({ kind: 'init-script', script });
    },
  };
  const sentinel = new Error('stop at the first navigation');
  const page = {
    setDefaultTimeout(timeout) { calls.push({ kind: 'default-timeout', timeout }); },
    setDefaultNavigationTimeout(timeout) { calls.push({ kind: 'navigation-timeout', timeout }); },
    context: () => browserContext,
    async evaluate(script) {
      calls.push({ kind: 'current-document', script });
    },
    async goto(url, options) {
      calls.push({ kind: 'goto', url, options });
      throw sentinel;
    },
  };
  const project = 'loom_dev_verify_authoring_caller';
  const origin = 'http://127.0.0.1:30008';
  const target = {
    uiUrl: origin,
    fixtureProject: project,
    fixtureGeneration: 'fixture-v1',
    fixtureDir: resolve(dirname(fileURLToPath(import.meta.url)), '../../../../testdata/verify-combine'),
    bootstrapExplorerId: 'loom-dev-bootstrap',
  };
  const report = { target: {}, requiredChecks: [], timings: {}, network: [], errors: [], assertions: [] };

  await assert.rejects(builderAuthoringWorkflow({
    page,
    report,
    action: async () => { throw new Error('no user action should run before first navigation'); },
    check: () => { throw new Error('no check should run before first navigation'); },
  }, { target, runID: 'caller-regression-0123456789' }), error => error === sentinel);

  assert.equal(report.finalNetworkCheckName, authoringNetworkCheck,
    'network acceptance is evaluated only after fixture-native ledger finalization');

  const setup = calls.filter(({ kind }) => ['binding', 'init-script', 'current-document'].includes(kind));
  const navigation = calls.find(({ kind }) => kind === 'goto');
  assert.deepEqual(setup.map(({ kind }) => kind), ['binding', 'init-script', 'current-document']);
  assert(navigation, 'the real caller must reach its first page navigation');
  assert(calls.indexOf(navigation) > calls.indexOf(setup.at(-1)),
    'the actual goto must happen after binding, future-document script, and current-document install');
  assert.equal(setup[0].name, '__loomNativeAbortProbeBinding');
  assert(setup[1].script.includes(`project: "${project}"`));
  assert(setup[1].script.includes('explorerScope: "project-routes"'));
  assert(setup[1].script.includes(`apiOrigin: "${origin}"`));
  assert.doesNotMatch(setup[1].script, /bootstrapExplorerId/,
    'the probe must capture the later selected Explorer rather than claim the bootstrap Explorer');
  const navigationURL = new URL(navigation.url);
  assert.equal(navigationURL.origin, origin);
  assert.equal(navigationURL.searchParams.get('project'), project);
  assert.equal(navigationURL.searchParams.get('explorer'), target.bootstrapExplorerId);
  assert.equal(report.target.fixtureOracle.project, project);
  assert.equal(report.nativeAbortProbeEvents.length, 0);
});

test('Builder authoring caller binds native request scope after selecting its fresh Explorer', async () => {
  const calls = [];
  const stopAfterScope = new Error('scope captured');
  const locator = name => ({
    async waitFor() {},
    async click() {},
    async fill() {},
    async inputValue() { return 'verify-fresh-authoring'; },
    toString() { return name; },
  });
  const browserContext = {
    async exposeBinding(name) { calls.push({ kind: 'binding', name }); },
    async addInitScript() { calls.push({ kind: 'init-script' }); },
  };
  const project = 'loom_dev_verify_authoring_scope';
  const origin = 'http://127.0.0.1:30008';
  const target = {
    uiUrl: origin,
    fixtureProject: project,
    fixtureGeneration: 'fixture-v1',
    fixtureDir: resolve(dirname(fileURLToPath(import.meta.url)), '../../../../testdata/verify-combine'),
    bootstrapExplorerId: 'loom-dev-bootstrap',
  };
  const report = {
    target: { project, uiUrl: origin },
    requiredChecks: [],
    timings: {},
    network: [],
    errors: [],
    assertions: [],
  };
  const page = {
    setDefaultTimeout() {},
    setDefaultNavigationTimeout() {},
    context: () => browserContext,
    async evaluate() { calls.push({ kind: 'current-document' }); },
    async goto() { calls.push({ kind: 'goto' }); },
    getByText: locator,
    getByRole: locator,
    getByTestId: locator,
    async waitForFunction() {},
    locator(selector) {
      if (selector === '#first-table-name') throw stopAfterScope;
      return locator(selector);
    },
  };

  await assert.rejects(builderAuthoringWorkflow({
    page,
    report,
    action: async (_name, _target, perform) => perform(),
    check: () => { throw new Error('the workflow should stop before the first root-table request'); },
  }, { target, runID: 'authoring-scope-regression-0123456789' }), error => error === stopAfterScope);

  assert.equal(report.target.explorer, 'verify-fresh-authoring');
  assert.deepEqual(report.nativeRequestCaptureScope, {
    project,
    origin,
    explorer: 'verify-fresh-authoring',
  });
  assert.deepEqual(calls.filter(({ kind }) => kind === 'goto' || kind === 'binding' || kind === 'init-script' || kind === 'current-document')
    .map(({ kind }) => kind), ['binding', 'init-script', 'current-document', 'goto']);
});

test('CASE-007 final network check accepts the retained exact Close abort after binding selected Explorer scope', () => {
  const { report, target } = request136Report();
  assert.equal(report.nativeRequestCaptureScope, undefined,
    'the retained report preimage omitted the scope needed by final classification');
  assert.equal(report.network[0].requestTimeline.action, null,
    'the network diagnostic has no action association; Close ownership is proven by probe evidence');
  assert.deepEqual(report.nativeRequestTerminalLedger.requests[0].sameFrameNavigationEventsAfterStart.map(event => event.phase),
    ['request-start', 'frame-navigated']);
  assert.deepEqual(report.navigationTimings.map(event => event.atMs), [4792, 4799],
    'the retained same-frame navigation occurs after native failure at 4552 ms');

  setAuthoringNativeRequestScope(report, target, retainedRequest136.target.explorer);
  const originalNetwork = structuredClone(report.network);
  const originalLedger = structuredClone(report.nativeRequestTerminalLedger);
  const originalProbeEvents = structuredClone(report.nativeAbortProbeEvents);
  assert.equal(report.assertions.some(assertion => assertion.name === authoringNetworkCheck), false,
    'the requested check is not marked passed before final native evidence is classified');

  finishReport(report);

  assert.equal(report.status, 'passed', JSON.stringify(report.errors));
  assert.deepEqual(report.missingRequiredChecks, []);
  assert.equal(report.expectedOwnerRetirements.length, 1);
  assert.equal(report.expectedOwnerRetirements[0].requestId, 'schema-fields-08ecbd51-a999-4b2c-b1d1-1fbc536f9f91');
  assert.equal(report.expectedOwnerRetirements[0].browserRequestId, 'request-136');
  assert.equal(report.assertions.find(assertion => assertion.name === authoringNetworkCheck).status, 'passed');
  assert.deepEqual(report.network, originalNetwork, 'classification preserves the retained raw network error');
  assert.deepEqual(report.nativeRequestTerminalLedger, originalLedger, 'classification preserves raw native terminal evidence');
  assert.deepEqual(report.nativeAbortProbeEvents, originalProbeEvents, 'classification preserves raw probe evidence');
});

test('CASE-007 final network check rejects missing scope or mismatched signal identity', () => {
  const missingScope = request136Report().report;
  finishReport(missingScope);
  assert.equal(missingScope.status, 'failed');
  assert.equal(missingScope.expectedOwnerRetirements?.length ?? 0, 0);
  assert.equal(missingScope.assertions.find(assertion => assertion.name === authoringNetworkCheck).status, 'failed');

  const { report, target } = request136Report();
  setAuthoringNativeRequestScope(report, target, retainedRequest136.target.explorer);
  const abortEvent = report.nativeAbortProbeEvents.find(event => event.kind === 'abort-controller-call');
  abortEvent.requests.find(request => request.requestId === 'schema-fields-08ecbd51-a999-4b2c-b1d1-1fbc536f9f91')
    .requestId = 'schema-fields-wrong-request';
  finishReport(report);
  assert.equal(report.status, 'failed');
  assert.equal(report.expectedOwnerRetirements?.length ?? 0, 0);
  assert.equal(report.assertions.find(assertion => assertion.name === authoringNetworkCheck).status, 'failed');
});

test('CASE-007 scope binding rejects a different project, Explorer, or UI origin', () => {
  const selected = request136Report();
  assert.throws(() => setAuthoringNativeRequestScope(
    selected.report, selected.target, 'verify-other-explorer',
  ), /exact fixture project, UI origin, and selected Explorer/);

  const wrongOrigin = request136Report();
  assert.throws(() => setAuthoringNativeRequestScope(wrongOrigin.report, {
    ...wrongOrigin.target,
    uiUrl: 'http://127.0.0.1:9999',
  }, retainedRequest136.target.explorer), /exact fixture project, UI origin, and selected Explorer/);

  const wrongProject = request136Report();
  assert.throws(() => setAuthoringNativeRequestScope(wrongProject.report, {
    ...wrongProject.target,
    fixtureProject: 'loom_dev_verify_other',
  }, retainedRequest136.target.explorer), /exact fixture project, UI origin, and selected Explorer/);
});

test('CASE-007 final network check rejects in-interval navigation, pending terminals, and unrelated HTTP errors', () => {
  const inIntervalNavigation = request136Report();
  setAuthoringNativeRequestScope(inIntervalNavigation.report, inIntervalNavigation.target,
    retainedRequest136.target.explorer);
  const navigation = [{ id: 'navigation-inside-abort-window', atMs: 4500 }];
  inIntervalNavigation.report.network[0].requestTimeline.mainFrameNavigations = structuredClone(navigation);
  inIntervalNavigation.report.nativeRequestTerminalLedger.requests[0].requestTimeline.mainFrameNavigations =
    structuredClone(navigation);
  finishReport(inIntervalNavigation.report);
  assert.equal(inIntervalNavigation.report.status, 'failed', 'navigation before the native terminal remains fatal');
  assert.equal(inIntervalNavigation.report.expectedOwnerRetirements?.length ?? 0, 0);

  const pending = request136Report();
  setAuthoringNativeRequestScope(pending.report, pending.target, retainedRequest136.target.explorer);
  const entry = pending.report.nativeRequestTerminalLedger.requests[0];
  entry.nativeEventChronology = entry.nativeEventChronology.slice(0, 1);
  entry.failure = null;
  entry.terminalEvent = null;
  entry.state = 'pending';
  entry.complete = false;
  finishReport(pending.report);
  assert.equal(pending.report.status, 'failed', 'an unresolved native terminal stays fatal');
  assert.equal(pending.report.expectedOwnerRetirements?.length ?? 0, 0);

  const unrelatedHttp = request136Report();
  setAuthoringNativeRequestScope(unrelatedHttp.report, unrelatedHttp.target, retainedRequest136.target.explorer);
  unrelatedHttp.report.network.push({
    kind: 'network', method: 'GET', url: `${retainedRequest136.target.uiUrl}/api/v1/metadata`,
    resourceType: 'fetch', status: 503, playwrightRequestId: 'request-http-503',
  });
  finishReport(unrelatedHttp.report);
  assert.equal(unrelatedHttp.report.status, 'failed', 'unrelated HTTP failures remain fatal beside the exact owner abort');
  assert.equal(unrelatedHttp.report.expectedOwnerRetirements.length, 1);
  assert.equal(unrelatedHttp.report.assertions.find(assertion => assertion.name === authoringNetworkCheck).status, 'failed');
});

test('shared installer attaches to current and future documents in exact-scope order', async () => {
  const calls = [];
  const browserContext = {
    async exposeBinding(name, callback) {
      calls.push({ kind: 'binding', name, callback });
    },
    async addInitScript(script) {
      calls.push({ kind: 'init-script', script });
    },
  };
  const page = {
    context: () => browserContext,
    async evaluate(script) {
      calls.push({ kind: 'current-document', script });
    },
  };
  const report = {};
  const project = 'loom_dev_verify_installer';
  const origin = 'http://127.0.0.1:30008';

  await installNativeAbortProbe({ page, report, project, explorer: { mode: 'project-routes' }, apiOrigin: origin });

  assert.deepEqual(calls.map(({ kind }) => kind), ['binding', 'init-script', 'current-document']);
  assert.equal(calls[0].name, '__loomNativeAbortProbeBinding');
  assert(calls[1].script.includes(`project: "${project}"`));
  assert(calls[1].script.includes('explorerScope: "project-routes"'));
  assert(calls[1].script.includes(`apiOrigin: "${origin}"`));
  assert.doesNotMatch(calls[1].script, /bootstrapExplorerId/);
  assert.deepEqual(report.nativeAbortProbeEvents, []);
});
