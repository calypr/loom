import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync, realpathSync } from 'node:fs';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import {
  captureCdaActionEpoch,
  createFreshBasicQuantityFixture,
  environmentSnapshot,
  assertCdaExplicitUIRoute,
  finalizeCdaExplorerMetadata,
  isOwnedCdaScopedRequest,
  shouldCreateFreshBasicQuantityProject,
} from '../cda-fixtures.mjs';
import {
  assertFixtureSourceApiFreeze,
  assertNoUnexpectedFixtureFailures,
} from '../fixture-failure-assertions.mjs';
import { canonicalProjectID } from '../../../loom-dev.mjs';
import { captureCDARequests, createCDARequestView } from '../cda-playwright-requests.mjs';
import { createdExplorerScope } from '../created-explorer-scope.mjs';

test('fresh generated project binds the Explorer response through canonical project identity', () => {
  const requestedProject = 'loom_dev_verify_mv2y5q93-30de6f32';
  const returnedProject = 'loom_dev_verify_mv2y5q93/30de6f32';
  assert.equal(canonicalProjectID(requestedProject), returnedProject);
  assert.notEqual(returnedProject, 'loom_dev_cda_fhir', 'The generated project remains distinct from the shared project');

  const scope = createdExplorerScope(requestedProject, {
    project: returnedProject,
    explorerId: 'related-one-all-quantity',
  });
  assert.deepEqual(scope, {
    project: returnedProject,
    explorerId: 'related-one-all-quantity',
    explorerRoot: '/api/v1/projects/loom_dev_verify_mv2y5q93%2F30de6f32/explorers/related-one-all-quantity',
    authoringBase: '/api/v1/projects/loom_dev_verify_mv2y5q93%2F30de6f32/explorers/related-one-all-quantity/authoring/v2',
    browserExplorerRoot: '/api/v1/projects/loom_dev_verify_mv2y5q93%252F30de6f32/explorers/related-one-all-quantity',
    browserAuthoringBase: '/api/v1/projects/loom_dev_verify_mv2y5q93%252F30de6f32/explorers/related-one-all-quantity/authoring/v2',
  });

  const explicitCanonicalRoute = new URL('http://127.0.0.1:3180/');
  explicitCanonicalRoute.searchParams.set('project', scope.project);
  explicitCanonicalRoute.searchParams.set('explorer', scope.explorerId);
  explicitCanonicalRoute.searchParams.set('mode', 'builder');
  assert.deepEqual(assertCdaExplicitUIRoute(explicitCanonicalRoute.toString(), {
    uiUrl: 'http://127.0.0.1:3180', fixtureProject: requestedProject,
  }), { project: returnedProject, explorer: scope.explorerId, mode: 'builder' },
  'The browser route must use the canonical project from the validated creation response');
  explicitCanonicalRoute.searchParams.set('project', 'loom_dev_verify_mv2y5q94/30de6f32');
  assert.throws(() => assertCdaExplicitUIRoute(explicitCanonicalRoute.toString(), {
    uiUrl: 'http://127.0.0.1:3180', fixtureProject: requestedProject,
  }), /owned fixture project/);

  assert.throws(() => createdExplorerScope(requestedProject, {
    project: 'loom_dev_verify_mv2y5q94/30de6f32',
    explorerId: 'related-one-all-quantity',
  }), /match the requested project/);
});

test('CDA action epoch records the actual wall-clock interval around a trusted Close click', () => {
  const startedAt = 1_791_548_796_304;
  const trustedCloseAt = 1_791_548_796_412;
  let clockSamples = 0;
  const epoch = captureCdaActionEpoch(startedAt, () => {
    clockSamples += 1;
    return 1_791_548_796_447;
  });

  assert.equal(clockSamples, 1, 'the final epoch must sample the controlled clock exactly once');
  assert.deepEqual(epoch, {
    startedAtEpochMs: startedAt,
    finishedAtEpochMs: 1_791_548_796_447,
  });
  assert(epoch.startedAtEpochMs <= trustedCloseAt && trustedCloseAt <= epoch.finishedAtEpochMs,
    'the measured action interval must enclose the trusted native Close click');
});

test('CDA action epoch omits missing or invalid wall-clock bounds', () => {
  assert.equal(captureCdaActionEpoch(undefined, () => 1_791_548_796_447), undefined);
  assert.equal(captureCdaActionEpoch(Number.NaN, () => 1_791_548_796_447), undefined);
  assert.equal(captureCdaActionEpoch(1_791_548_796_500, () => 1_791_548_796_447), undefined);
  assert.equal(captureCdaActionEpoch(1_791_548_796_304, () => Number.NaN), undefined);
});

