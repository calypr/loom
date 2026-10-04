import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { launchBrowser, sanitizeText } from '../lib/playwright-browser.mjs';
import { makeReportLocation, scenarioFor } from './cli.mjs';
import { createReport, finishReport, recordCheck, recordUntested, writeReport } from './report.mjs';
import { requiredChecksFor } from './registry.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './source-fingerprint.mjs';

const apiBuildIdentity = target => {
  const stdout = execFileSync('docker', [
    'exec', `${target.composeProject}-loom-api-1`, '/workspace/loom-dev-build-stamp.sh', '--check',
  ], { encoding: 'utf8', timeout: 10000 }).trim();
  assert.match(stdout, /^[a-f0-9]{64}\s+[a-f0-9]{64}\s+[a-f0-9]{64}$/i);
  return stdout.split(/\s+/).join(':').toLowerCase();
};

const safeTarget = target => ({
  kind: target.kind ?? 'owned-dev-fixture',
  uiUrl: new URL(target.uiUrl).origin,
  sourceRoot: target.sourceRoot,
  project: target.fixtureProject,
  generation: target.fixtureGeneration,
});

const pathAndMethod = request => {
  const url = new URL(request.url());
  return { method: request.method(), origin: url.origin, pathname: url.pathname };
};

export const ownedBrowserRequestTarget = (target, { method, path }) => ({
  origin: new URL(target.uiUrl).origin,
  path,
  method,
});

export const matchesViewerRequestBody = (body, { project, selector }) => {
  const input = body?.variables?.input;
  return input?.projectId === project && input.selector?.recipe === selector?.recipe &&
    input.selector?.translationVersion === selector?.translationVersion &&
    input.selector?.output === selector?.output;
};

const isLoopback = origin => {
  try { return ['127.0.0.1', 'localhost', '::1'].includes(new URL(origin).hostname); }
  catch { return false; }
};

export const diagnosticsToNetwork = (diagnostics, injectedTarget) => {
  let injectedFailureConsumed = false;
  const matchesInjectedPath = value => {
    try {
      const url = new URL(value);
      return url.origin === injectedTarget?.origin && url.pathname === injectedTarget?.path;
    }
    catch { return false; }
  };
  const markInjected = item => {
    const matches = !injectedFailureConsumed && item.method === injectedTarget?.method && matchesInjectedPath(item.url);
    if (matches) injectedFailureConsumed = true;
    return matches;
  };
  const entries = [];
  const injectedNetworkFailure = diagnostics.networkFailures.find(item =>
    item.method === injectedTarget?.method && matchesInjectedPath(item.url));
  let injectedConsoleConsumed = false;
  for (const item of diagnostics.console) {
    const matchesInjectedConsole = !injectedConsoleConsumed && injectedNetworkFailure &&
      item.location === injectedNetworkFailure.url && item.text === 'Failed to load resource: net::ERR_FAILED';
    if (matchesInjectedConsole) injectedConsoleConsumed = true;
    entries.push(matchesInjectedConsole
      ? { kind: 'network', observedAs: 'console-error', ...item, errorText: 'net::ERR_FAILED', injectedFault: true }
      : { kind: 'console-error', ...item });
  }
  for (const item of diagnostics.pageErrors) entries.push({ kind: 'exception', ...item });
  for (const item of diagnostics.networkFailures) {
    const fault = markInjected(item);
    entries.push({ kind: 'network', ...item, errorText: item.failure, ...(fault ? { injectedFault: true } : {}) });
  }
  for (const item of diagnostics.httpFailures) {
    const fault = markInjected(item);
    entries.push({ kind: 'network', ...item, ...(fault ? { injectedFault: true, injectedStatus: item.status } : {}) });
  }
  return entries;
};

