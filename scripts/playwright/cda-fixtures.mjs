import { test as base, expect } from '@playwright/test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { realpathSync } from 'node:fs';
import { performance } from 'node:perf_hooks';
import { mkdir, writeFile } from 'node:fs/promises';
import { basename, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startVerificationIdentity } from '../lib/cda-verification-identity.mjs';
import { assertOwnedCdaTarget } from '../lib/owned-cda-target.mjs';
import { assertOwnedDevSession, createDevSession } from '../loom-dev.mjs';
import {
  browserEval,
  captureRequests,
  click,
  fill,
  includeBrowserDiagnostics,
  navigate,
  press,
  scrollIntoView,
  selectOption,
  waitForBrowser,
  waitForCapturedResponse,
} from '../lib/cda-playwright.mjs';
import { applyInjectedFaultPolicy, matchExpectedHttpConsole, ownedFaultTarget, matchesOwnedFaultRequest } from './network-evidence.mjs';
import { registry, requiredChecksFor } from '../verify-ui/registry.mjs';
import { classifyNetworkRecord, createReport, finishReport, recordCheck, writeReport } from '../verify-ui/report.mjs';
import { sanitizeBody, sanitizePayload, sanitizeText } from '../lib/playwright-browser.mjs';
import { captureNativeFailureEvidence, MAX_NATIVE_FAILURE_CAPTURE_MS } from './native-failure-evidence.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const MAX_DIAGNOSTICS = 100;
const sourceRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const safeText = value => sanitizeText(value).slice(0, 4_000);

function safeURL(raw) {
  try {
    const url = new URL(raw);
    return safeText(`${url.origin}${url.pathname}`);
  } catch {
    return safeText(raw);
  }
}

function environmentSnapshot(overrides) {
  const env = {
    LOOM_CDA_PROJECT: overrides.project,
    LOOM_CDA_API_ORIGIN: overrides.apiOrigin,
    LOOM_CDA_UI_ORIGIN: overrides.uiOrigin,
    LOOM_CDA_COMPOSE_PROJECT: overrides.composeProject,
    LOOM_CDA_API_CONTAINER: overrides.apiContainer,
    LOOM_CDA_ARANGO_CONTAINER: overrides.arangoContainer,
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
    requiredChecks: scenario ? requiredChecksFor(scenario, caseName) : [],
  });
}

function requestDiagnostic(request) {
  let body;
  try { body = request.postDataJSON(); } catch { body = undefined; }
  const input = body?.variables?.input ?? body ?? {};
  const headers = request.headers();
  return sanitizePayload({
    requestId: headers['x-request-id'] ?? body?.requestId ?? body?.requestID ?? null,
    draftVersion: input.expectedDraftVersion ?? input.draftVersion ?? null,
    draftDigest: input.expectedDraftDigest ?? input.draftDigest ?? null,
    outputId: input.outputId ?? null,
    stageId: input.stageId ?? null,
  });
}

function safeAttachmentPath(testInfo, name) {
  const basenameOnly = basename(String(name || 'cda-attachment.json'));
  const safeName = basenameOnly.replace(/[^a-zA-Z0-9_.-]/g, '_') || 'cda-attachment.json';
  return testInfo.outputPath('attachments', safeName);
}

function gateFailure(report) {
  const missing = report.runnerStatus === 'skipped' ? [] : report.missingRequiredChecks ?? [];
  const failedAssertions = (report.assertions ?? []).filter(entry => entry.status === 'failed');
  const unexpectedNetwork = (report.network ?? []).filter(entry =>
    !entry.expectedHttpFailure && classifyNetworkRecord(entry) === 'unexpected-error');
  const unexpectedErrors = (report.errors ?? []).filter(entry => {
    if (entry.expected === true || entry.expectedCancellation || entry.expectedInjectedFault || entry.expectedHttpFailure) return false;
    if (entry.kind === 'expected-injected' && entry.injectedFault === true) return false;
    return true;
  });
  if (!missing.length && !failedAssertions.length && !unexpectedNetwork.length && !unexpectedErrors.length) return undefined;
  return new Error(`CDA verification evidence is incomplete: ${JSON.stringify({
    missingRequiredChecks: missing,
    failedAssertions: failedAssertions.map(({ dimension, name, evidence }) => ({ dimension, name, evidence })),
    unexpectedNetwork: unexpectedNetwork.map(({ kind, method, url, status, errorText, message }) => ({ kind, method, url, status, errorText, message })),
    unexpectedErrors: unexpectedErrors.map(({ kind, method, url, status, message, error }) => ({ kind, method, url, status, message, error })),
  })}`);
}

