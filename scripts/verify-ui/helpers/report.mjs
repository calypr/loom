import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';

export const reportDimensions = Object.freeze(['usability', 'correctness', 'persistence', 'performance']);

export const createReport = ({ scenario, target, evidenceDirectory, caseName, requiredChecks = [] }) => ({
  schemaVersion: 2,
  scenario,
  case: caseName ?? null,
  target,
  status: 'running',
  dimensions: Object.fromEntries(reportDimensions.map((dimension) => [dimension, { status: 'untested', evidence: [] }])),
  actions: [],
  errors: [],
  network: [],
  assetFailures: [],
  assertions: [],
  requiredChecks,
  missingRequiredChecks: [],
  evidence: [],
  timings: {},
  failureDom: [],
  startedAt: new Date().toISOString(),
  evidenceDirectory,
});

export const recordCheck = (report, dimension, name, passed, evidence = {}) => {
  const status = passed ? 'passed' : 'failed';
  const current = report.dimensions[dimension];
  if (!current) throw new Error('unknown report dimension: ' + dimension);
  if (status === 'failed' || current.status === 'untested') current.status = status;
  report.assertions.push({ dimension, name, status, evidence });
  current.evidence.push({ name, status, ...evidence });
  return passed;
};

export const recordUntested = (report, dimension, name, reason) => {
  const current = report.dimensions[dimension];
  if (!current) throw new Error('unknown report dimension: ' + dimension);
  current.evidence.push({ name, status: 'untested', reason });
};

// This is a diagnostic label only. The Playwright observer owns native failure assertions.
export const classifyNetworkRecord = (record = {}) => {
  if (['exception', 'console-error', 'console', 'pageerror', 'runtime'].includes(record.kind) || record.internalError) {
    return 'unexpected-error';
  }
  if (record.kind === 'asset-failure') return 'incidental-asset';
  if (record.expectedTransportFailure) return 'expected-injected';
  if ([400, 422].includes(record.status) && record.expectedValidation?.status === record.status) {
    return 'expected-validated-http';
  }
  if ([400, 422].includes(record.status) && record.injectedFault === true && record.injectedStatus === record.status) {
    return 'expected-injected';
  }
  if (record.status >= 400) return 'unexpected-error';
  if (record.errorText && record.canceled && record.readCancellationDiagnostic === true) return 'cancelled';
  if (record.errorText) return 'unexpected-error';
  return 'ok';
};

export const isActionable = (snapshot) =>
  Boolean(snapshot?.visible)
  && !snapshot?.disabled
  && snapshot?.ariaDisabled !== 'true'
  && snapshot?.pointerEvents !== 'none'
  && Boolean(snapshot?.receivesPointer);

// Report status describes recorded evidence; it is never used to decide whether a native test passed.
export const finishReport = (report) => {
  const assertions = Array.isArray(report.assertions) ? report.assertions : [];
  const requiredChecks = Array.isArray(report.requiredChecks) ? report.requiredChecks : [];
  const failed = assertions.some((assertion) => assertion.status === 'failed');
  const passed = assertions.some((assertion) => assertion.status === 'passed');
  const unreachable = report.dimensions && Object.values(report.dimensions).some((dimension) => dimension.status === 'unreachable');
  report.missingRequiredChecks = requiredChecks.length
    ? requiredChecks.filter((name) => !assertions.some((assertion) => assertion.name === name && assertion.status === 'passed'))
    : ['case requirements are missing'];
  report.status = failed ? 'failed' : unreachable ? 'unreachable' : !passed ? 'untested' : report.missingRequiredChecks.length ? 'partial' : 'passed';
  report.finishedAt = new Date().toISOString();
  return report;
};

export const writeReport = (path, report) => {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
};