export const runPlaywrightCase = async (context, scenarioID, caseName, work) => {
  const location = makeReportLocation(context, scenarioID, caseName);
  const registration = scenarioFor(scenarioID);
  const target = context.target;
  const report = createReport({
    scenario: scenarioID,
    caseName,
    target: safeTarget(target),
    evidenceDirectory: location.evidenceDirectory,
    requiredChecks: requiredChecksFor(registration, caseName, context.custom),
  });
  report.registryCoverage = registration.coverage;
  let sourceAtStart;
  let initialBuild;
  let browser;
  let injectedTarget;
  let activeAction;
  let activeStartedAt = Date.now();
  try {
    if (target.sourceRoot) {
      sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
      report.target.sourceFingerprint = sourceAtStart.fingerprint;
      report.sourceFingerprintManifest = { before: sourceAtStart.manifest };
    } else {
      report.target.sourceFingerprint = null;
      report.target.sourceFingerprintStatus = 'unknown: custom target has no local source root';
      recordUntested(report, 'correctness', 'local source fingerprint stayed unchanged during browser run',
        'Custom read-only targets do not have a local source checkout.');
    }
    if (target.composeProject) {
      initialBuild = apiBuildIdentity(target);
      report.target.apiBuildIdentity = initialBuild;
    } else {
      report.target.apiBuildIdentity = null;
      report.target.apiBuildIdentityStatus = 'unknown: custom target has no owned API container';
      recordUntested(report, 'correctness', 'local API build identity stayed unchanged during browser run',
        'Custom read-only targets do not have an owned API build container.');
    }
    browser = await launchBrowser({
      evidence: location.evidenceDirectory,
      appOrigins: [target.uiUrl, target.apiUrl],
      noAuth: context.custom === false && target.kind === 'isolated' && Boolean(target.composeProject) &&
        isLoopback(target.uiUrl) && isLoopback(target.apiUrl),
    });
    const { page, diagnostics } = browser;

    const check = (dimension, name, passed, evidence = {}) => {
      recordCheck(report, dimension, name, Boolean(passed), evidence);
      assert(passed, name);
    };

    const action = async (label, locator, perform, { after, budget = 5000, timeout = 5000, editable = false } = {}) => {
      activeAction = { label, locator: locator.toString() };
      browser.setCurrentAction(label);
      activeStartedAt = Date.now();
      const started = activeStartedAt;
      try {
        page.setDefaultTimeout(timeout);
        const count = await locator.count();
        assert.equal(count, 1, `${label}: expected one locator target, found ${count}`);
        assert(await locator.isVisible(), `${label}: target is not visible`);
        assert(await locator.isEnabled(), `${label}: target is disabled`);
        if (editable) assert(await locator.isEditable(), `${label}: target is read-only`);
        await perform();
        if (after) await after();
        const elapsedMs = Date.now() - started;
        report.actions.push({ label, status: 'passed', elapsedMs, locator: locator.toString() });
        report.timings[label] = elapsedMs;
        recordCheck(report, 'usability', `${label} completed`, true, { elapsedMs });
        if (after) recordCheck(report, 'performance', `${label} action-to-render within budget`, elapsedMs <= budget,
          { elapsedMs, budgetMs: budget, waitTimeoutMs: timeout });
        page.setDefaultTimeout(30000);
      } catch (error) {
        page.setDefaultTimeout(30000);
        const elapsedMs = Date.now() - started;
        report.actions.push({ label, status: 'failed', elapsedMs, locator: locator.toString(), error: sanitizeText(error.message) });
        recordCheck(report, 'usability', `${label} completed`, false, { elapsedMs, error: sanitizeText(error.message) });
        if (after) recordCheck(report, 'performance', `${label} action-to-render within budget`, false,
          { elapsedMs, budgetMs: budget, waitTimeoutMs: timeout });
        await browser.captureFailure(error, { action: { label, locator: locator.toString(), targetLocator: locator }, elapsedMs, phase: 'action' });
        throw error;
      }
    };

    const fault = async ({ method, path, matchesRequest }) => {
      injectedTarget = ownedBrowserRequestTarget(target, { method, path });
      const browserRequestOrigin = injectedTarget.origin;
      let count = 0;
      await page.route('**/*', async route => {
        const parts = pathAndMethod(route.request());
        const exactRequest = count === 0 && parts.origin === browserRequestOrigin &&
          parts.method === method && parts.pathname === path;
        let bodyMatches = !matchesRequest;
        if (exactRequest && matchesRequest) {
          try { bodyMatches = matchesRequest(route.request().postDataJSON()); }
          catch { bodyMatches = false; }
        }
        if (exactRequest && bodyMatches) {
          count += 1;
          await route.abort();
        } else {
          await route.continue();
        }
      });
      return { count: () => count, path };
    };

    await work({ page, browser, report, check, action, fault });
  } catch (error) {
    report.errors.push({ kind: 'scenario-error', message: sanitizeText(error.message ?? error) });
    recordCheck(report, 'correctness', 'scenario completed without an uncaught error', false,
      { message: sanitizeText(error.message ?? error) });
    if (browser) await browser.captureFailure(error, {
      action: activeAction ? { ...activeAction } : undefined,
      elapsedMs: Date.now() - activeStartedAt,
      phase: 'scenario',
    }).catch(() => undefined);
  } finally {
    if (browser) {
      report.network.push(...diagnosticsToNetwork(browser.diagnostics, injectedTarget));
      const unexpectedNetwork = report.network.filter(record =>
        record.kind === 'exception' || record.kind === 'console-error' ||
        (record.kind === 'network' && !record.injectedFault && !record.canceled));
      if (unexpectedNetwork.length) {
        await browser.captureFailure(new Error(`${unexpectedNetwork.length} unexpected browser or network errors`), {
          action: activeAction,
          elapsedMs: Date.now() - activeStartedAt,
          phase: 'final network audit',
          failures: unexpectedNetwork,
        }).catch(() => undefined);
      }
      report.assetFailures.push(...browser.diagnostics.assetFailures.map(item => ({ kind: 'asset-failure', ...item })));
      if (browser.diagnostics.assetFailures.length) {
        recordCheck(report, 'correctness', 'incidental asset failures are explicitly recorded', true,
          { failures: report.assetFailures });
      }
      await browser.close().catch(error => {
        report.errors.push({ kind: 'browser-close-error', message: sanitizeText(error.message) });
      });
    }
    if (sourceAtStart) {
      try {
        const sourceAtEnd = sourceFingerprintWithManifest(target.sourceRoot);
        const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
        report.sourceFingerprintManifest.changedPaths = changedPaths;
        recordCheck(report, 'correctness', 'watched source stayed unchanged during browser run',
          sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256,
          { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths });
      } catch (error) {
        recordCheck(report, 'correctness', 'watched source stayed unchanged during browser run', false,
          { before: sourceAtStart.fingerprint, error: sanitizeText(error.message) });
      }
    }
    if (initialBuild) {
      try {
        const finalBuild = apiBuildIdentity(target);
        recordCheck(report, 'correctness', 'API build identity stayed unchanged during browser run', finalBuild === initialBuild,
          { before: initialBuild, after: finalBuild });
      } catch (error) {
        recordCheck(report, 'correctness', 'API build identity stayed unchanged during browser run', false,
          { before: initialBuild, error: sanitizeText(error.message) });
      }
    }
  }
  finishReport(report);
  writeReport(location.reportPath, report);
  console.log(`UI_VERIFY scenario=${scenarioID} case=${caseName} status=${report.status} report=${location.reportPath}`);
  return report;
};
