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

export const classifyNetworkRecord = (record) => {
  if (record.kind === 'exception' || record.kind === 'console-error' || record.internalError) return 'unexpected-error';
  if (record.kind === 'asset-failure') return 'incidental-asset';
  if (record.injectedFault && record.status === 422 && record.injectedStatus === 422) return 'expected-injected';
  if (record.status >= 400 || record.internalError) return 'unexpected-error';
  if (record.errorText === 'net::ERR_ABORTED' && record.canceled) return 'cancelled';
  if (record.injectedFault && record.errorText) return 'expected-injected';
  if (record.errorText) return 'unexpected-error';
  return 'ok';
};

export const isActionable = (snapshot) =>
  Boolean(snapshot?.visible)
  && !snapshot?.disabled
  && snapshot?.ariaDisabled !== 'true'
  && snapshot?.pointerEvents !== 'none'
  && Boolean(snapshot?.receivesPointer);

export const finishReport = (report) => {
  const unexpected = report.network.filter((record) => classifyNetworkRecord(record) === 'unexpected-error');
  if (unexpected.length) {
    report.errors.push(...unexpected.map((record) => ({ kind: 'unexpected-network', ...record })));
    recordCheck(report, 'correctness', 'no unexpected network, module, or browser errors', false, { count: unexpected.length });
  } else if (report.network.some((record) => classifyNetworkRecord(record) === 'expected-injected')) {
    report.errors.push(...report.network.filter((record) => classifyNetworkRecord(record) === 'expected-injected').map((record) => ({ kind: 'expected-injected', ...record })));
  }
  const failed = report.assertions.some((assertion) => assertion.status === 'failed');
  const passed = report.assertions.some((assertion) => assertion.status === 'passed');
  const unreachable = report.dimensions && Object.values(report.dimensions).some((dimension) => dimension.status === 'unreachable');
  report.missingRequiredChecks = report.requiredChecks.length
    ? report.requiredChecks.filter((name) => !report.assertions.some((assertion) => assertion.name === name && assertion.status === 'passed'))
    : ['case requirements are missing'];
  report.status = failed ? 'failed' : unreachable ? 'unreachable' : !passed ? 'untested' : report.missingRequiredChecks.length ? 'partial' : 'passed';
  report.finishedAt = new Date().toISOString();
  return report;
};

export const writeReport = (path, report) => {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
};
