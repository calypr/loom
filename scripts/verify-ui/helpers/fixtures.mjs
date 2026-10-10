import { test as base, expect } from '@playwright/test';
import { performance } from 'node:perf_hooks';
import {
  captureInjectedFaultRequest,
  capabilityBinding,
  capabilityResponseMatches,
  isIncidentalFavicon,
  matchesOwnedFaultRequest,
  ownedFaultTarget,
  supersedingCapabilityRequest,
} from './network-evidence.mjs';
import { checkContainerApiBuildStamp } from './api-build-freeze.mjs';
import { sanitizeBody, sanitizePayload, sanitizeText } from './playwright-browser.mjs';
import {
  createRunContext,
  environmentForFixtureDir,
  makeReportLocation,
  scenarioFor,
  validateScenarioCase,
} from './fixture-context.mjs';
import { adjudicatePendingLifecycle, classifyNetworkRecord, createReport, finishReport, recordCheck, writeReport } from './report.mjs';
import { scenarioCaseFor } from '../registry.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './source-fingerprint.mjs';
import { captureNativeFailureEvidence, MAX_NATIVE_FAILURE_CAPTURE_MS } from './native-failure-evidence.mjs';
import { correlateRequestFailure } from './network-timing.mjs';
import { captureConstructionChoiceProposalRequest } from './construction-choice-request.mjs';
import { createPendingResponseReads } from './pending-response-reads.mjs';
import { includeBrowserDiagnostics } from './cda-playwright.mjs';
import {
  createFixtureNativeRequestLedger,
  finalizeFixtureNativeRequestReport,
  openBasicFixtureNativeRequestScope,
  projectFixtureNetworkDiagnostics,
} from './native-request-ledger.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const CONTEXT_SETUP_TIMEOUT_MS = 120_000;
const MAX_DIAGNOSTICS = 100;
const safeText = (value) => sanitizeText(value).slice(0, 4_000);

export function createFixtureBrowserDiagnostics(report) {
  if (!report || typeof report !== 'object') {
    throw new TypeError('Fixture browser diagnostics require a report object.');
  }
  const channels = ['pageErrors', 'console', 'networkFailures', 'httpFailures', 'assetFailures'];
  const diagnostics = Object.fromEntries(channels.map(channel => [channel, []]));
  const droppedCounts = Object.fromEntries(channels.map(channel => [channel, 0]));
  diagnostics.droppedCounts = droppedCounts;
  const responseReads = createPendingResponseReads();
  let overflowError;
  const reportCaptureCounts = () => {
    const retainedCounts = Object.fromEntries(channels.map(channel => [channel, diagnostics[channel].length]));
    report.browserDiagnostics = { retainedCounts, droppedCounts: { ...droppedCounts } };
    if (Object.values(droppedCounts).some(count => count > 0)) {
      report.errors ??= [];
      if (!overflowError) {
        overflowError = {
          kind: 'browser-diagnostic-overflow',
          message: 'Fixture browser diagnostics exceeded a per-channel capture limit.',
          droppedCounts: { ...droppedCounts },
        };
        report.errors.push(overflowError);
        recordCheck(report, 'correctness', 'fixture browser diagnostic channels stayed within capture limits', false,
          { retainedCounts, droppedCounts: { ...droppedCounts }, limitPerChannel: MAX_DIAGNOSTICS });
      } else {
        overflowError.droppedCounts = { ...droppedCounts };
        const assertion = report.assertions?.find(item => item.name === 'fixture browser diagnostic channels stayed within capture limits'
          && item.status === 'failed');
        if (assertion) assertion.evidence = { retainedCounts, droppedCounts: { ...droppedCounts }, limitPerChannel: MAX_DIAGNOSTICS };
      }
    }
  };
  const project = () => {
    includeBrowserDiagnostics(diagnostics, report);
    reportCaptureCounts();
  };
  const flushHttpDiagnostics = ({ timeoutMs = 5_000 } = {}) =>
    responseReads.flush({ timeoutMs, label: 'fixture HTTP diagnostic bodies' });
  return {
    diagnostics,
    record(channel, entry) {
      const entries = diagnostics[channel];
      if (!Array.isArray(entries)) throw new TypeError(`Unknown fixture browser diagnostic channel: ${channel}`);
      if (entries.length >= MAX_DIAGNOSTICS) {
        droppedCounts[channel] += 1;
        return false;
      }
      entries.push(entry);
      return true;
    },
    trackHttpDiagnosticRead(read, details) {
      return responseReads.track(read, { phase: 'fixture-http-response-body', ...details });
    },
    flushHttpDiagnostics,
    includeBrowserDiagnostics() {
      project();
    },
    async finalize({ timeoutMs = 5_000 } = {}) {
      let drainError;
      try {
        await flushHttpDiagnostics({ timeoutMs });
      } catch (error) {
        drainError = safeText(error?.message ?? error);
        report.errors ??= [];
        report.errors.push({ kind: 'browser-diagnostic-drain', message: drainError });
        recordCheck(report, 'correctness', 'fixture HTTP response diagnostics drained before report finalization', false,
          { message: drainError, timeoutMs });
      }
      reportCaptureCounts();
      if (!drainError) {
        recordCheck(report, 'correctness', 'fixture HTTP response diagnostics drained before report finalization', true,
          { timeoutMs, retainedHttpDiagnostics: diagnostics.httpFailures.length });
      }
      return { drainError, droppedCounts: { ...droppedCounts } };
    },
  };
}

