import { test as base, expect } from '@playwright/test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { realpathSync } from 'node:fs';
import { performance } from 'node:perf_hooks';
import { mkdir, writeFile } from 'node:fs/promises';
import { basename, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startVerificationIdentity } from './cda-verification-identity.mjs';
import { assertOwnedCdaTarget } from './owned-cda-target.mjs';
import {
  assertOwnedDevSession,
  canonicalProjectID,
  createDevSession,
  createFreshVerificationFixture,
} from '../../loom-dev.mjs';
import {
  createCdaInspector,
  click,
  fill,
  navigate,
  press,
  scrollIntoView,
  selectOption,
  waitForBrowser,
} from './cda-playwright.mjs';
import { ownedFaultTarget, matchesOwnedFaultRequest } from './network-evidence.mjs';
import { registry, scenarioCaseFor } from '../registry.mjs';
import { createReport, finishReport, recordCheck, writeReport } from './report.mjs';
import { sanitizePayload, sanitizeText } from './playwright-browser.mjs';
import { captureNativeFailureEvidence, MAX_NATIVE_FAILURE_CAPTURE_MS } from './native-failure-evidence.mjs';
import { createPlaywrightNetworkObserver } from './playwright-network-observer.mjs';
import { createCDARequestView } from './cda-playwright-requests.mjs';
import { assertNoUnexpectedFixtureFailures } from './fixture-failure-assertions.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const MAX_DIAGNOSTICS = 100;
const sourceRoot = resolve(fileURLToPath(new URL('../../..', import.meta.url)));
const safeText = value => sanitizeText(value).slice(0, 4_000);

export const captureCdaActionEpoch = (startedAt, clock = Date.now) => {
  const finishedAt = clock();
  if (!Number.isFinite(startedAt) || !Number.isFinite(finishedAt) || finishedAt < startedAt) return undefined;
  return { startedAtEpochMs: startedAt, finishedAtEpochMs: finishedAt };
};

function safeURL(raw) {
  try {
    const url = new URL(raw);
    return safeText(`${url.origin}${url.pathname}`);
  } catch {
    return safeText(raw);
  }
}

export function environmentSnapshot(overrides) {
  const env = {
    LOOM_CDA_PROJECT: overrides.project,
    LOOM_CDA_API_ORIGIN: overrides.apiOrigin,
    LOOM_CDA_UI_ORIGIN: overrides.uiOrigin,
    LOOM_CDA_COMPOSE_PROJECT: overrides.composeProject,
    LOOM_CDA_API_CONTAINER: overrides.apiContainer,
    LOOM_CDA_ARANGO_CONTAINER: overrides.arangoContainer,
    LOOM_ARANGO_DATABASE: process.env.LOOM_ARANGO_DATABASE,
    LOOM_CDA_CLICKHOUSE_CONTAINER: overrides.clickhouseContainer,
    LOOM_CDA_GENERATION: overrides.generation,
    LOOM_CDA_DATASET_DIR: overrides.datasetDir,
    LOOM_CDA_SOURCE_ROOT: overrides.sourceRoot,
    LOOM_CDA_API_PORT: overrides.apiPort,
    LOOM_CDA_UI_PORT: overrides.uiPort,
    LOOM_CDA_EXPLORER_ID: overrides.explorer,
    LOOM_CDA_NO_AUTH: process.env.LOOM_CDA_NO_AUTH,
    LOOM_DEV_PROJECT: process.env.LOOM_DEV_PROJECT,
    LOOM_QA_EXPLORER: process.env.LOOM_QA_EXPLORER,
  };
  return Object.freeze(Object.fromEntries(Object.entries(env).filter(([, value]) => value !== undefined && value !== '')));
}

function makeReport({ scenarioID, caseName, target, evidenceDirectory }) {
  const scenario = registry.find(entry => entry.id === scenarioID);
  return createReport({
    scenario: scenarioID,
    caseName,
    target: {
      kind: 'owned-cda',
      project: target.project,
      generation: target.fixtureGeneration ?? null,
      explorer: target.explorer ?? null,
      apiUrl: target.apiUrl,
      uiUrl: target.uiUrl,
      composeProject: target.composeProject,
      apiContainer: target.apiContainer,
      arangoContainer: target.arangoContainer ?? null,
      clickhouseContainer: target.clickhouseContainer ?? null,
      sourceRoot: target.sourceRoot,
    },
    evidenceDirectory,
    requiredChecks: scenario ? scenarioCaseFor(scenario, caseName).requiredChecks : [],
  });
}

const decodeOwnedRouteSegment = raw => {
  let value = raw;
  try {
    for (let pass = 0; pass < 2 && value.includes('%'); pass += 1) {
      const decoded = decodeURIComponent(value);
      if (decoded === value) break;
      value = decoded;
    }
    return value;
  } catch {
    return null;
  }
};

export function isOwnedCdaScopedRequest(requestURL, target, ownedOrigins) {
  try {
    const url = new URL(requestURL);
    if (!ownedOrigins.has(url.origin)) return false;
    const route = url.pathname.match(/\/projects\/([^/]+)\/explorers\/([^/]+)/);
    if (!route) return true;
    const project = decodeOwnedRouteSegment(route[1]);
    const explorer = decodeOwnedRouteSegment(route[2]);
    return project !== null && explorer !== null
      && canonicalProjectID(project) === canonicalProjectID(target.fixtureProject)
      && (!target.explorer || explorer === target.explorer);
  } catch {
    return false;
  }
}

