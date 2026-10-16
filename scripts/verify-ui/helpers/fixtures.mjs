import { test as base, expect } from '@playwright/test';
import { performance } from 'node:perf_hooks';
import {
  matchesOwnedFaultRequest,
  ownedFaultTarget,
} from './network-evidence.mjs';
import { checkContainerApiBuildStamp } from './api-build-freeze.mjs';
import { sanitizePayload, sanitizeText } from './playwright-browser.mjs';
import {
  createRunContext,
  environmentForFixtureDir,
  fixtureExplorerRequestPathMatches,
  fixtureProjectExplorerCollectionPath,
  makeReportLocation,
  scenarioFor,
  validateScenarioCase,
} from './fixture-context.mjs';
import { createReport, finishReport, recordCheck, writeReport } from './report.mjs';
import { scenarioCaseFor } from '../registry.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './source-fingerprint.mjs';
import { captureNativeFailureEvidence, MAX_NATIVE_FAILURE_CAPTURE_MS } from './native-failure-evidence.mjs';
import { createPlaywrightNetworkObserver } from './playwright-network-observer.mjs';
import { canonicalProjectID } from '../../loom-dev.mjs';
import {
  assertFixtureSourceApiFreeze,
  assertNoUnexpectedFixtureFailures,
} from './fixture-failure-assertions.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const CONTEXT_SETUP_TIMEOUT_MS = 120_000;
const safeText = (value) => sanitizeText(value).slice(0, 4_000);

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
    const sanitizedReport = sanitizePayload(report);
    writeReport(location.reportPath, sanitizedReport);
    await testInfo.attach('loom-verification-report.json', {
      path: location.reportPath,
      contentType: 'application/json',
    });
    if (testInfo.status === 'passed') assertFixtureSourceApiFreeze(report.assertions);
  }, { timeout: CONTEXT_SETUP_TIMEOUT_MS }],

  workflow: async ({ loomContext, page }, use, testInfo) => {
    const { report, target } = loomContext;
    const lifecycleStarted = performance.now();
    const workflowStartedAt = lifecycleStarted;
    const ownedOrigins = new Set([target.uiUrl, target.apiUrl].filter(Boolean).map((url) => new URL(url).origin));
    const projectExplorerCollectionPath = fixtureProjectExplorerCollectionPath(target.fixtureProject);
    const selectedExplorer = () => report.target?.explorer ?? report.explorer ?? target.bootstrapExplorerId;
    const scopeRequest = (request) => {
      try {
        const url = new URL(request.url());
        if (url.origin !== new URL(target.uiUrl).origin) return false;
        if (url.pathname === projectExplorerCollectionPath) return true;
        if (request.method() === 'POST' && url.pathname === '/graphql/graph') {
          let input;
          try { input = request.postDataJSON()?.variables?.input; }
          catch { return false; }
          const outputId = report.target?.viewerOutputId;
          return input?.projectId === canonicalProjectID(target.fixtureProject) && typeof outputId === 'string' &&
            input.selector?.output === outputId && Boolean(selectedExplorer());
        }
        const explorer = selectedExplorer();
        return fixtureExplorerRequestPathMatches(url.pathname, target.fixtureProject, explorer);
      } catch { return false; }
    };
    const networkObserver = createPlaywrightNetworkObserver(page, {
      scopeRequest,
      appOrigins: [...ownedOrigins],
      responsePaths: /^\/api\/v1\/projects\/[^/]+\/explorers\/[^/]+(?:\/.*)?$/,
      report,
    });
    const diagnostics = networkObserver.diagnostics;
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
    let actionSequence = 0;
    const faultRouteHandlers = [];
    let activeAction;
    let activeActionContext;
    let firstFailureActionContext;
    let droppedDiagnostics = 0;
    const recordTeardownError = (error) => {
      const message = safeText(error?.message ?? error);
      report.errors ??= [];
      report.errors.push({ kind: 'fixture-teardown', message });
      report.network.push({ kind: 'exception', message });
    };
    const expectTransportFailure = (request, { reason, action } = {}) =>
      networkObserver.markExpectedTransportFailure(request, { reason, action });
    const actionSnapshot = () => {
      if (activeAction) return { id: activeAction.id, label: activeAction.label };
      const workflowAction = report.activeAction;
      const label = workflowAction?.label ?? workflowAction?.name;
      return workflowAction?.id && label ? { id: workflowAction.id, label: safeText(label) } : null;
    };
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
      report.injectedRequests ??= [];
      const attempt = {
        id: `injected-${report.injectedRequests.length + 1}`,
        ...ownedTarget,
        matched: false,
        action: response == null ? 'abort' : 'fulfill',
        responseStatus,
      };
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
        const observed = networkObserver.byRequest.get(request);
        if (!observed) throw new Error('Injected fault request was outside the shared observer scope.');
        attempt.matched = true;
        attempt.browserRequestId = observed.browserRequestId;
        evidence.matched = true;
        evidence.browserRequestId = observed.browserRequestId;
        evidence.url = observed.url;
        evidence.request = { method: observed.method, path: observed.path };
        if (response == null) {
          expectTransportFailure(request, {
            reason: `fixture fault ${attempt.id} intentionally aborted this request`,
            action: actionSnapshot()?.label ?? attempt.id,
          });
          await route.abort();
        }
        else {
          await route.fulfill(response);
          if ([400, 422].includes(responseStatus)) {
            const observed = networkObserver.byRequest.get(request);
            if (!observed) throw new Error('Injected validation request was outside the shared observer scope.');
            const completed = await networkObserver.waitForResponse(entry => entry === observed, { fromIndex: observed.index });
            networkObserver.markExpectedValidation(completed, {
              status: responseStatus,
              reason: `fixture fault ${attempt.id} intentionally returned an HTTP validation response`,
              proof: evidence,
            });
          }
        }
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
      await use({ ...loomContext, diagnostics, page, check, action, fault, expectTransportFailure, networkObserver });
    } finally {
      try {
        for (const handler of faultRouteHandlers) await page.unroute('**/*', handler);
      } catch (error) {
        recordTeardownError(error);
      } finally {
        try { await networkObserver.stop(); }
        catch (error) { recordTeardownError(error); }
      }
      report.assetFailures = [...diagnostics.assetFailures];
      const diagnosticChannels = ['console', 'pageErrors', 'networkFailures', 'httpFailures', 'assetFailures'];
      const retainedCounts = Object.fromEntries(diagnosticChannels.map(channel => [channel, diagnostics[channel].length]));
      const droppedCounts = diagnostics.overflow ? { ...diagnostics.overflow } : {};
      droppedDiagnostics = Object.values(droppedCounts).reduce((total, count) => total + count, 0);
      report.browserDiagnostics = {
        source: 'playwright-network-observer',
        retainedCounts,
        droppedCounts,
        captureComplete: droppedDiagnostics === 0,
      };
      if (report.assetFailures.length) recordCheck(report, 'correctness', 'incidental asset failures are explicitly recorded', true, { failures: report.assetFailures });
      report.browserLifecycle = {
        kind: 'official-playwright-page',
        status: testInfo.status,
        durationMs: Math.round(performance.now() - lifecycleStarted),
        droppedDiagnostics,
        observedRequestCount: networkObserver.requests.length,
      };
      const observerFailures = networkObserver.failures.map(failure => ({
        kind: failure.kind,
        message: failure.message ?? failure.errorText ?? failure.entry?.failure?.errorText,
        url: failure.url ?? failure.entry?.url,
        method: failure.entry?.method,
        path: failure.entry?.path,
      }));
      const fixtureErrors = report.errors.filter(entry => ['fixture-audit-error', 'fixture-teardown'].includes(entry.kind));
      const noUnexpectedFailures = observerFailures.length === 0 && fixtureErrors.length === 0;
      recordCheck(report, 'correctness', 'fixture browser observer reported no unexpected network or runtime errors',
        noUnexpectedFailures, { observerFailures, fixtureErrors });
      const firstFailurePending = testInfo.status !== 'passed' ||
        report.assertions.some(assertion => assertion.status === 'failed') ||
        !noUnexpectedFailures;
      if (firstFailurePending && !report.failureEvidence) {
        const failureAction = firstFailureActionContext ?? activeActionContext;
        await captureFailureEvidence({
          reason: testInfo.error?.message ?? observerFailures[0]?.message ?? fixtureErrors[0]?.message ??
            'Playwright workflow failed outside the action helper',
          label: failureAction?.label,
          locator: failureAction?.locator,
          startedAt: failureAction?.startedAt,
        });
      }
      if (testInfo.status === 'passed') {
        assertNoUnexpectedFixtureFailures({
          observerFailures,
          reportErrors: [],
          fixtureErrors,
        });
      }
    }
  },
});