const safeURL = (raw) => {
  try {
    const url = new URL(raw);
    return safeText(`${url.origin}${url.pathname}`);
  } catch {
    return safeText(raw);
  }
};

const requestDiagnostic = (request) => {
  let body;
  try { body = request.postDataJSON(); } catch { body = undefined; }
  const input = body?.variables?.input ?? body ?? {};
  const headers = request.headers();
  const constructionChoiceProposal = captureConstructionChoiceProposalRequest(request, body);
  let previewReceiptId;
  try {
    if (new URL(request.url()).pathname.endsWith('/authoring/v2/preview')) previewReceiptId = input.receiptId ?? null;
  } catch {}
  return sanitizePayload({
    requestId: headers['x-request-id'] ?? body?.requestId ?? body?.requestID ?? null,
    draftVersion: input.expectedDraftVersion ?? input.draftVersion ?? null,
    draftDigest: input.expectedDraftDigest ?? input.draftDigest ?? null,
    outputId: input.outputId ?? null,
    stageId: input.stageId ?? null,
    ...(constructionChoiceProposal ? { constructionChoiceProposal } : {}),
    ...(previewReceiptId === undefined ? {} : { receiptId: previewReceiptId }),
  });
};

const apiIdentity = async (target) => {
  if (!target.composeProject) return { identity: null, error: 'owned API container is unavailable' };
  const observation = await checkContainerApiBuildStamp(`${target.composeProject}-loom-api-1`);
  const stamp = /^([a-f0-9]{64})\s+([a-f0-9]{64})\s+([a-f0-9]{64})$/i.exec(observation.stdout.trim());
  if (observation.status !== 0 || !stamp) {
    return {
      identity: null,
      error: `API build identity check failed (status=${observation.status ?? 'unknown'}${observation.errorCode ? `, code=${observation.errorCode}` : ''})`,
    };
  }
  return { identity: stamp.slice(1).join(':').toLowerCase() };
};

const safeTarget = (target) => ({
  kind: target.kind ?? 'owned-dev-fixture',
  uiUrl: new URL(target.uiUrl).origin,
  sourceRoot: target.sourceRoot ?? null,
  project: target.fixtureProject,
  generation: target.fixtureGeneration,
});

const recordAuditFailure = (report, name, error) => {
  const message = safeText(error?.message ?? error);
  report.errors.push({ kind: 'fixture-audit-error', message });
  recordCheck(report, 'correctness', name, false, { message });
};