export const finalizeCdaExplorerMetadata = (report, target) => {
  const explorer = [report?.explorer, report?.target?.explorer, target?.explorer]
    .find(value => typeof value === 'string' && value.trim()) ?? null;
  report.explorer = explorer;
  if (report.target) report.target.explorer = explorer;
  return explorer;
};

function safeAttachmentPath(testInfo, name) {
  const basenameOnly = basename(String(name || 'cda-attachment.json'));
  const safeName = basenameOnly.replace(/[^a-zA-Z0-9_.-]/g, '_') || 'cda-attachment.json';
  return testInfo.outputPath('attachments', safeName);
}

export const assertCdaExplicitUIRoute = (rawURL, target) => {
  let url;
  try { url = new URL(rawURL); }
  catch { throw new Error('CDA explicit UI route must be an absolute URL'); }
  const expectedOrigin = new URL(target.uiUrl).origin;
  assert.equal(url.origin, expectedOrigin, 'CDA explicit UI route must use the owned UI origin');
  assert.equal(url.username, '', 'CDA explicit UI route must not include credentials');
  assert.equal(url.password, '', 'CDA explicit UI route must not include credentials');
  assert.equal(url.pathname, '/', 'CDA explicit UI route must use the demo root');
  assert.equal(url.hash, '', 'CDA explicit UI route must not include a fragment');

  const projects = url.searchParams.getAll('project');
  assert.equal(projects.length, 1, 'CDA explicit UI route must contain one project query value');
  assert.equal(canonicalProjectID(projects[0]), canonicalProjectID(target.fixtureProject),
    'CDA explicit UI route must select the owned fixture project');
  const explorers = url.searchParams.getAll('explorer');
  assert.equal(explorers.length, 1, 'CDA explicit UI route must contain one Explorer query value');
  assert.match(explorers[0], /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/, 'CDA explicit UI route must select a valid Explorer identity');
  const modes = url.searchParams.getAll('mode');
  assert.equal(modes.length, 1, 'CDA explicit UI route must contain one mode query value');
  assert.ok(['builder', 'viewer'].includes(modes[0]), 'CDA explicit UI route must select Builder or Viewer mode');
  return { project: projects[0], explorer: explorers[0], mode: modes[0] };
};

const DEFAULT_CDA_UI_ROUTING = 'explicit-query';
const BASIC_QUANTITY_SCENARIO = 'basic-related-quantity-one-all';
const BASIC_QUANTITY_CASE = 'basic-height-quantity-one-all-lifecycle';

export const shouldCreateFreshBasicQuantityProject = ({ scenarioID, caseName } = {}) =>
  scenarioID === BASIC_QUANTITY_SCENARIO && caseName === BASIC_QUANTITY_CASE;

export async function createFreshBasicQuantityFixture(session, runID, {
  createFixture = createFreshVerificationFixture,
} = {}) {
  assert(session?.sourceRoot && session?.fixtureDir && session?.fixtureProject && session?.fixtureGeneration,
    'A configured Basic development session is required before creating its isolated Quantity project');
  assert.equal(realpathSync(resolve(session.sourceRoot)), realpathSync(sourceRoot),
    'The fresh Basic Quantity project must use this verifier source checkout');
  const expectedFixtureDir = realpathSync(resolve(sourceRoot, 'testdata/devloop-fixture'));
  assert.equal(realpathSync(resolve(session.fixtureDir)), expectedFixtureDir,
    'Fresh Basic Quantity projects must be seeded from the literal devloop fixture');

  const fresh = await createFixture(session, runID);
  assert(fresh?.target && fresh?.seed, 'Fresh Basic Quantity fixture creation must return its target and seed receipt');
  assert.equal(fresh.seed.fresh, true, 'Fresh Basic Quantity fixture must report a fresh generation');
  assert.equal(fresh.seed.reused, false, 'Fresh Basic Quantity fixture must not reuse an existing generation');
  assert.equal(realpathSync(resolve(fresh.target.sourceRoot)), realpathSync(sourceRoot),
    'Fresh Basic Quantity project source must remain bound to this verifier checkout');
  assert.equal(realpathSync(resolve(fresh.target.fixtureDir)), expectedFixtureDir,
    'Fresh Basic Quantity project must retain the literal devloop fixture directory');
  assert.equal(fresh.target.fixtureGeneration, session.fixtureGeneration,
    'Fresh Basic Quantity project must retain the owned stack generation');
  assert.notEqual(canonicalProjectID(fresh.target.fixtureProject), canonicalProjectID(session.fixtureProject),
    'Fresh Basic Quantity project must not reuse the configured shared project');
  assert.match(fresh.target.fixtureProject, /^loom_dev_verify_[a-z0-9][a-z0-9-]{0,24}$/,
    'Fresh Basic Quantity project must use the isolated verification namespace');
  return fresh;
}