function markExpectedErrors(report) {
  const expectedRequests = (report.network ?? []).filter(entry => entry.expected === true || entry.injectedFault === true);
  const matchedConsoleErrors = new Set();
  for (const evidence of expectedRequests) {
    const matchesRequest = error => Boolean(
      (evidence.browserRequestId && error.browserRequestId === evidence.browserRequestId) ||
      (evidence.playwrightRequestId && error.playwrightRequestId === evidence.playwrightRequestId),
    );
    for (const error of report.errors ?? []) {
      if (!matchesRequest(error)) continue;
      error.expected = true;
      if (evidence.injectedFault) error.expectedInjectedFault = {
        requestId: evidence.injectedRequestId,
        action: evidence.injectedAction,
      };
      if (evidence.expectedCancellation) error.expectedCancellation = evidence.expectedCancellation;
    }
    if (!evidence.injectedFault || evidence.observedAs !== 'console-error') continue;
    const index = (report.errors ?? []).findIndex((error, candidateIndex) => !matchedConsoleErrors.has(candidateIndex) &&
      error.kind === 'console' && error.message === evidence.text && error.location === evidence.location);
    if (index >= 0) {
      matchedConsoleErrors.add(index);
      report.errors[index].expected = true;
      report.errors[index].expectedInjectedFault = {
        requestId: evidence.injectedRequestId,
        action: evidence.injectedAction,
      };
    }
  }
}

export function classifyExpectedCdaCancellation({ request, reason, proof, report, requestFailures, trackers }) {
  if (!request || typeof request.url !== 'function') throw new TypeError('Expected cancellation needs the native Playwright Request object.');
  if (typeof reason !== 'string' || !reason.trim()) throw new TypeError('Expected cancellation needs a concrete reason.');
  if (!proof || typeof proof !== 'object') throw new TypeError('Expected cancellation needs request/action proof.');
  const failure = requestFailures.get(request);
  if (!failure) throw new Error('Expected cancellation must match an exact observed native request failure.');
  if (failure.errorText !== 'net::ERR_ABORTED') throw new Error('Only a native net::ERR_ABORTED request can be marked as an expected cancellation.');

  const capturedEntry = [...trackers].map(tracker => tracker.byRequest.get(request)).find(Boolean);
  let cancellation;
  if (failure.expected) {
    if (failure.expectedCancellation?.reason !== reason) {
      throw new Error('This exact native request already has a different cancellation classification.');
    }
    cancellation = failure.expectedCancellation;
  } else {
    cancellation = sanitizePayload({
      requestId: failure.requestId ?? failure.playwrightRequestId,
      playwrightRequestId: failure.playwrightRequestId,
      browserRequestId: capturedEntry?.browserRequestId,
      method: failure.method,
      url: failure.url,
      reason: safeText(reason),
      proof,
    });
    failure.expected = true;
    failure.canceled = true;
    failure.cancellationReason = cancellation.reason;
    failure.expectedCancellation = cancellation;
    report.expectedCancellations ??= [];
    report.expectedCancellations.push(cancellation);
    if (capturedEntry) {
      capturedEntry.expected = true;
      capturedEntry.canceled = true;
      capturedEntry.cancellationReason = cancellation.reason;
      capturedEntry.expectedCancellation = cancellation;
    }
  }

  if (capturedEntry?.browserRequestId) {
    const sameCapturedRequest = entry => entry.kind === 'network' &&
      entry.browserRequestId === capturedEntry.browserRequestId;
    for (const diagnostic of report.network ?? []) {
      if (!sameCapturedRequest(diagnostic)) continue;
      diagnostic.expected = true;
      diagnostic.canceled = true;
      diagnostic.cancellationReason = cancellation.reason;
      diagnostic.expectedCancellation = cancellation;
    }
    for (const capturedError of report.errors ?? []) {
      if (!sameCapturedRequest(capturedError)) continue;
      capturedError.expected = true;
      capturedError.expectedCancellation = cancellation;
    }
  }
  return cancellation;
}