export { expect };
export const test = base.extend({
  scenarioID: ['builder-combine', { option: true }],
  caseName: ['append', { option: true }],
  fixtureDir: [undefined, { option: true }],
  fixtureGeneration: [undefined, { option: true }],

  loomContext: [async ({ scenarioID, caseName, fixtureDir, fixtureGeneration }, use, testInfo) => {
    const setupStarted = performance.now();
    const scenario = scenarioFor(scenarioID);
    const caseContract = validateScenarioCase(scenario, caseName);
    const args = { caseName };
    const contextStarted = performance.now();
    const env = environmentForFixtureDir(process.env, fixtureDir, fixtureGeneration);
    const context = await createRunContext(args, scenario, { env });
    const contextSetupMs = performance.now() - contextStarted;
    const location = makeReportLocation(context, scenarioID, caseName);
    const report = createReport({
      scenario: scenarioID,
      caseName,
      target: safeTarget(context.target),
      evidenceDirectory: location.evidenceDirectory,
      requiredChecks: context.custom ? scenarioCaseFor(scenario, caseName, true).requiredChecks : caseContract.requiredChecks,
    });
    report.registryCoverage = scenario.coverage;
    report.fixtureTimings = { createRunContextMs: contextSetupMs };

    let sourceAtStart;
    const fingerprintStarted = performance.now();
    try {
      sourceAtStart = sourceFingerprintWithManifest(context.target.sourceRoot);
      report.target.sourceFingerprint = sourceAtStart.fingerprint;
      report.sourceFingerprintManifest = { before: sourceAtStart.manifest };
      recordCheck(report, 'correctness', 'watched source fingerprint baseline captured before browser lifecycle', true,
        { fingerprint: sourceAtStart.fingerprint });
    } catch (error) {
      recordAuditFailure(report, 'watched source fingerprint baseline captured before browser lifecycle', error);
    }
    report.fixtureTimings.sourceFingerprintBeforeMs = performance.now() - fingerprintStarted;

    const apiStarted = performance.now();
    let apiAtStart;
    try {
      apiAtStart = await apiIdentity(context.target);
      report.target.apiBuildIdentity = apiAtStart.identity;
      if (!apiAtStart.identity) throw new Error(apiAtStart.error);
      recordCheck(report, 'correctness', 'API build identity baseline captured before browser lifecycle', true,
        { identity: apiAtStart.identity });
    } catch (error) {
      recordAuditFailure(report, 'API build identity baseline captured before browser lifecycle', error);
    }
    report.fixtureTimings.apiBuildIdentityBeforeMs = performance.now() - apiStarted;
    report.fixtureTimings.totalSetupMs = performance.now() - setupStarted;

    if (!sourceAtStart || !apiAtStart?.identity) {
      finishReport(report);
      writeReport(location.reportPath, sanitizePayload(report));
      await testInfo.attach('loom-verification-report.json', { path: location.reportPath, contentType: 'application/json' });
      throw new Error('Loom fixture source/API baseline could not be established; browser workflow was not executed.');
    }
    const fixture = { ...context, scenarioID, caseName, report, reportPath: location.reportPath, sourceAtStart, apiAtStart };
    await use(fixture);

    if (testInfo.status !== 'passed') {
      report.errors.push({ kind: 'playwright-test', message: `official test finished with status ${testInfo.status}` });
      recordCheck(report, 'correctness', 'official Playwright case completed without failure', false,
        { status: testInfo.status });
    }

    if (sourceAtStart) {
      const fingerprintAfterStarted = performance.now();
      try {
        const sourceAtEnd = sourceFingerprintWithManifest(context.target.sourceRoot);
        const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
        report.target.sourceFingerprintAfter = sourceAtEnd.fingerprint;
        report.sourceFingerprintManifest = {
          before: sourceAtStart.manifest,
          after: sourceAtEnd.manifest,
          changedPaths,
        };
        recordCheck(report, 'correctness', 'watched source stayed unchanged during browser run',
          sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256,
          { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths });
      } catch (error) {
        recordAuditFailure(report, 'watched source stayed unchanged during browser run', error);
      }
      report.fixtureTimings.sourceFingerprintAfterMs = performance.now() - fingerprintAfterStarted;
    } else {
      recordCheck(report, 'correctness', 'watched source stayed unchanged during browser run', false,
        { reason: 'source fingerprint baseline was unavailable' });
    }

    const apiAfterStarted = performance.now();
    try {
      const apiAtEnd = await apiIdentity(context.target);
      report.apiBuildFreeze = { before: apiAtStart?.identity ?? null, after: apiAtEnd.identity };
      report.target.apiBuildIdentityAfter = apiAtEnd.identity;
      recordCheck(report, 'correctness', 'API build identity stayed unchanged during browser run',
        Boolean(apiAtStart?.identity && apiAtEnd.identity && apiAtStart.identity === apiAtEnd.identity),
        { before: apiAtStart?.identity ?? null, after: apiAtEnd.identity, error: apiAtEnd.error });
    } catch (error) {
      recordAuditFailure(report, 'API build identity stayed unchanged during browser run', error);
    }
    report.fixtureTimings.apiBuildIdentityAfterMs = performance.now() - apiAfterStarted;

    finishReport(report);
    adjudicatePendingLifecycle(report);
    const sanitizedReport = sanitizePayload(report);
    writeReport(location.reportPath, sanitizedReport);
    await testInfo.attach('loom-verification-report.json', {
      path: location.reportPath,
      contentType: 'application/json',
    });
    if (testInfo.status === 'passed') {
      expect(report.missingRequiredChecks, 'required Loom workflow checks were not recorded as passed').toEqual([]);
      expect(report.status, 'Loom domain report did not pass').toBe('passed');
    }
  }, { timeout: CONTEXT_SETUP_TIMEOUT_MS }],

  workflow: async ({ loomContext, page }, use, testInfo) => {
    const { report, target } = loomContext;
    const lifecycleStarted = performance.now();
    const workflowStartedAt = lifecycleStarted;
    const ownedOrigins = new Set([target.uiUrl, target.apiUrl].filter(Boolean).map((url) => new URL(url).origin));
    const captureFailureEvidence = async details => {
      if (report.failureEvidence) return;
      try {
        report.failureEvidence = await captureNativeFailureEvidence({ page, ownedOrigins, ...details });
      } catch (error) {
        report.failureEvidence = {
          reason: safeText(details.reason ?? 'Playwright workflow failed'),
          captureError: safeText(error?.message ?? error),
        };
      }
    };
    const capabilityRequests = new Map();
    const requestIDs = new WeakMap();
    const requestMetadata = new WeakMap();
    const nativeRequestLedger = createFixtureNativeRequestLedger();
    openBasicFixtureNativeRequestScope(nativeRequestLedger, target);
    const browserDiagnostics = createFixtureBrowserDiagnostics(report);
    const mainFrameNavigations = [];
    let navigationSequence = 0;
    let droppedNavigationTimings = 0;
    let actionSequence = 0;
    const faultAttempts = [];
    const faultRouteHandlers = [];
    let requestSequence = 0;
    let requestIDSequence = 0;
    let activeAction;
    let activeActionContext;
    let firstFailureActionContext;
    let retainedDiagnostics = 0;
    let droppedDiagnostics = 0;
    const addDiagnostic = (entry) => {
      if (retainedDiagnostics < MAX_DIAGNOSTICS) {
        report.network.push(entry);
        retainedDiagnostics += 1;
        return true;
      } else {
        droppedDiagnostics += 1;
        return false;
      }
    };
    const belongsToTarget = (request) => {
      try { return ownedOrigins.has(new URL(request.url()).origin); }
      catch { return false; }
    };
    const playwrightRequestId = (request) => {
      let id = requestIDs.get(request);
      if (!id) {
        id = `request-${++requestIDSequence}`;
        requestIDs.set(request, id);
      }
      return id;
    };

    const actionSnapshot = () => {
      if (activeAction) return { id: activeAction.id, label: activeAction.label };
      const workflowAction = report.activeAction;
      const label = workflowAction?.label ?? workflowAction?.name;
      return workflowAction?.id && label ? { id: workflowAction.id, label: safeText(label) } : null;
    };
    const onRequest = request => {
      const requestId = playwrightRequestId(request);
      const frameIdentity = nativeRequestLedger.frameIdentityForRequest(request, page);
      if (belongsToTarget(request) && request.isNavigationRequest?.() && frameIdentity.frameIsMainFrame === true) {
        const startedAt = performance.now();
        const sequence = ++navigationSequence;
        if (mainFrameNavigations.length < MAX_DIAGNOSTICS) {
          mainFrameNavigations.push({ id: `navigation-${sequence}`, sequence, startedAt,
            atMs: Math.round(startedAt - workflowStartedAt), url: safeURL(request.url()), phase: 'request-start',
            ...frameIdentity, browserRequestId: requestId });
        } else droppedNavigationTimings += 1;
      }
      if (belongsToTarget(request)) {
        const startedAt = performance.now();
        const metadata = {
          requestId,
          method: request.method(),
          resourceType: request.resourceType(),
          url: safeURL(request.url()),
          requestDetails: requestDiagnostic(request),
          startedAt,
          startedMs: Math.round(startedAt - workflowStartedAt),
          action: actionSnapshot(),
          navigationSequenceAtStart: navigationSequence,
          ...frameIdentity,
        };
        requestMetadata.set(request, metadata);
        nativeRequestLedger.recordRequest(request, {
          requestId: metadata.requestDetails.requestId ?? requestId,
          browserRequestId: requestId,
          method: metadata.method,
          resourceType: metadata.resourceType,
          url: metadata.url,
          requestDetails: metadata.requestDetails,
          startedAt: Date.now(),
          action: metadata.action,
          navigationSequenceAtStart: metadata.navigationSequenceAtStart,
          pageId: metadata.pageId,
          frameId: metadata.frameId,
          frameIsMainFrame: metadata.frameIsMainFrame,
          frameIdentityStatus: metadata.frameIdentityStatus,
        });
      }
      const binding = capabilityBinding(request, { ...target, explorer: report.target.explorer ?? target.bootstrapExplorerId });
      if (binding) capabilityRequests.set(request, { binding, sequence: ++requestSequence, status: null, requestAction: actionSnapshot()?.label ?? null });
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
      mainFrameNavigations.push({
        id: `navigation-${sequence}`,
        sequence,
        startedAt,
        atMs: Math.round(startedAt - workflowStartedAt),
        url: safeURL(frame.url()),
        ...nativeRequestLedger.frameIdentityForFrame(frame, page),
        phase: 'frame-navigated',
      });
    };
    const onConsole = (message) => {
      if (message.type() !== 'error') return;
      if (isIncidentalFavicon(message.location().url, target, 404) && message.text().includes('404')) {
        const failure = { kind: 'asset-failure', url: safeURL(message.location().url), status: 404 };
        if (browserDiagnostics.record('assetFailures', failure)) report.assetFailures.push(failure);
        return;
      }
      browserDiagnostics.record('console', { text: safeText(message.text()), location: safeURL(message.location().url) });
      addDiagnostic({
        kind: 'console-error',
        text: safeText(message.text()),
        location: safeURL(message.location().url),
        rawLocation: message.location().url,
      });
    };
    const onPageError = (error) => {
      const failure = { message: safeText(error.message), stack: safeText(error.stack) };
      browserDiagnostics.record('pageErrors', failure);
      addDiagnostic({ kind: 'exception', ...failure });
    };
    const onResponse = (response) => {
      nativeRequestLedger.recordResponse(response.request(), {
        status: response.status(),
        serverRequestId: response.headers()?.['x-request-id'],
      });
      const ownedRequest = capabilityRequests.get(response.request());
      if (ownedRequest) {
        ownedRequest.status = response.status();
        if (response.ok()) void response.json().then(body => {
          ownedRequest.responseMatches = capabilityResponseMatches(ownedRequest.binding, body);
        }, error => { ownedRequest.responseReadError = safeText(error.message); });
      }
      if (response.status() < 400 || !belongsToTarget(response.request())) return;
      if (isIncidentalFavicon(response.url(), target, response.status())) {
        const failure = { kind: 'asset-failure', url: safeURL(response.url()), status: 404 };
        if (browserDiagnostics.record('assetFailures', failure)) report.assetFailures.push(failure);
        return;
      }
      const request = response.request();
      const responseBody = { captureState: 'pending' };
      const entry = {
        kind: 'network', status: response.status(), method: response.request().method(),
        url: safeURL(response.url()), resourceType: response.request().resourceType(),
        rawURL: response.url(),
        playwrightRequestId: playwrightRequestId(request),
        requestDetails: requestMetadata.get(request)?.requestDetails ?? requestDiagnostic(request),
        ...(requestMetadata.has(request) ? { requestTimeline: correlateRequestFailure({
          requestStartedAt: requestMetadata.get(request).startedAt, failedAt: performance.now(), workflowStartedAt,
          action: requestMetadata.get(request).action,
          navigationSequenceAtStart: requestMetadata.get(request).navigationSequenceAtStart,
          navigations: mainFrameNavigations,
        }) } : {}),
        responseBody,
      };
      nativeRequestLedger.associateDiagnostic(request, entry);
      if (!addDiagnostic(entry)) return;
      const read = Promise.resolve().then(() => response.text()).then(body => {
        responseBody.captureState = 'completed';
        responseBody.body = sanitizeBody(body);
        browserDiagnostics.record('httpFailures', { url: entry.url, status: entry.status, body: responseBody.body,
          browserRequestId: entry.playwrightRequestId, playwrightRequestId: entry.playwrightRequestId });
      }, error => {
        responseBody.captureState = 'readfailed';
        responseBody.error = safeText(error?.message ?? error);
        browserDiagnostics.record('httpFailures', { url: entry.url, status: entry.status, body: responseBody,
          browserRequestId: entry.playwrightRequestId, playwrightRequestId: entry.playwrightRequestId });
        throw error;
      });
      browserDiagnostics.trackHttpDiagnosticRead(read, {
        browserRequestId: entry.playwrightRequestId,
        requestId: entry.requestDetails.requestId,
        method: entry.method,
        path: new URL(entry.rawURL).pathname,
        status: entry.status,
      });
    };
    const onRequestFailed = (request) => {
      const rawErrorText = request.failure()?.errorText ?? null;
      nativeRequestLedger.recordFailed(request, {
        failure: rawErrorText == null ? null : safeText(rawErrorText).replace(/https?:\/\/[^\s"'<>]+/g, value => safeURL(value)),
      });
      if (!belongsToTarget(request)) return;
      const ownedRequest = capabilityRequests.get(request);
      if (ownedRequest) ownedRequest.failed = true;
      const failedAt = performance.now();
      const metadata = requestMetadata.get(request);
      const errorText = safeText(request.failure()?.errorText).replace(/https?:\/\/[^\s\"'<>]+/g, value => safeURL(value));
      const entry = {
        kind: 'network', method: request.method(), url: metadata?.url ?? safeURL(request.url()),
        resourceType: request.resourceType(), errorText,
        rawURL: request.url(),
        playwrightRequestId: metadata?.requestId ?? playwrightRequestId(request),
        requestDetails: metadata?.requestDetails ?? requestDiagnostic(request),
        ...(metadata ? { requestTimeline: correlateRequestFailure({
          requestStartedAt: metadata.startedAt, failedAt, workflowStartedAt,
          action: metadata.action,
          navigationSequenceAtStart: metadata.navigationSequenceAtStart,
          navigations: mainFrameNavigations,
        }) } : { failedAtMs: Math.round(failedAt - workflowStartedAt) }),
        ...capabilityRequests.get(request), triggerAction: metadata?.action?.label ?? null,
      };
      browserDiagnostics.record('networkFailures', { url: entry.url, errorText: entry.errorText,
        browserRequestId: entry.playwrightRequestId, playwrightRequestId: entry.playwrightRequestId });
      nativeRequestLedger.associateDiagnostic(request, entry);
      addDiagnostic(entry);
    };
    const onRequestFinished = request => {
      nativeRequestLedger.recordFinished(request);
      const ownedRequest = capabilityRequests.get(request);
      if (ownedRequest) ownedRequest.finished = true;
    };
    page.on('requestfinished', onRequestFinished);
    page.on('framenavigated', onFrameNavigated);
    page.on('request', onRequest);
    page.on('console', onConsole);
    page.on('pageerror', onPageError);
    page.on('response', onResponse);
    page.on('requestfailed', onRequestFailed);

    const check = (dimension, name, passed, evidence = {}) => {
      const success = Boolean(passed);
      recordCheck(report, dimension, name, success, evidence);
      expect(success, name).toBe(true);
      return success;
    };

    const action = async (label, locator, perform, {
      after,
      timeout = ACTION_TIMEOUT_MS,
      budget = ACTION_TIMEOUT_MS,
      editable = false,
      requiredCheck,
    } = {}) => test.step(label, async () => {
      const actionID = `action-${++actionSequence}`;
      activeAction = { id: actionID, label: safeText(label) };
      const started = performance.now();
      const startedMs = Math.round(started - workflowStartedAt);
      const startedAt = Date.now();
      const actionContext = { id: actionID, label, locator, startedAt, startedMs };
      activeActionContext = actionContext;
      const requestedTimeout = Number.isFinite(timeout) ? timeout : ACTION_TIMEOUT_MS;
      const actionTimeout = Math.max(1, Math.min(ACTION_TIMEOUT_MS, requestedTimeout));
      const requestedBudget = Number.isFinite(budget) ? budget : ACTION_TIMEOUT_MS;
      const budgetMs = Math.max(1, Math.min(ACTION_TIMEOUT_MS, requestedBudget));
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
        const ended = performance.now();
        report.actions.push({
          id: actionID, startedAtMs: startedMs, endedAtMs: Math.round(ended - workflowStartedAt),
          startedAtEpochMs: startedAt, finishedAtEpochMs: Date.now(),
          label: safeText(label), status: passed ? 'passed' : 'failed', elapsedMs: Math.round(elapsedMs),
          locator: safeText(locator.toString()), ...(afterMs === undefined ? {} : { afterMs: Math.round(afterMs) }),
        });
        report.timings[label] = Math.round(elapsedMs);
        recordCheck(report, 'usability', `${label} completed`, passed,
          { elapsedMs: Math.round(elapsedMs), afterMs: afterMs === undefined ? null : Math.round(afterMs) });
        if (after) {
          recordCheck(report, 'performance', requiredCheck ?? `${label} action-to-render within budget`,
            passed, { elapsedMs: Math.round(elapsedMs), afterMs: afterMs === undefined ? null : Math.round(afterMs), budgetMs });
        }
        if (activeActionContext === actionContext) activeActionContext = undefined;
        if (activeAction?.id === actionID) activeAction = undefined;
      }
    }, { timeout: ACTION_TIMEOUT_MS });

    const fault = async ({ method, path, matchesRequest, response }) => {
      const ownedTarget = ownedFaultTarget(target, { method, path });
      const responseStatus = response == null ? null :
        (typeof response.status === 'function' ? response.status() : (response.status ?? 200));
      const attempt = {
        id: `injected-${faultAttempts.length + 1}`,
        ...ownedTarget,
        matched: false,
        action: response == null ? 'abort' : 'fulfill',
        responseStatus,
      };
      faultAttempts.push(attempt);
      report.injectedRequests ??= [];
      const evidence = {
        id: attempt.id,
        matched: false,
        origin: ownedTarget.origin,
        path: ownedTarget.path,
        method: ownedTarget.method,
        action: attempt.action,
        configuredResponseStatus: responseStatus,
      };
      report.injectedRequests.push(evidence);

      const handler = async (route) => {
        const request = route.request();
        if (attempt.matched || !matchesOwnedFaultRequest(request, ownedTarget, matchesRequest)) {
          await route.fallback();
          return;
        }
        captureInjectedFaultRequest(attempt, request, playwrightRequestId(request));
        evidence.matched = true;
        evidence.playwrightRequestId = attempt.playwrightRequestId;
        evidence.url = safeURL(request.url());
        evidence.request = requestDiagnostic(request);
        if (response == null) await route.abort();
        else await route.fulfill(response);
      };
      await page.route('**/*', handler);
      faultRouteHandlers.push(handler);
      return {
        id: attempt.id,
        path: attempt.path,
        count: () => Number(attempt.matched),
        evidence,
      };
    };

    try {
      await use({ ...loomContext, ...browserDiagnostics, page, check, action, fault, nativeRequestLedger });
    } finally {
      try {
        try {
          for (const handler of faultRouteHandlers) await page.unroute('**/*', handler);
        } finally {
          await nativeRequestLedger.finalizeScope({
            project: target.fixtureProject,
            explorer: report.target?.explorer ?? report.explorer,
            timeoutMs: 5_000,
          });
        }
      } finally {
        page.removeListener('requestfinished', onRequestFinished);
        page.removeListener('framenavigated', onFrameNavigated);
        page.removeListener('request', onRequest);
        page.removeListener('console', onConsole);
        page.removeListener('pageerror', onPageError);
        page.removeListener('response', onResponse);
        page.removeListener('requestfailed', onRequestFailed);
      }
      await browserDiagnostics.finalize({ timeoutMs: 5_000 });
      projectFixtureNetworkDiagnostics({ report, ledger: nativeRequestLedger, faults: faultAttempts });
      for (const failure of report.network) {
        const replacement = supersedingCapabilityRequest(failure, [...capabilityRequests.values()]);
        if (!replacement) continue;
        failure.canceled = true;
        failure.cancellationReason = 'superseded capability binding has a later successful replacement';
        failure.replacement = { sequence: replacement.sequence, status: replacement.status, finished: replacement.finished, responseMatches: replacement.responseMatches, binding: replacement.binding };
      }
      report.navigationTimings = mainFrameNavigations.map(({ id, sequence, atMs, url, phase, pageId, frameId,
        frameIsMainFrame, frameIdentityStatus, browserRequestId }) => ({
        id, sequence, atMs, url, phase, pageId, frameId, frameIsMainFrame, frameIdentityStatus,
        ...(browserRequestId ? { browserRequestId } : {}),
      }));
      await finalizeFixtureNativeRequestReport({
        report,
        ledger: nativeRequestLedger,
        project: target.fixtureProject,
        explorer: report.target?.explorer ?? report.explorer,
        timeoutMs: 5_000,
      });
      if (report.assetFailures.length) recordCheck(report, 'correctness', 'incidental asset failures are explicitly recorded', true, { failures: report.assetFailures });
      report.browserLifecycle = {
        kind: 'official-playwright-page',
        status: testInfo.status,
        durationMs: Math.round(performance.now() - lifecycleStarted),
        diagnosticLimit: MAX_DIAGNOSTICS,
        droppedDiagnostics,
        droppedNavigationTimings,
      };
      if (droppedDiagnostics > 0) {
        report.network.push({ kind: 'exception', message: `diagnostic limit exceeded; ${droppedDiagnostics} events omitted` });
      }
      const missingRequiredChecks = report.requiredChecks.filter(name =>
        !report.assertions.some(assertion => assertion.name === name && assertion.status === 'passed'));
      const firstFailurePending = testInfo.status !== 'passed' ||
        report.assertions.some(assertion => assertion.status === 'failed') ||
        missingRequiredChecks.length > 0 || droppedDiagnostics > 0 ||
        report.network.some(entry => classifyNetworkRecord(entry) === 'unexpected-error');
      if (firstFailurePending && !report.failureEvidence) {
        const failureAction = firstFailureActionContext ?? activeActionContext;
        await captureFailureEvidence({
          reason: testInfo.error?.message ?? 'Playwright workflow failed outside the action helper',
          label: failureAction?.label,
          locator: failureAction?.locator,
          startedAt: failureAction?.startedAt,
        });
      }
    }
  },
});