export const createCdaNavigator = ({ target, uiRouting = DEFAULT_CDA_UI_ROUTING, navigateTo }) => {
  if (!['defaults', 'explicit-query'].includes(uiRouting)) throw new Error(`unsupported CDA UI routing mode: ${uiRouting}`);
  if (typeof navigateTo !== 'function') throw new TypeError('CDA navigator requires a page navigation function');
  return rawURL => {
    if (uiRouting === 'explicit-query' && rawURL !== 'about:blank') assertCdaExplicitUIRoute(rawURL, target);
    return navigateTo(rawURL);
  };
};

export { expect };
export const test = base.extend({
  cdaProject: [process.env.LOOM_CDA_PROJECT, { option: true }],
  cdaApiOrigin: [process.env.LOOM_CDA_API_ORIGIN, { option: true }],
  cdaUiOrigin: [process.env.LOOM_CDA_UI_ORIGIN, { option: true }],
  cdaComposeProject: [process.env.LOOM_CDA_COMPOSE_PROJECT, { option: true }],
  cdaApiContainer: [process.env.LOOM_CDA_API_CONTAINER, { option: true }],
  cdaArangoContainer: [process.env.LOOM_CDA_ARANGO_CONTAINER ?? process.env.LOOM_ARANGO_CONTAINER, { option: true }],
  cdaClickhouseContainer: [process.env.LOOM_CDA_CLICKHOUSE_CONTAINER ?? process.env.LOOM_CLICKHOUSE_CONTAINER, { option: true }],
  cdaGeneration: [process.env.LOOM_CDA_GENERATION, { option: true }],
  cdaDatasetDir: [process.env.LOOM_CDA_DATASET_DIR, { option: true }],
  cdaSourceRoot: [process.env.LOOM_CDA_SOURCE_ROOT, { option: true }],
  cdaApiPort: [process.env.LOOM_CDA_API_PORT, { option: true }],
  cdaUiPort: [process.env.LOOM_CDA_UI_PORT, { option: true }],
  cdaExplorer: [process.env.LOOM_CDA_EXPLORER_ID ?? process.env.LOOM_CDA_EXPLORER ?? process.env.LOOM_QA_EXPLORER, { option: true }],
  cdaScenarioID: ['cda-native', { option: true }],
  cdaCaseName: ['workflow', { option: true }],
  cdaUiRouting: [DEFAULT_CDA_UI_ROUTING, { option: true }],
  cdaRequireClickhouse: [false, { option: true }],
  cdaRequireSourceFixture: [false, { option: true }],
  cdaCreateFreshBasicQuantityProject: [false, { option: true }],

  cda: async ({
    page,
    cdaProject,
    cdaApiOrigin,
    cdaUiOrigin,
    cdaComposeProject,
    cdaApiContainer,
    cdaArangoContainer,
    cdaClickhouseContainer,
    cdaGeneration,
    cdaDatasetDir,
    cdaSourceRoot,
    cdaApiPort,
    cdaUiPort,
    cdaExplorer,
    cdaScenarioID,
    cdaCaseName,
    cdaUiRouting,
    cdaRequireClickhouse,
    cdaRequireSourceFixture,
    cdaCreateFreshBasicQuantityProject,
  }, use, testInfo) => {
    const setupStarted = performance.now();
    let env = environmentSnapshot({
      project: cdaProject,
      apiOrigin: cdaApiOrigin,
      uiOrigin: cdaUiOrigin,
      composeProject: cdaComposeProject,
      apiContainer: cdaApiContainer,
      arangoContainer: cdaArangoContainer,
      clickhouseContainer: cdaClickhouseContainer,
      generation: cdaGeneration,
      datasetDir: cdaDatasetDir,
      sourceRoot: cdaSourceRoot,
      apiPort: cdaApiPort,
      uiPort: cdaUiPort,
      explorer: cdaExplorer,
    });
    assert(env.LOOM_CDA_ARANGO_CONTAINER, 'Set LOOM_CDA_ARANGO_CONTAINER or LOOM_ARANGO_CONTAINER to the isolated CDA source database container.');
    if (cdaRequireClickhouse) {
      assert(env.LOOM_CDA_CLICKHOUSE_CONTAINER, 'Set LOOM_CDA_CLICKHOUSE_CONTAINER or LOOM_CLICKHOUSE_CONTAINER to the isolated source database container.');
    }

    const sourceFixtureEnvironment = [
      'LOOM_CDA_SOURCE_ROOT', 'LOOM_CDA_DATASET_DIR', 'LOOM_CDA_COMPOSE_PROJECT',
      'LOOM_CDA_API_PORT', 'LOOM_CDA_UI_PORT', 'LOOM_CDA_API_ORIGIN', 'LOOM_CDA_UI_ORIGIN',
      'LOOM_CDA_API_CONTAINER', 'LOOM_CDA_PROJECT', 'LOOM_CDA_GENERATION',
    ];
    const fullSourceTargetConfigured = sourceFixtureEnvironment.every(name => String(env[name] ?? '').trim());
    if (cdaRequireSourceFixture || fullSourceTargetConfigured) {
      const missing = sourceFixtureEnvironment.filter(name => !String(env[name] ?? '').trim());
      assert.equal(missing.length, 0, `Set an explicit isolated CDA source fixture: ${missing.join(', ')}`);
    }
    let fixtureDir = null;
    let devTarget = {};
    if (env.LOOM_CDA_SOURCE_ROOT) {
      const configuredSourceRoot = realpathSync(resolve(env.LOOM_CDA_SOURCE_ROOT));
      assert.equal(configuredSourceRoot, sourceRoot, 'LOOM_CDA_SOURCE_ROOT must be this checked-out source tree');
    }
    if (cdaRequireSourceFixture || fullSourceTargetConfigured) {
      const configuredSourceRoot = realpathSync(resolve(env.LOOM_CDA_SOURCE_ROOT));
      fixtureDir = realpathSync(resolve(env.LOOM_CDA_DATASET_DIR));
      devTarget = createDevSession({
        LOOM_DEV_SOURCE_ROOT: configuredSourceRoot,
        LOOM_DEV_FIXTURE_DIR: fixtureDir,
        LOOM_DEV_COMPOSE_PROJECT: env.LOOM_CDA_COMPOSE_PROJECT,
        LOOM_DEV_API_PORT: env.LOOM_CDA_API_PORT,
        LOOM_DEV_UI_PORT: env.LOOM_CDA_UI_PORT,
        LOOM_DEV_API_URL: env.LOOM_CDA_API_ORIGIN,
        LOOM_DEV_UI_URL: env.LOOM_CDA_UI_ORIGIN,
        LOOM_DEV_PROJECT: env.LOOM_DEV_PROJECT ?? env.LOOM_CDA_PROJECT,
        LOOM_DEV_GENERATION: env.LOOM_CDA_GENERATION,
        LOOM_DEV_ARTIFACTS: resolve(configuredSourceRoot, '.artifacts/cda-playwright'),
      }, configuredSourceRoot);
      await assertOwnedDevSession(devTarget, { uiRouting: cdaUiRouting });
      const apiContainerLabels = execFileSync('docker', [
        'inspect', '--format', '{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}',
        env.LOOM_CDA_API_CONTAINER,
      ], { encoding: 'utf8', timeout: 10_000 }).trim();
      assert.equal(apiContainerLabels, `${devTarget.composeProject}|loom-api`,
        'Named API container does not belong to the validated isolated stack');
    }

    if (cdaCreateFreshBasicQuantityProject) {
      assert(shouldCreateFreshBasicQuantityProject({ scenarioID: cdaScenarioID, caseName: cdaCaseName }),
        'Only the registered Basic Quantity ONE/ALL lifecycle may create a fresh Basic project');
      const runID = `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
      const fresh = await createFreshBasicQuantityFixture(devTarget, runID);
      devTarget = fresh.target;
      env = Object.freeze({
        ...env,
        LOOM_CDA_PROJECT: devTarget.fixtureProject,
        LOOM_CDA_GENERATION: devTarget.fixtureGeneration,
        LOOM_DEV_PROJECT: devTarget.fixtureProject,
      });
    }

    const owned = await assertOwnedCdaTarget({
      project: env.LOOM_CDA_PROJECT,
      apiOrigin: env.LOOM_CDA_API_ORIGIN,
      uiOrigin: env.LOOM_CDA_UI_ORIGIN,
      apiContainer: env.LOOM_CDA_API_CONTAINER,
      composeProject: env.LOOM_CDA_COMPOSE_PROJECT,
      sourceRoot,
      arangoContainer: env.LOOM_CDA_ARANGO_CONTAINER,
      clickhouseContainer: env.LOOM_CDA_CLICKHOUSE_CONTAINER,
    });
    const apiUrl = `${env.LOOM_CDA_API_ORIGIN.replace(/\/$/, '')}`;
    const uiUrl = `${env.LOOM_CDA_UI_ORIGIN.replace(/\/$/, '')}`;
    const target = {
      ...owned,
      ...devTarget,
      kind: 'owned-cda',
      fixtureProject: owned.project,
      project: owned.project,
      fixtureGeneration: env.LOOM_CDA_GENERATION ?? null,
      explorer: env.LOOM_CDA_EXPLORER_ID ?? env.LOOM_CDA_EXPLORER ?? env.LOOM_QA_EXPLORER ?? null,
      apiUrl,
      uiUrl,
      apiOrigin: apiUrl,
      uiOrigin: uiUrl,
      generation: env.LOOM_CDA_GENERATION ?? null,
      fixtureDir: devTarget.fixtureDir ?? fixtureDir ?? null,
      artifacts: devTarget.artifacts,
    };
    const evidenceDirectory = testInfo.outputPath('cda-evidence');
    await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
    const reportPath = testInfo.outputPath('cda-report.json');
    const report = makeReport({ scenarioID: cdaScenarioID, caseName: cdaCaseName, target, evidenceDirectory });
    report.caseName = cdaCaseName;
    report.nativeRequests = [];
    report.incidentalErrors = [];
    report.authorization = {
      browserContext: 'fresh Playwright page context',
      browserNoAuth: true,
      directAPICredentialsConfigured: Boolean(process.env.LOOM_CDA_API_TOKEN || process.env.LOOM_CDA_TOKEN),
    };
    report.fixtureTimings = { setupMs: Math.round(performance.now() - setupStarted) };

    const workflowStartedAt = performance.now();
    const ownedOrigins = new Set([apiUrl, uiUrl].map(value => new URL(value).origin));
    const captureFailureEvidence = async details => {
      if (report.failureEvidence) return;
      try {
        report.failureEvidence = await captureNativeFailureEvidence({ page, ownedOrigins, ...details });
        if (report.failureEvidence.action && details.failurePhase !== undefined) {
          report.failureEvidence.action.failurePhase = details.failurePhase;
          report.failureEvidence.action.phases = details.phases;
        }
      } catch (error) {
        report.failureEvidence = {
          reason: safeText(details.reason ?? 'CDA Playwright workflow failed'),
          captureError: safeText(error?.message ?? error),
          ...(details.failurePhase === undefined ? {} : {
            action: { failurePhase: details.failurePhase, phases: details.phases },
          }),
        };
      }
    };
    const faultAttempts = [];
    const faultHandlers = [];
    let navigationSequence = 0;
    let actionSequence = 0;
    const mainFrameNavigations = [];
    let droppedNavigationTimings = 0;
    let droppedDiagnostics = 0;
    let activeAction;
    let activeLocator;
    let activeActionContext;
    let firstFailureActionContext;
    const scopedRequest = request => isOwnedCdaScopedRequest(request.url(), target, ownedOrigins);
    const operationResponsePath = /(?:\/commands|\/authoring\/v2\/(?:publish|save)|\/reconcile|\/preview|\/construction-proposals|\/construction-choice-proposals|\/construction-capabilities|\/row-definition-proposals|\/row-definition-choices|\/table-shape-proposals|\/cell-trace|\/population-mapping|\/row-lineage|\/selections|\/explicit-groups|\/related-expand-choices)(?:\/|$)/;
    const requestedResponsePaths = [];
    const shouldCaptureResponsePath = pathname => operationResponsePath.test(pathname) || requestedResponsePaths.some(candidate => {
      if (candidate instanceof RegExp) {
        candidate.lastIndex = 0;
        return candidate.test(pathname);
      }
      if (typeof candidate === 'function') return Boolean(candidate(pathname));
      return typeof candidate === 'string' && candidate === pathname;
    });
    const observer = createPlaywrightNetworkObserver(page, {
      scopeRequest: scopedRequest,
      appOrigins: [...ownedOrigins],
      responsePaths: shouldCaptureResponsePath,
      report,
    });
    const diagnostics = observer.diagnostics;
    const addNetworkDiagnostic = entry => {
      if (report.network.length < MAX_DIAGNOSTICS) report.network.push(entry);
      else droppedDiagnostics += 1;
      if (entry.kind === 'exception') {
        report.errors.push(entry);
      }
    };
    const onFrameNavigated = frame => {
      if (frame !== page.mainFrame()) return;
      let origin;
      try { origin = new URL(frame.url()).origin; } catch { return; }
      if (!ownedOrigins.has(origin)) return;
      const startedAt = performance.now();
      const sequence = ++navigationSequence;
      if (mainFrameNavigations.length >= MAX_DIAGNOSTICS) {
        droppedNavigationTimings += 1;
        return;
      }
      mainFrameNavigations.push({ id: `navigation-${sequence}`, sequence, startedAt,
        atMs: Math.round(startedAt - workflowStartedAt), url: safeURL(frame.url()), phase: 'commit' });
    };
    page.on('framenavigated', onFrameNavigated);

    let customDialogHandler;
    const onUnexpectedDialog = async dialog => {
      // Dialog listeners are snapshotted before dispatch, so an existing page.once('dialog')
      // handler is already visible here even though this observer was registered first.
      if (page.listeners('dialog').some(listener => listener !== onUnexpectedDialog)) return;
      const message = `Unexpected ${dialog.type()} dialog: ${safeText(dialog.message())}`;
      addNetworkDiagnostic({ kind: 'exception', message });
      await dialog.dismiss().catch(() => undefined);
    };
    page.on('dialog', onUnexpectedDialog);

    const action = async (label, locator, perform, {
      after,
      timeout = ACTION_TIMEOUT_MS,
      budget = ACTION_TIMEOUT_MS,
      editable = false,
      requiredCheck,
    } = {}) => base.step(label, async () => {
      activeAction = safeText(label);
      activeLocator = locator;
      const started = performance.now();
      const startedAt = Date.now();
      const actionContext = { id: `cda-action-${++actionSequence}`, label: safeText(label), locator, startedAt, startedMs: Math.round(started - workflowStartedAt) };
      activeActionContext = actionContext;
      const actionTimeout = Math.max(1, Math.min(ACTION_TIMEOUT_MS, Number.isFinite(timeout) ? timeout : ACTION_TIMEOUT_MS));
      const budgetMs = Math.max(1, Math.min(ACTION_TIMEOUT_MS, Number.isFinite(budget) ? budget : ACTION_TIMEOUT_MS));
      const phases = {
        count: { completed: false },
        trial: { completed: false },
        ...(editable ? { editable: { completed: false } } : {}),
        perform: { completed: false },
        ...(after ? { after: { completed: false } } : {}),
      };
      actionContext.phases = phases;
      const runPhase = async (name, work) => {
        failurePhase = name;
        actionContext.failurePhase = name;
        const phaseStarted = performance.now();
        try {
          const result = await work();
          phases[name].completed = true;
          return result;
        } finally {
          phases[name].durationMs = Math.round(performance.now() - phaseStarted);
        }
      };
      let performCompleted = false;
      let afterCompleted = false;
      let elapsedMs;
      let failureReason;
      let failurePhase;
      try {
        failurePhase = 'count';
        await runPhase('count', () => expect(locator, `${label}: expected exactly one control`).toHaveCount(1, { timeout: actionTimeout }));
        failurePhase = 'trial';
        await runPhase('trial', () => locator.click({ trial: true, timeout: actionTimeout }));
        if (editable) {
          failurePhase = 'editable';
          await runPhase('editable', () => expect(locator, `${label}: expected an editable control`).toBeEditable({ timeout: actionTimeout }));
        }
        failurePhase = 'perform';
        await runPhase('perform', async () => {
          await perform(locator);
          performCompleted = true;
        });
        if (after) {
          failurePhase = 'after';
          await runPhase('after', async () => {
            await after();
            afterCompleted = true;
          });
        }
        elapsedMs = performance.now() - started;
        failurePhase = 'budget';
        actionContext.failurePhase = 'budget';
        expect(elapsedMs, `${label} action-to-render exceeded ${ACTION_TIMEOUT_MS} ms`).toBeLessThanOrEqual(budgetMs);
        failurePhase = undefined;
        return elapsedMs;
      } catch (error) {
        failureReason = error?.message ?? error;
        throw error;
      } finally {
        const actionEpoch = captureCdaActionEpoch(startedAt);
        elapsedMs ??= performance.now() - started;
        const passed = performCompleted && (!after || afterCompleted) && elapsedMs <= budgetMs;
        actionContext.failurePhase = passed ? null : failurePhase ?? actionContext.failurePhase ?? 'unknown';
        actionContext.phases = phases;
        if (!passed && !firstFailureActionContext) firstFailureActionContext = actionContext;
        // Leave a full read-capture window before the native step deadline; teardown captures late failures.
        if (!passed && !report.failureEvidence && elapsedMs + MAX_NATIVE_FAILURE_CAPTURE_MS + 250 < ACTION_TIMEOUT_MS) {
          await captureFailureEvidence({
            reason: failureReason ?? `${label} did not complete successfully`,
            label,
            locator,
            elapsedMs,
            startedAt,
            failurePhase: actionContext.failurePhase,
            phases,
          });
        }
        report.actions.push({
          ...(actionEpoch ?? {}),
          id: actionContext.id, label: safeText(label), status: passed ? 'passed' : 'failed', elapsedMs: Math.round(elapsedMs),
          failurePhase: actionContext.failurePhase, phases,
          locator: safeText(locator.toString()),
          ...(phases.after ? { afterMs: phases.after.durationMs } : {}),
        });
        report.timings[label] = Math.round(elapsedMs);
        recordCheck(report, 'usability', `${label} completed`, passed,
          { elapsedMs: Math.round(elapsedMs), afterMs: phases.after?.durationMs ?? null,
            failurePhase: actionContext.failurePhase, phases });
        if (after) recordCheck(report, 'performance', requiredCheck ?? `${label} action-to-render within budget`,
          passed, { afterMs: phases.after.durationMs ?? null, budgetMs, failurePhase: actionContext.failurePhase, phases });
        if (passed && activeActionContext === actionContext) activeActionContext = undefined;
        if (passed) {
          activeAction = undefined;
          activeLocator = undefined;
        }
      }
    }, { timeout: ACTION_TIMEOUT_MS });

    const check = (dimension, name, passed, evidence = {}) => {
      const success = Boolean(passed);
      recordCheck(report, dimension, name, success, evidence);
      expect(success, name).toBe(true);
      return success;
    };
    const step = (label, work, timeout = ACTION_TIMEOUT_MS) => base.step(label, work, { timeout });
    const fault = async ({ method, path, matchesRequest, response, reason }) => {
      const faultTarget = ownedFaultTarget(target, { method, path });
      const responseStatus = response == null ? null :
        (typeof response.status === 'function' ? response.status() : (response.status ?? 200));
      const attempt = {
        id: `injected-${faultAttempts.length + 1}`,
        ...faultTarget,
        matched: false,
        action: response == null ? 'abort' : 'fulfill',
        responseStatus,
      };
      faultAttempts.push(attempt);
      report.injectedRequests ??= [];
      const evidence = {
        id: attempt.id, matched: false, origin: faultTarget.origin, path: faultTarget.path,
        method: faultTarget.method, action: attempt.action, configuredResponseStatus: responseStatus,
      };
      report.injectedRequests.push(evidence);
      const handler = async route => {
        const request = route.request();
        if (attempt.matched || !matchesOwnedFaultRequest(request, faultTarget, matchesRequest)) {
          await route.fallback();
          return;
        }
        attempt.matched = true;
        attempt.rawURL = request.url();
        attempt.browserRequestId = observer.byRequest.get(request)?.browserRequestId;
        evidence.matched = true;
        evidence.url = safeURL(attempt.rawURL);
        if (attempt.browserRequestId) evidence.browserRequestId = attempt.browserRequestId;
        if (response == null) {
          const observed = observer.byRequest.get(request);
          if (observed && !observed.isMutation) observer.markExpectedTransportFailure(request, {
            reason: reason ?? `Injected route failure for ${faultTarget.method} ${faultTarget.path}`,
            action: 'abort',
          });
          await route.abort('failed');
        } else {
          await route.fulfill(response);
        }
      };
      await page.route('**/*', handler);
      faultHandlers.push(handler);
      return { id: attempt.id, path: attempt.path, count: () => Number(attempt.matched), evidence };
    };
    const expectTransportFailure = (request, details) => observer.markExpectedTransportFailure(request, details);
    const expectHttpFailure = (capturedEntry, reason, proof, { status = 422 } = {}) => {
      if (status !== 400 && status !== 422) {
        throw new RangeError('Expected HTTP failures may classify only exact native status 400 or 422 responses.');
      }
      const evidence = observer.markExpectedValidation(capturedEntry, { status, reason, proof });
      report.expectedHttpFailures ??= [];
      if (!report.expectedHttpFailures.some(item => item.browserRequestId === evidence.browserRequestId)) {
        report.expectedHttpFailures.push(evidence);
      }
      return evidence;
    };
    const captureRequests = (ownedPathPrefix, options = {}) => {
      if (options.responsePaths) {
        const paths = Array.isArray(options.responsePaths) ? options.responsePaths : [options.responsePaths];
        requestedResponsePaths.push(...paths);
      }
      return createCDARequestView(observer, {
        browserRequestOrigin: options.browserRequestOrigin ?? uiUrl,
        ownedPathPrefix,
      });
    };

    const context = {
      page,
      browserContext: page.context(),
      request: page.context().request,
      report,
      nativeRequests: report.nativeRequests,
      errors: report.errors,
      assetFailures: report.assetFailures,
      reportPath,
      evidenceDirectory,
      evidence: evidenceDirectory,
      target,
      env,
      project: target.fixtureProject,
      explorer: target.explorer,
      caseName: cdaCaseName,
      scenarioID: cdaScenarioID,
      apiOrigin: target.apiUrl,
      uiOrigin: target.uiUrl,
      generation: target.fixtureGeneration,
      action,
      check,
      fault,
      expectTransportFailure,
      expectHttpFailure,
      captureRequests,
      requestBody: observer.requestBody,
      responseBody: observer.responseBody,
      step,
      setActionEvidence: (label, locator) => {
        activeAction = safeText(label);
        activeLocator = locator;
        const startedAt = Date.now();
        const started = performance.now();
        activeActionContext = { id: `cda-action-${++actionSequence}`, label: activeAction, locator, startedAt,
          startedMs: Math.round(started - workflowStartedAt) };
        report.activeAction = {
          id: activeActionContext.id,
          label: activeAction,
          locator: locator?.toString ? safeText(locator.toString()) : undefined,
          startedAt,
          startedMs: activeActionContext.startedMs,
        };
      },
    };
    const cda = {
      ...context,
      diagnostics,
      step,
      action,
      check,
      fault,
      expectTransportFailure,
      expectHttpFailure,
      onDialog: handler => {
        if (customDialogHandler) page.removeListener('dialog', customDialogHandler);
        page.removeListener('dialog', onUnexpectedDialog);
        customDialogHandler = async dialog => {
          try {
            const result = await handler({
              type: dialog.type(),
              message: dialog.message(),
              defaultValue: dialog.defaultValue(),
            });
            if (result?.accept === false) await dialog.dismiss();
            else await dialog.accept(result?.promptText);
          } catch (error) {
            report.errors.push({ kind: 'dialog-handler', message: safeText(error?.message ?? error) });
            await dialog.dismiss().catch(() => undefined);
            throw error;
          }
        };
        page.on('dialog', customDialogHandler);
      },
      click: (selector, identity, timeout) => click(page, selector, identity, timeout, context),
      fill: (selector, value, identity, timeout) => fill(page, selector, value, identity, timeout, context),
      press: (selector, key, timeout) => press(page, selector, key, timeout, context),
      selectOption: (selector, value, options) => selectOption(page, selector, value, options, context),
      scrollIntoView: (selector, identity, timeout) => scrollIntoView(page, selector, identity, timeout, context),
      navigate: createCdaNavigator({
        target,
        uiRouting: cdaUiRouting,
        navigateTo: url => navigate(page, url, context),
      }),
      inspect: createCdaInspector(page),
      wait: (callback, args, timeout) => waitForBrowser(page, callback, args, timeout),
      attachReport: async (name, value = report) => {
        const path = safeAttachmentPath(testInfo, name);
        await mkdir(dirname(path), { recursive: true, mode: 0o700 });
        await writeFile(path, `${JSON.stringify(sanitizePayload(value), null, 2)}\n`, { mode: 0o600 });
        await testInfo.attach(name, { path, contentType: 'application/json' });
      },
    };

    const identity = await startVerificationIdentity(target.sourceRoot, target.apiContainer);
    report.target.sourceFingerprint = identity.sourceFingerprint;
    report.target.apiBuildIdentity = identity.apiBuildIdentity;
    report.fixtureTimings.setupMs = Math.round(performance.now() - setupStarted);
    try {
      await use(cda);
    } finally {
      observer.stop();
    }

    let freezeError;
    let reportAttachmentError;
    let observerAssertionError;
    try {
      for (const handler of faultHandlers) await page.unroute('**/*', handler);
    } catch (error) {
      report.errors.push({ kind: 'fault-route-cleanup', message: safeText(error?.message ?? error) });
    }
    try {
      report.navigationTimings = mainFrameNavigations.map(({ id, atMs, url, phase }) => ({ id, atMs, url, phase }));
      report.browserLifecycle = {
        kind: 'official-playwright-page',
        status: testInfo.status,
        durationMs: Math.round(performance.now() - setupStarted),
        diagnosticLimit: MAX_DIAGNOSTICS,
        droppedDiagnostics,
        droppedNavigationTimings,
      };
      const result = await identity.finish();
      report.verificationIdentity = sanitizePayload(result);
      recordCheck(report, 'correctness', 'CDA watched source and API build stayed unchanged', true, report.verificationIdentity);
    } catch (error) {
      freezeError = error;
      report.invalidations ??= [];
      report.invalidations.push({ kind: 'source-api-freeze', reason: safeText(error?.message ?? error) });
      recordCheck(report, 'correctness', 'CDA watched source and API build stayed unchanged', false,
        { message: safeText(error?.message ?? error) });
    } finally {
      page.removeListener('framenavigated', onFrameNavigated);
      page.removeListener('dialog', onUnexpectedDialog);
      if (customDialogHandler) page.removeListener('dialog', customDialogHandler);
      if (report.assetFailures.length) recordCheck(report, 'correctness', 'incidental asset failures are explicitly recorded', true,
        { failures: report.assetFailures });
      if (report.requiredChecks.length) finishReport(report);
      else {
        report.missingRequiredChecks = [];
        report.status = report.assertions.some(assertion => assertion.status === 'failed') ? 'failed' : 'unverified';
        report.finishedAt = new Date().toISOString();
      }
      report.runnerStatus = testInfo.status;
      finalizeCdaExplorerMetadata(report, target);
      report.authorization = {
        browserContext: 'fresh Playwright page context',
        browserNoAuth: true,
        directAPICredentialsConfigured: Boolean(process.env.LOOM_CDA_API_TOKEN || process.env.LOOM_CDA_TOKEN),
      };
      const runnerSkipped = testInfo.status === 'skipped';
      const failedAssertion = report.assertions.some(assertion => assertion.status === 'failed');
      if (freezeError || failedAssertion || (!runnerSkipped && testInfo.status !== 'passed')) {
        report.status = 'failed';
      } else if (runnerSkipped || report.status === 'running') {
        report.status = 'unverified';
      }
      report.finishedAt = new Date().toISOString();
      if (testInfo.status === 'passed') {
        try {
          assertNoUnexpectedFixtureFailures({
            observerFailures: observer.failures,
            reportErrors: report.errors,
            fixtureErrors: [],
          });
        } catch (error) {
          observerAssertionError = error;
        }
      }
      if (observerAssertionError) {
        report.status = 'failed';
        report.errors.push({ kind: 'browser-observer-assertion', message: safeText(observerAssertionError.message ?? observerAssertionError) });
        report.finishedAt = new Date().toISOString();
      }
      if (report.status === 'failed' && !report.failureEvidence) {
        const currentAction = firstFailureActionContext ?? activeActionContext;
        await captureFailureEvidence({
          reason: observerAssertionError?.message ?? testInfo.error?.message ?? freezeError?.message ??
            report.errors.at(-1)?.message ?? 'CDA Playwright workflow failed outside the action helper',
          label: currentAction?.label ?? activeAction ?? report.activeAction?.label,
          locator: currentAction?.locator ?? activeLocator,
          startedAt: currentAction?.startedAt ?? report.activeAction?.startedAt,
          failurePhase: currentAction?.failurePhase,
          phases: currentAction?.phases,
        });
      }
      try {
        writeReport(reportPath, sanitizePayload(report));
        await testInfo.attach('cda-domain-report.json', { path: reportPath, contentType: 'application/json' });
      } catch (error) {
        reportAttachmentError = error;
        report.attachmentError = safeText(error?.message ?? error);
        if (testInfo.status === 'passed') {
          report.status = 'failed';
          report.errors.push({ kind: 'report-attachment', message: report.attachmentError });
        }
        report.finishedAt = new Date().toISOString();
        try { writeReport(reportPath, sanitizePayload(report)); } catch { /* Preserve the test failure when the report path itself is unavailable. */ }
      }
    }
    if (reportAttachmentError && testInfo.status === 'passed') throw reportAttachmentError;
    if (freezeError) throw freezeError;
    if (observerAssertionError) throw observerAssertionError;
  },
});