test('CDA environment snapshot retains the oracle database but omits host credentials', () => {
  const keys = ['LOOM_ARANGO_DATABASE', 'LOOM_ARANGO_USER', 'LOOM_ARANGO_PASSWORD', 'UNRELATED_SECRET'];
  const original = new Map(keys.map(key => [key, process.env[key]]));
  try {
    process.env.LOOM_ARANGO_DATABASE = 'synthetic_oracle_database';
    process.env.LOOM_ARANGO_USER = 'synthetic_user';
    process.env.LOOM_ARANGO_PASSWORD = 'synthetic_password';
    process.env.UNRELATED_SECRET = 'synthetic_unrelated_secret';
    const snapshot = environmentSnapshot({ project: 'synthetic_cda_project', generation: '' });

    assert.equal(snapshot.LOOM_ARANGO_DATABASE, 'synthetic_oracle_database');
    assert.equal(snapshot.LOOM_CDA_PROJECT, 'synthetic_cda_project');
    assert.equal(Object.isFrozen(snapshot), true);
    for (const key of ['LOOM_ARANGO_USER', 'LOOM_ARANGO_PASSWORD', 'UNRELATED_SECRET', 'LOOM_CDA_GENERATION', 'LOOM_CDA_DATASET_DIR']) {
      assert.equal(Object.hasOwn(snapshot, key), false, `Snapshot must omit ${key}`);
    }

    process.env.LOOM_ARANGO_DATABASE = '';
    assert.equal(Object.hasOwn(environmentSnapshot({}), 'LOOM_ARANGO_DATABASE'), false);
    delete process.env.LOOM_ARANGO_DATABASE;
    assert.equal(Object.hasOwn(environmentSnapshot({}), 'LOOM_ARANGO_DATABASE'), false);
    assert.equal(snapshot.LOOM_ARANGO_DATABASE, 'synthetic_oracle_database');
  } finally {
    for (const [key, value] of original) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
});

test('only the exact Basic Quantity lifecycle gets a fresh literal-fixture project', async () => {
  const scenarioID = 'basic-related-quantity-one-all';
  const caseName = 'basic-height-quantity-one-all-lifecycle';
  assert.equal(shouldCreateFreshBasicQuantityProject({ scenarioID, caseName }), true);
  assert.equal(shouldCreateFreshBasicQuantityProject({ scenarioID, caseName: 'other-case' }), false);
  assert.equal(shouldCreateFreshBasicQuantityProject({ scenarioID: 'cda-related-one-all', caseName }), false);

  const sourceRoot = realpathSync(fileURLToPath(new URL('../../../../', import.meta.url)));
  const fixtureDir = realpathSync(fileURLToPath(new URL('../../../../testdata/devloop-fixture', import.meta.url)));
  const session = {
    sourceRoot,
    fixtureDir,
    fixtureProject: 'loom_dev_shared',
    fixtureGeneration: 'fixture-v1',
  };
  const target = {
    ...session,
    fixtureProject: 'loom_dev_verify_test-run',
  };
  const fresh = await createFreshBasicQuantityFixture(session, 'test-run', {
    createFixture: async (receivedSession, runID) => {
      assert.equal(receivedSession, session);
      assert.equal(runID, 'test-run');
      return { target, seed: { fresh: true, reused: false } };
    },
  });
  assert.equal(fresh.target, target);

  let seeded = false;
  await assert.rejects(() => createFreshBasicQuantityFixture(
    { ...session, fixtureDir: sourceRoot },
    'wrong-fixture',
    { createFixture: async () => { seeded = true; throw new Error('must not seed'); } },
  ), /literal devloop fixture/);
  assert.equal(seeded, false, 'A wrong fixture directory must be rejected before any generation seed');

  await assert.rejects(() => createFreshBasicQuantityFixture(session, 'reused-run', {
    createFixture: async () => ({ target, seed: { fresh: false, reused: true } }),
  }), /must report a fresh generation/);
});

test('CDA report retains the workflow-selected Explorer without inferring it from traffic', () => {
  const report = {
    explorer: 'collection-repair-fresh-113',
    target: { explorer: null },
    nativeRequests: [{ path: '/projects/unrelated/explorers/incidental' }],
  };
  const target = { explorer: 'configured-existing-explorer' };

  assert.equal(finalizeCdaExplorerMetadata(report, target), 'collection-repair-fresh-113');
  assert.equal(report.explorer, 'collection-repair-fresh-113');
  assert.equal(report.target.explorer, 'collection-repair-fresh-113');
});

test('successful fixture finalization ignores omitted workflow bookkeeping but still rejects unexpected failures', () => {
  const freezeAssertions = [
    { name: 'watched source fingerprint baseline captured before browser lifecycle', status: 'passed' },
    { name: 'API build identity baseline captured before browser lifecycle', status: 'passed' },
    { name: 'watched source stayed unchanged during browser run', status: 'passed' },
    { name: 'API build identity stayed unchanged during browser run', status: 'passed' },
  ];
  const report = {
    assertions: freezeAssertions,
    errors: [],
    missingRequiredChecks: ['local source-fields oracle outcome'],
  };
  assert.doesNotThrow(() => assertNoUnexpectedFixtureFailures({
    observerFailures: [],
    reportErrors: report.errors,
    fixtureErrors: [],
  }));
  assert.doesNotThrow(() => assertFixtureSourceApiFreeze(report.assertions));
  const reportWithoutBookkeeping = { errors: [] };
  assert.equal(Object.hasOwn(reportWithoutBookkeeping, 'missingRequiredChecks'), false);
  assert.doesNotThrow(() => assertNoUnexpectedFixtureFailures({
    observerFailures: [],
    reportErrors: reportWithoutBookkeeping.errors,
    fixtureErrors: [],
  }));
  assert.throws(() => assertNoUnexpectedFixtureFailures({
    observerFailures: [],
    reportErrors: [{ kind: 'http', status: 500 }],
    fixtureErrors: [],
  }), /Unexpected HTTP, transport, runtime, or application errors/);
  assert.throws(() => assertNoUnexpectedFixtureFailures({
    observerFailures: [{ kind: 'network' }],
    reportErrors: [],
    fixtureErrors: [],
  }), /Unexpected browser transport, runtime, or console failures/);
  assert.throws(() => assertNoUnexpectedFixtureFailures({
    observerFailures: [],
    reportErrors: [],
    fixtureErrors: [{ kind: 'fixture-audit-error' }],
  }), /fixture lifecycle reported unexpected network\/runtime errors/);
  for (const failedCheck of [
    'watched source stayed unchanged during browser run',
    'API build identity stayed unchanged during browser run',
  ]) {
    const changedFreeze = freezeAssertions.map(assertion => assertion.name === failedCheck
      ? { ...assertion, status: 'failed' }
      : assertion);
    const changedFreezeReport = { assertions: changedFreeze };
    assert.equal(Object.hasOwn(changedFreezeReport, 'missingRequiredChecks'), false);
    assert.throws(() => assertFixtureSourceApiFreeze(changedFreezeReport.assertions), /Loom source\/API freeze checks must pass/);
  }
});

test('Basic and CDA fixtures retain failure assertions without gating on missing workflow checks', () => {
  const source = readFileSync(new URL('../cda-fixtures.mjs', import.meta.url), 'utf8');
  const basicSource = readFileSync(new URL('../fixtures.mjs', import.meta.url), 'utf8');
  assert.match(source, /createPlaywrightNetworkObserver\(page/);
  assert.match(source, /assertNoUnexpectedFixtureFailures\(\{\s*observerFailures:\s*observer\.failures,\s*reportErrors:\s*report\.errors,\s*fixtureErrors:\s*\[\],\s*\}\)/);
  assert.doesNotMatch(source, /expect\(report\.missingRequiredChecks/);
  assert.match(basicSource, /assertNoUnexpectedFixtureFailures\(\{\s*observerFailures,\s*reportErrors:\s*\[\],\s*fixtureErrors,\s*\}\)/);
  assert.match(basicSource, /await testInfo\.attach\('loom-verification-report\.json'[\s\S]*?assertFixtureSourceApiFreeze\(report\.assertions\)/);
  assert.doesNotMatch(basicSource, /expect\(report\.missingRequiredChecks/);
  assert.doesNotMatch(source, /runAfterNativeRequestDrain|flushNativeAbortProbeEvents|gateFailure|waitForCapturedResponse|withExpectedCancellations/);
});

test('CDA observer accepts canonical and legacy project aliases but keeps origin and Explorer exact', () => {
  const target = { fixtureProject: 'loom_dev_scope001-auth', explorer: 'auth-explorer-01' };
  const origins = new Set(['http://127.0.0.1:30088', 'http://127.0.0.1:8188']);
  const canonicalDoubleEncoded = 'http://127.0.0.1:30088/api/v1/projects/loom_dev_scope001%252Fauth/explorers/auth-explorer-01/authoring/v2/builder';
  const legacyProject = 'http://127.0.0.1:8188/api/v1/projects/loom_dev_scope001-auth/explorers/auth-explorer-01/authoring/v2/builder';
  assert.equal(isOwnedCdaScopedRequest(canonicalDoubleEncoded, target, origins), true);
  assert.equal(isOwnedCdaScopedRequest(legacyProject, target, origins), true);
  assert.equal(isOwnedCdaScopedRequest(
    'http://127.0.0.1:8188/api/v1/projects/loom_dev_scope001%252Fblocked/explorers/auth-explorer-01/authoring/v2/builder',
    target, origins,
  ), false);
  assert.equal(isOwnedCdaScopedRequest(
    'http://127.0.0.1:8188/api/v1/projects/loom_dev_scope001%252Fauth/explorers/foreign-explorer/authoring/v2/builder',
    target, origins,
  ), false);
  assert.equal(isOwnedCdaScopedRequest(
    'http://127.0.0.1:8188/api/v1/projects/loom_dev_scope001%25zz/explorers/auth-explorer-01/authoring/v2/builder',
    target, origins,
  ), false);
  assert.equal(isOwnedCdaScopedRequest(
    'http://127.0.0.1:30089/api/v1/projects/loom_dev_scope001-auth/explorers/auth-explorer-01/authoring/v2/builder',
    target, origins,
  ), false);
});

test('native and fixture request views match the double-encoded UI route without widening project or Explorer scope', async () => {
  const requestedProject = 'loom_dev_verify_mv2y5q93-30de6f32';
  const returnedProject = 'loom_dev_verify_mv2y5q93/30de6f32';
  const explorerId = 'related-one-all-quantity';
  const scope = createdExplorerScope(requestedProject, { project: returnedProject, explorerId });
  const origin = 'http://127.0.0.1:30088';
  const exactPath = `${scope.browserAuthoringBase}/preview`;
  const exactURL = `${origin}${exactPath}`;
  const foreignProjectURL = exactURL.replace('loom_dev_verify_mv2y5q93%252F30de6f32', 'loom_dev_verify_mv2y5q94%252F30de6f32');
  const foreignExplorerURL = exactURL.replace(`/${explorerId}/authoring/`, `/${explorerId}-other/authoring/`);
  const foreignOriginURL = exactURL.replace(origin, 'http://127.0.0.1:30089');

  const page = new EventEmitter();
  page.url = () => `${origin}/`;
  const nativeReport = { nativeRequests: [], network: [], errors: [], assetFailures: [] };
  const nativeView = captureCDARequests(page, {
    apiOrigin: origin,
    browserRequestOrigin: origin,
    appOrigins: [origin],
    ownedPathPrefix: scope.browserAuthoringBase,
    report: nativeReport,
    responsePaths: /\/preview$/,
  });
  const request = (url, body = { outputId: 'output-owned' }) => ({
    url: () => url,
    method: () => 'POST',
    resourceType: () => 'fetch',
    postData: () => JSON.stringify(body),
    headers: () => ({}),
    failure: () => null,
  });
  const exactRequest = request(exactURL);
  for (const url of [exactURL, foreignProjectURL, foreignExplorerURL, foreignOriginURL]) {
    page.emit('request', url === exactURL ? exactRequest : request(url));
  }
  const responseWait = nativeView.waitForResponse(entry => entry.path.endsWith('/preview') &&
    entry.request?.outputId === 'output-owned' && entry.status === 200, { timeoutMs: 1000 });
  page.emit('response', {
    request: () => exactRequest,
    status: () => 200,
    headers: () => ({ 'content-type': 'application/json' }),
    body: async () => Buffer.from(JSON.stringify({ outputId: 'output-owned', rowCount: 1 })),
  });
  const nativeResponse = await responseWait;
  assert.equal(nativeResponse.path, exactPath);
  assert.equal(nativeResponse.response.rowCount, 1);
  assert.deepEqual(nativeView.requests, [nativeResponse], 'the live native capture accepts only the exact browser route');
  nativeView.stop();

  const fixtureEntries = [
    { index: 0, origin, path: exactPath, status: 200, request: { outputId: 'output-owned' }, response: { rowCount: 1 } },
    { index: 1, origin, path: new URL(foreignProjectURL).pathname, status: 200 },
    { index: 2, origin, path: new URL(foreignExplorerURL).pathname, status: 200 },
    { index: 3, origin: new URL(foreignOriginURL).origin, path: exactPath, status: 200 },
  ];
  const fixtureObserver = {
    requests: fixtureEntries,
    byRequest: new WeakMap(),
    requestBody: async entry => entry.request,
    responseBody: async entry => entry.response,
    markExpectedValidation() {},
    markExpectedTransportFailure() {},
    waitForResponse(predicate, { fromIndex = 0 } = {}) {
      const match = this.requests.slice(fromIndex).find(predicate);
      return match ? Promise.resolve(match) : Promise.reject(new Error('No exact owned fixture response'));
    },
  };
  const officialView = createCDARequestView(fixtureObserver, {
    browserRequestOrigin: origin,
    ownedPathPrefix: scope.browserExplorerRoot,
  });
  assert.deepEqual(officialView.requests, [fixtureEntries[0]], 'the official fixture view uses the exact canonical Explorer prefix');
  assert.equal(await officialView.waitForResponse(entry => entry.path.endsWith('/preview') && entry.status === 200), fixtureEntries[0]);
});
