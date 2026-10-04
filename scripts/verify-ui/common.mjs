import { createReport, finishReport, recordCheck, writeReport } from './report.mjs';
import { startBrowser, startNetworkMonitor, markNetwork, markFaultRecords, collectPageDiagnostics, captureDOM } from './browser.mjs';
import { parseArgs, createRunContext, makeReportLocation, printList, scenarioFor, validateScenarioCase, usage } from './cli.mjs';
import { requiredChecksFor } from './registry.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './source-fingerprint.mjs';
import { resolve } from 'node:path';

const safeTarget = (target) => {
  let url;
  try { url = new URL(target.uiUrl ?? 'http://127.0.0.1'); } catch { url = new URL('http://127.0.0.1'); }
  return { kind: target.kind ?? 'owned-dev-fixture', uiUrl: url.origin + url.pathname, sourceRoot: target.sourceRoot ?? null, project: target.fixtureProject ?? null, explorer: target.bootstrapExplorerId ?? null };
};

export const browserURL = (target, project, explorer, mode) => {
  const url = new URL(target.uiUrl);
  url.searchParams.set('project', project);
  url.searchParams.set('explorer', explorer);
  url.searchParams.set('mode', mode);
  return url.toString();
};

export const runBrowserCase = async (context, scenarioID, caseName, work) => {
  const location = makeReportLocation(context, scenarioID, caseName);
  const registration = scenarioFor(scenarioID);
  const report = createReport({ scenario: scenarioID, caseName, target: safeTarget(context.target), evidenceDirectory: location.evidenceDirectory, requiredChecks: requiredChecksFor(registration, caseName, context.custom) });
  report.registryCoverage = registration.coverage;
  const sourceAtStart = context.target.sourceRoot ? sourceFingerprintWithManifest(context.target.sourceRoot) : null;
  if (sourceAtStart) {
    report.target.sourceFingerprint = sourceAtStart.fingerprint;
    report.sourceFingerprintManifest = { before: sourceAtStart.manifest };
  }
  let browser;
  let monitor;
  const faults = [];
  try {
    browser = await startBrowser(location.evidenceDirectory);
    monitor = startNetworkMonitor(browser.cdp);
    await work({ cdp: browser.cdp, browser, report, faults });
  } catch (error) {
    report.errors.push({ kind: 'scenario-error', message: error instanceof Error ? error.message : String(error) });
    recordCheck(report, 'correctness', 'scenario completed without an uncaught error', false, { message: error instanceof Error ? error.message : String(error) });
    if (browser && !report.failureDom.length) {
      try { await captureDOM(report, browser.cdp, 'scenario-failure'); } catch {}
    }
  } finally {
    for (const fault of faults) await fault.restore().catch(() => undefined);
    if (browser) {
      try {
        const diagnostics = await collectPageDiagnostics(browser.cdp);
        report.longTasks = diagnostics.longTasks;
        report.pageErrors = diagnostics.pageErrors;
        if (diagnostics.navigationDurationMs !== undefined) report.timings.navigationDurationMs = diagnostics.navigationDurationMs;
        for (const message of diagnostics.pageErrors) report.network.push({ kind: 'exception', text: message });
      } catch (error) {
        report.errors.push({ kind: 'diagnostic-capture-error', message: error instanceof Error ? error.message : String(error) });
        recordCheck(report, 'correctness', 'browser diagnostics were collected', false, { message: error instanceof Error ? error.message : String(error) });
      }
      if (monitor) {
        try {
          const records = await monitor.stop(browser, faults);
          markFaultRecords(report, faults);
          markNetwork(report, records, faults);
        } catch (error) {
          report.errors.push({ kind: 'network-capture-error', message: error instanceof Error ? error.message : String(error) });
          recordCheck(report, 'correctness', 'network evidence collected', false);
        }
      }
      await browser.close().catch((error) => {
        report.errors.push({ kind: 'browser-close-error', message: String(error) });
        recordCheck(report, 'correctness', 'browser session closed cleanly', false, { message: String(error) });
      });
    }
  }
  if (sourceAtStart) {
    try {
      const sourceAtEnd = sourceFingerprintWithManifest(context.target.sourceRoot);
      const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
      report.sourceFingerprintManifest.changedPaths = changedPaths;
      recordCheck(report, 'correctness', 'watched source stayed unchanged during browser run', sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256, {
        before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths,
      });
    } catch (error) {
      recordCheck(report, 'correctness', 'watched source stayed unchanged during browser run', false, { before: sourceAtStart.fingerprint, error: String(error) });
    }
  }
  finishReport(report);
  writeReport(location.reportPath, report);
  console.log('UI_VERIFY scenario=' + scenarioID + ' case=' + (caseName ?? 'default') + ' status=' + report.status + ' report=' + location.reportPath);
  return report;
};

const writeUnreachable = (scenario, args, error) => {
  const path = args.reportPath ? resolve(args.reportPath) : resolve('.artifacts/verify-ui', scenario.id + '-unreachable-' + Date.now() + '.json');
  const report = createReport({ scenario: scenario.id, target: { kind: args.url ? 'read-only-custom' : 'owned-dev-fixture' }, evidenceDirectory: resolve('.artifacts/verify-ui') });
  report.status = 'unreachable';
  report.errors.push({ kind: 'unreachable', message: error instanceof Error ? error.message : String(error) });
  for (const dimension of Object.values(report.dimensions)) dimension.status = 'unreachable';
  writeReport(path, report);
  console.error('UI_VERIFY scenario=' + scenario.id + ' status=unreachable report=' + path + ' error=' + report.errors[0].message);
  process.exitCode = 1;
};

export const executeScenario = async ({ id, argv, runner, mutating = false }) => {
  const scenario = scenarioFor(id);
  let args;
  try { args = parseArgs(argv); } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 2;
    return [];
  }
  if (args.help) { console.log(usage(id)); return []; }
  if (args.list) { printList(); return []; }
  try { validateScenarioCase(scenario, args.caseName); } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 2;
    return [];
  }
  let context;
  try { context = await createRunContext(args, scenario, { mutating }); } catch (error) {
    writeUnreachable(scenario, args, error);
    return [];
  }
  const cases = args.caseName ? [args.caseName] : scenario.cases;
  const reports = await runner(context, cases);
  if (reports.some((report) => report.status !== 'passed')) process.exitCode = 1;
  return reports;
};