function finishCdaReport(report) {
  const completeNetwork = report.network;
  report.network = completeNetwork.filter(entry => !entry.expectedHttpFailure);
  try {
    return finishReport(report);
  } finally {
    report.network = completeNetwork;
  }
}

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
  cdaRequireClickhouse: [false, { option: true }],
  cdaRequireSourceFixture: [false, { option: true }],

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
    cdaRequireClickhouse,
    cdaRequireSourceFixture,
  }, use, testInfo) => {
    const setupStarted = performance.now();
    const env = environmentSnapshot({
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
      await assertOwnedDevSession(devTarget);
      const apiContainerLabels = execFileSync('docker', [
        'inspect', '--format', '{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}',
        env.LOOM_CDA_API_CONTAINER,
      ], { encoding: 'utf8', timeout: 10_000 }).trim();
      assert.equal(apiContainerLabels, `${devTarget.composeProject}|loom-api`,
        'Named API container does not belong to the validated isolated stack');
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

    const diagnostics = { console: [], pageErrors: [], networkFailures: [], httpFailures: [], assetFailures: [] };
    const ownedOrigins = new Set([apiUrl, uiUrl].map(value => new URL(value).origin));
    const captureFailureEvidence = async details => {
      if (report.failureEvidence) return;
      try {
        report.failureEvidence = await captureNativeFailureEvidence({ page, ownedOrigins, ...details });
      } catch (error) {
        report.failureEvidence = {
          reason: safeText(details.reason ?? 'CDA Playwright workflow failed'),
          captureError: safeText(error?.message ?? error),
        };
      }
    };
    const trackers = new Set();
    const faultAttempts = [];
    const faultHandlers = [];
    const requestIDs = new WeakMap();
    const requestFailures = new WeakMap();
    const pendingRequests = new Set();
    const cancellationScopes = [];
    let requestSequence = 0;
    let activeAction;
    let activeLocator;
    let activeActionContext;
    let firstFailureActionContext;
    let retainedDiagnostics = 0;
    let droppedDiagnostics = 0;
    const addNetworkDiagnostic = entry => {
      if (retainedDiagnostics < MAX_DIAGNOSTICS) {
        report.network.push(entry);
        retainedDiagnostics += 1;
      } else {
        droppedDiagnostics += 1;
      }
    };
    const localRequest = request => {
      try { return ownedOrigins.has(new URL(request.url()).origin); }
      catch { return false; }
    };
    const playwrightRequestId = request => {
      let id = requestIDs.get(request);
      if (!id) {
        id = `cda-request-${++requestSequence}`;
        requestIDs.set(request, id);
      }
      return id;
    };
    const capturedEntryFor = request => {
      for (const tracker of trackers) {
        const entry = tracker.byRequest.get(request);
        if (entry) return entry;
      }
      return undefined;
    };
    const matchesCancellationScope = (request, scope) => {
      try {
        const url = new URL(request.url());
        const requestId = request.headers()['x-request-id'] ?? playwrightRequestId(request);
        return Date.now() - scope.armedAt <= ACTION_TIMEOUT_MS && url.origin === scope.origin &&
          request.method() === scope.method && scope.paths.includes(url.pathname) &&
          scope.requestIdPrefixes.some(prefix => requestId.startsWith(prefix));
      } catch {
        return false;
      }
    };
    const onRequest = request => {
      playwrightRequestId(request);
      if (!localRequest(request)) return;
      pendingRequests.add(request);
      for (const scope of cancellationScopes) {
        if (scope.active && matchesCancellationScope(request, scope)) scope.requests.add(request);
      }
    };
    const onRequestFinished = request => {
      pendingRequests.delete(request);
      for (const scope of cancellationScopes) scope.requests.delete(request);
    };
    const onConsole = message => {
      if (message.type() !== 'error') return;
      const location = message.location().url;
      if (location && !localRequest({ url: () => location })) return;
      if (location && new URL(location).pathname === '/favicon.ico' && /404(?: \(Not Found\))?/i.test(message.text())) {
        const entry = { kind: 'asset-failure', url: safeURL(location), status: 404 };
        diagnostics.assetFailures.push(entry);
        report.assetFailures.push(entry);
        return;
      }
      const entry = { kind: 'console-error', text: safeText(message.text()), location: safeURL(location), rawLocation: location };
      const expectedConsoleMatches = report.nativeRequests
        .filter(request => request.expectedHttpFailure && typeof request.expectedHttpFailure === 'object' &&
          !request.fixtureExpectedHttpConsoleConsumed)
        .map(capturedEntry => ({
          capturedEntry,
          match: matchExpectedHttpConsole({
            capturedEntry,
            nativeRequests: report.nativeRequests,
            diagnostics: [entry],
          }),
        }))
        .filter(candidate => candidate.match?.fixtureDiagnostic === entry);
      if (expectedConsoleMatches.length === 1) {
        const { capturedEntry, match } = expectedConsoleMatches[0];
        const evidence = capturedEntry.expectedHttpFailure;
        if (!evidence.console) {
          evidence.console = {
            status: match.status,
            message: safeText(match.message),
            location: safeURL(match.location),
            fixtureDiagnostic: true,
            requestCaptureError: false,
          };
          capturedEntry.fixtureExpectedHttpConsoleConsumed = true;
          return;
        }
      }
      diagnostics.console.push(entry);
      addNetworkDiagnostic(entry);
    };
    const onPageError = error => {
      const entry = { kind: 'exception', message: safeText(error.message), stack: safeText(error.stack) };
      diagnostics.pageErrors.push(entry);
      addNetworkDiagnostic(entry);
    };
    const onRequestFailed = request => {
      if (!localRequest(request)) return;
      const entry = {
        kind: 'network', method: request.method(), url: safeURL(request.url()),
        resourceType: request.resourceType(), errorText: safeText(request.failure()?.errorText),
        triggerAction: activeAction,
        requestId: request.headers()['x-request-id'] ?? null,
        playwrightRequestId: playwrightRequestId(request),
        browserRequestId: capturedEntryFor(request)?.browserRequestId,
        rawURL: request.url(),
        expected: false,
      };
      diagnostics.networkFailures.push(entry);
      requestFailures.set(request, entry);
      addNetworkDiagnostic(entry);
      for (const scope of cancellationScopes) {
        if (!scope.active || !scope.requests.has(request) || entry.errorText !== 'net::ERR_ABORTED') continue;
        entry.cancellationAction = safeText(scope.actionLabel);
        expectCanceledRequest(request, scope.reason, {
          ...scope.proof,
          scopeAction: scope.actionLabel,
          scopeRequest: requestDiagnostic(request),
        });
        scope.requests.delete(request);
      }
      pendingRequests.delete(request);
    };
    const onResponse = response => {
      if (!localRequest(response.request()) || response.status() < 400) return;
      const url = safeURL(response.url());
      if (response.status() === 404 && new URL(response.url()).pathname === '/favicon.ico') {
        const entry = { kind: 'asset-failure', url, status: response.status() };
        diagnostics.assetFailures.push(entry);
        if (!report.assetFailures.some(value => value.url === url && value.status === response.status())) report.assetFailures.push(entry);
        return;
      }
      const entry = {
        kind: 'network', status: response.status(), method: response.request().method(), url,
        requestDetails: requestDiagnostic(response.request()),
        requestId: response.request().headers()['x-request-id'] ?? null,
        playwrightRequestId: playwrightRequestId(response.request()),
        browserRequestId: capturedEntryFor(response.request())?.browserRequestId,
        rawURL: response.url(),
        responseBody: { captureState: 'pending' },
      };
      const browserRequestId = entry.browserRequestId;
      const requestID = entry.playwrightRequestId;
      addNetworkDiagnostic(entry);
      void response.text().then(body => {
        entry.responseBody.captureState = 'completed';
        entry.responseBody.body = sanitizeBody(body);
        diagnostics.httpFailures.push({ url, status: response.status(), body: entry.responseBody.body,
          browserRequestId, playwrightRequestId: requestID });
      }, error => {
        entry.responseBody.captureState = 'readfailed';
        entry.responseBody.error = safeText(error?.message ?? error);
        diagnostics.httpFailures.push({ url, status: response.status(), body: entry.responseBody,
          browserRequestId, playwrightRequestId: requestID });
      });
    };
    page.on('request', onRequest);
    page.on('requestfinished', onRequestFinished);
    page.on('console', onConsole);
    page.on('pageerror', onPageError);
    page.on('requestfailed', onRequestFailed);
    page.on('response', onResponse);

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
      const actionContext = { label: safeText(label), locator, startedAt };
      activeActionContext = actionContext;
      const actionTimeout = Math.max(1, Math.min(ACTION_TIMEOUT_MS, Number.isFinite(timeout) ? timeout : ACTION_TIMEOUT_MS));
      const budgetMs = Math.max(1, Math.min(ACTION_TIMEOUT_MS, Number.isFinite(budget) ? budget : ACTION_TIMEOUT_MS));
      let performCompleted = false;
      let afterCompleted = false;
      let afterMs;
      let elapsedMs;
      let failureReason;
      try {
        await expect(locator, `${label}: expected exactly one control`).toHaveCount(1, { timeout: actionTimeout });
        await locator.click({ trial: true, timeout: actionTimeout });
        if (editable) await expect(locator, `${label}: expected an editable control`).toBeEditable({ timeout: actionTimeout });
        await perform(locator);
        performCompleted = true;
        if (after) {
          const afterStarted = performance.now();
          await after();
          afterMs = performance.now() - afterStarted;
          afterCompleted = true;
        }
        elapsedMs = performance.now() - started;
        expect(elapsedMs, `${label} action-to-render exceeded ${ACTION_TIMEOUT_MS} ms`).toBeLessThanOrEqual(budgetMs);
        return elapsedMs;
      } catch (error) {
        failureReason = error?.message ?? error;
        throw error;
      } finally {
        elapsedMs ??= performance.now() - started;
        const passed = performCompleted && (!after || afterCompleted) && elapsedMs <= budgetMs;
        if (!passed && !firstFailureActionContext) firstFailureActionContext = actionContext;
        // Leave a full read-capture window before the native step deadline; teardown captures late failures.
        if (!passed && !report.failureEvidence && elapsedMs + MAX_NATIVE_FAILURE_CAPTURE_MS + 250 < ACTION_TIMEOUT_MS) {
          await captureFailureEvidence({
            reason: failureReason ?? `${label} did not complete successfully`,
            label,
            locator,
            elapsedMs,
            startedAt,
          });
        }
        report.actions.push({
          label: safeText(label), status: passed ? 'passed' : 'failed', elapsedMs: Math.round(elapsedMs),
          locator: safeText(locator.toString()), ...(afterMs === undefined ? {} : { afterMs: Math.round(afterMs) }),
        });
        report.timings[label] = Math.round(elapsedMs);
        recordCheck(report, 'usability', `${label} completed`, passed,
          { elapsedMs: Math.round(elapsedMs), afterMs: afterMs === undefined ? null : Math.round(afterMs) });
        if (after) recordCheck(report, 'performance', requiredCheck ?? `${label} action-to-render within budget`,
          passed, { afterMs: afterMs === undefined ? null : Math.round(afterMs), budgetMs });
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
    const fault = async ({ method, path, matchesRequest, response }) => {
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
        attempt.playwrightRequestId = playwrightRequestId(request);
        evidence.matched = true;
        evidence.url = safeURL(attempt.rawURL);
        evidence.request = sanitizePayload({ requestId: attempt.playwrightRequestId, method: request.method() });
        if (response == null) await route.abort();
        else await route.fulfill(response);
      };
      await page.route('**/*', handler);
      faultHandlers.push(handler);
      return { id: attempt.id, path: attempt.path, count: () => Number(attempt.matched), evidence };
    };
    const expectCanceledRequest = (request, reason, proof) =>
      classifyExpectedCdaCancellation({ request, reason, proof, report, requestFailures, trackers });
    const expectCapturedCancellation = (capturedEntry, reason, proof) => {
      if (!capturedEntry || !report.nativeRequests.includes(capturedEntry)) {
        throw new TypeError('Expected cancellation must name an exact entry from this CDA fixture request capture.');
      }
      if (typeof capturedEntry.browserRequestId !== 'string' || !capturedEntry.browserRequestId ||
        report.nativeRequests.filter(entry => entry.browserRequestId === capturedEntry.browserRequestId).length !== 1) {
        throw new Error('Expected cancellation needs a unique native browser request ID.');
      }
      const nativeRequests = [...trackers].flatMap(tracker => [...tracker.byRequest.entries()]
        .filter(([, entry]) => entry === capturedEntry)
        .map(([request]) => request));
      if (nativeRequests.length !== 1) {
        throw new Error('Expected cancellation must resolve to exactly one retained native Playwright Request object.');
      }
      return expectCanceledRequest(nativeRequests[0], reason, proof);
    };
    const expectHttpFailure = (capturedEntry, reason, proof, { status = 422 } = {}) => {
      if (status !== 400 && status !== 422) {
        throw new RangeError('Expected HTTP failures may classify only exact native status 400 or 422 responses.');
      }
      if (!capturedEntry || !report.nativeRequests.includes(capturedEntry)) {
        throw new TypeError('Expected HTTP failures must name an exact entry from this CDA fixture request capture.');
      }
      if (typeof capturedEntry.browserRequestId !== 'string' || !capturedEntry.browserRequestId) {
        throw new Error('Expected HTTP failures need a concrete native browser request ID.');
      }
      if (capturedEntry.status !== status) {
        throw new Error(`Expected HTTP ${status} classification did not match the captured native response status.`);
      }
      if (!capturedEntry.completedAt) {
        throw new Error('Expected HTTP failures need a completed native response capture.');
      }
      if (capturedEntry.responseReadError || capturedEntry.response === undefined || capturedEntry.response?.bodyNotRead === true) {
        throw new Error('Expected HTTP failures need the captured response body to exclude internal errors.');
      }
      if (/\bINTERNAL(?:_SERVER)?_ERROR\b|\binternal(?: server)? error\b/i.test(JSON.stringify(capturedEntry.response))) {
        throw new Error('INTERNAL_ERROR responses cannot be classified as expected validation failures.');
      }
      if (typeof reason !== 'string' || !reason.trim()) throw new TypeError('Expected HTTP failures need a concrete reason.');
      if (!proof || typeof proof !== 'object') throw new TypeError('Expected HTTP failures need request/action proof.');
      const diagnostic = report.network.find(entry => entry.kind === 'network' &&
        entry.browserRequestId === capturedEntry.browserRequestId && entry.status === capturedEntry.status &&
        entry.method === capturedEntry.method);
      if (!diagnostic) throw new Error('Expected HTTP failure did not match the exact observed native response diagnostic.');
      const consoleMatch = matchExpectedHttpConsole({
        capturedEntry,
        nativeRequests: report.nativeRequests,
        diagnostics: diagnostics.console,
        errors: report.errors,
      });
      const evidence = sanitizePayload({
        browserRequestId: capturedEntry.browserRequestId,
        requestId: capturedEntry.requestId,
        method: capturedEntry.method,
        path: capturedEntry.path,
        status: capturedEntry.status,
        reason: safeText(reason),
        proof,
        ...(consoleMatch ? { console: {
          status: consoleMatch.status,
          message: safeText(consoleMatch.message),
          location: safeURL(consoleMatch.location),
          fixtureDiagnostic: Boolean(consoleMatch.fixtureDiagnostic),
          requestCaptureError: Boolean(consoleMatch.reportError),
        } } : {}),
      });
      if (diagnostic.expectedHttpFailure) {
        if (diagnostic.expectedHttpFailure.reason === evidence.reason) return diagnostic.expectedHttpFailure;
        throw new Error('This exact native HTTP response already has a different expected-failure classification.');
      }
      diagnostic.expected = true;
      diagnostic.expectedHttpFailure = evidence;
      if (consoleMatch?.fixtureDiagnostic) {
        consoleMatch.fixtureDiagnostic.expected = true;
        consoleMatch.fixtureDiagnostic.expectedHttpFailure = evidence;
        const index = diagnostics.console.indexOf(consoleMatch.fixtureDiagnostic);
        if (index < 0) throw new Error('Matched expected HTTP console diagnostic disappeared before classification.');
        diagnostics.console.splice(index, 1);
        capturedEntry.fixtureExpectedHttpConsoleConsumed = true;
      }
      if (consoleMatch?.reportError) {
        consoleMatch.reportError.expected = true;
        consoleMatch.reportError.expectedHttpFailure = evidence;
      }
      capturedEntry.expected = true;
      capturedEntry.expectedHttpFailure = evidence;
      report.expectedHttpFailures ??= [];
      report.expectedHttpFailures.push(evidence);
      for (const error of report.errors) {
        if (error.browserRequestId !== capturedEntry.browserRequestId) continue;
        error.expected = true;
        error.expectedHttpFailure = evidence;
      }
      return evidence;
    };
    const withExpectedCancellations = async (specification, work) => {
      if (typeof work !== 'function') throw new TypeError('Expected cancellation scope needs a work callback.');
      const { origin, method, paths, requestIdPrefixes, reason, proof = {}, actionLabel } = specification ?? {};
      const normalizedOrigin = new URL(origin).origin;
      if (!ownedOrigins.has(normalizedOrigin)) throw new Error('Expected cancellation origin must belong to the validated CDA target.');
      if (typeof method !== 'string' || !method.trim()) throw new TypeError('Expected cancellation method is required.');
      if (!Array.isArray(paths) || paths.length === 0 || paths.some(path => typeof path !== 'string' || !path.startsWith('/'))) {
        throw new TypeError('Expected cancellation needs exact request paths.');
      }
      if (!Array.isArray(requestIdPrefixes) || requestIdPrefixes.length === 0 || requestIdPrefixes.some(prefix => typeof prefix !== 'string' || !prefix)) {
        throw new TypeError('Expected cancellation needs request ID prefixes.');
      }
      if (typeof actionLabel !== 'string' || !actionLabel.trim()) throw new TypeError('Expected cancellation needs its active action label.');
      if (typeof reason !== 'string' || !reason.trim()) throw new TypeError('Expected cancellation needs a concrete reason.');
      const scope = {
        origin: normalizedOrigin,
        method: method.toUpperCase(),
        paths,
        requestIdPrefixes,
        reason,
        proof,
        actionLabel,
        armedAt: Date.now(),
        requests: new Set(),
        active: true,
      };
      cancellationScopes.push(scope);
      for (const request of pendingRequests) {
        if (matchesCancellationScope(request, scope)) scope.requests.add(request);
      }
      try {
        return await work();
      } finally {
        scope.active = false;
        cancellationScopes.splice(cancellationScopes.indexOf(scope), 1);
        scope.requests.clear();
      }
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
      expectCanceledRequest,
      expectCapturedCancellation,
      expectHttpFailure,
      withExpectedCancellations,
      step,
      setActionEvidence: (label, locator) => {
        activeAction = safeText(label);
        activeLocator = locator;
        activeActionContext = { label: activeAction, locator, startedAt: Date.now() };
        report.activeAction = {
          label: activeAction,
          locator: locator?.toString ? safeText(locator.toString()) : undefined,
          startedAt: Date.now(),
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
      expectCanceledRequest,
      expectCapturedCancellation,
      expectHttpFailure,
      withExpectedCancellations,
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
      navigate: url => navigate(page, url, context),
      inspect: (callback, args) => browserEval(page, callback, args),
      wait: (callback, args, timeout) => waitForBrowser(page, callback, args, timeout),
      captureRequests: (ownedPathPrefix, options = {}) => {
        const tracker = captureRequests(page, report, ownedPathPrefix, {
          apiOrigin: target.apiUrl,
          uiOrigin: target.uiUrl,
          currentAction: () => activeAction,
          ...options,
        });
        trackers.add(tracker);
        return tracker;
      },
      waitForCapturedResponse: (tracker, predicate, timeout) => waitForCapturedResponse(page, tracker, predicate, timeout),
      includeBrowserDiagnostics: () => includeBrowserDiagnostics(diagnostics, report),
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
    await use(cda);

    let freezeError;
    let reportVerificationError;
    let reportAttachmentError;
    try {
      for (const handler of faultHandlers) await page.unroute('**/*', handler);
      await Promise.all([...trackers].map(tracker => tracker.flush()));
      includeBrowserDiagnostics(diagnostics, report);
      report.browserLifecycle = {
        kind: 'official-playwright-page',
        status: testInfo.status,
        durationMs: Math.round(performance.now() - setupStarted),
        diagnosticLimit: MAX_DIAGNOSTICS,
        droppedDiagnostics,
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
      page.removeListener('console', onConsole);
      page.removeListener('request', onRequest);
      page.removeListener('requestfinished', onRequestFinished);
      page.removeListener('pageerror', onPageError);
      page.removeListener('requestfailed', onRequestFailed);
      page.removeListener('response', onResponse);
      page.removeListener('dialog', onUnexpectedDialog);
      if (customDialogHandler) page.removeListener('dialog', customDialogHandler);
      report.network = applyInjectedFaultPolicy(report.network, faultAttempts);
      markExpectedErrors(report);
      for (const failure of diagnostics.networkFailures) delete failure.rawURL;
      if (report.assetFailures.length) recordCheck(report, 'correctness', 'incidental asset failures are explicitly recorded', true,
        { failures: report.assetFailures });
      if (report.requiredChecks.length) finishCdaReport(report);
      else {
        report.missingRequiredChecks = [];
        report.status = report.assertions.some(assertion => assertion.status === 'failed') ? 'failed' : 'unverified';
        report.finishedAt = new Date().toISOString();
      }
      report.runnerStatus = testInfo.status;
      report.explorer = target.explorer;
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
      if ((testInfo.status === 'passed' || runnerSkipped) && !freezeError) {
        reportVerificationError = gateFailure(report);
        if (reportVerificationError) {
          report.errors.push({ kind: 'verification-gate', message: safeText(reportVerificationError.message) });
          recordCheck(report, 'correctness', 'CDA report has all required checks and no unexpected diagnostics', false,
            { message: safeText(reportVerificationError.message) });
          report.status = 'failed';
          report.finishedAt = new Date().toISOString();
        }
      }
      if (report.status === 'failed' && !report.failureEvidence) {
        const currentAction = firstFailureActionContext ?? activeActionContext;
        await captureFailureEvidence({
          reason: testInfo.error?.message ?? freezeError?.message ?? reportVerificationError?.message ??
            report.errors.at(-1)?.message ?? 'CDA Playwright workflow failed outside the action helper',
          label: currentAction?.label ?? activeAction ?? report.activeAction?.label,
          locator: currentAction?.locator ?? activeLocator,
          startedAt: currentAction?.startedAt ?? report.activeAction?.startedAt,
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
    if (reportVerificationError) throw reportVerificationError;
    if (reportAttachmentError && testInfo.status === 'passed') throw reportAttachmentError;
    if (freezeError) throw freezeError;
  },
});
