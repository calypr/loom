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

const validatedObsoleteNetworkReads = new WeakMap();

const isCodedSourceColumnReport = (report) =>
  report?.scenario === 'builder-coded-source-column' && report?.case === 'coded-source-column';

const validationSnapshot = (report, record, proof) => {
  const assertionNames = new Set([proof.assertion?.name, proof.assertion?.reloadName].filter(Boolean));
  const networkRecord = { ...record };
  delete networkRecord.rawURL;
  return JSON.stringify({
    proof,
    record: networkRecord,
    fixtureOracle: report.target?.fixtureOracle,
    actions: (report.actions ?? []).filter(action => action.id === proof.action?.id),
    assertions: (report.assertions ?? []).filter(assertion => assertionNames.has(assertion.name)),
  });
};

const exactValidatedObsoleteRead = (report, record) => {
  const registered = validatedObsoleteNetworkReads.get(report)?.get(record.playwrightRequestId);
  const proof = registered?.proof;
  if (!proof || !isCodedSourceColumnReport(report) || record.kind !== 'network' ||
      record.errorText !== 'net::ERR_ABORTED' || record.status >= 400 || record.internalError ||
      record.expectedObsolete !== true || record.obsolescenceEvidence !== proof ||
      (record.rawURL !== undefined && record.rawURL !== record.url) ||
      typeof record.playwrightRequestId !== 'string' || !record.playwrightRequestId ||
      proof.failedRequest?.playwrightRequestId !== record.playwrightRequestId ||
      (report.network ?? []).filter(candidate => candidate === record).length !== 1 ||
      (report.network ?? []).filter(candidate => candidate.kind === 'network' &&
        candidate.playwrightRequestId === record.playwrightRequestId).length !== 1 ||
      (report.expectedObsolete ?? []).filter(candidate => candidate === proof).length !== 1 ||
      (report.expectedObsolete ?? []).filter(candidate =>
        candidate?.failedRequest?.playwrightRequestId === record.playwrightRequestId).length !== 1 ||
      validationSnapshot(report, record, proof) !== registered.snapshot) return false;
  return true;
};

export const registerValidatedObsoleteNetworkRead = (report, record, proof) => {
  if (!isCodedSourceColumnReport(report) || record.kind !== 'network' ||
      record.errorText !== 'net::ERR_ABORTED' || record.status >= 400 || record.internalError ||
      record.expectedObsolete !== true || record.obsolescenceEvidence !== proof ||
      typeof record.playwrightRequestId !== 'string' || !record.playwrightRequestId ||
      proof?.failedRequest?.playwrightRequestId !== record.playwrightRequestId ||
      (report.network ?? []).filter(candidate => candidate === record).length !== 1 ||
      (report.network ?? []).filter(candidate => candidate.kind === 'network' &&
        candidate.playwrightRequestId === record.playwrightRequestId).length !== 1 ||
      (report.expectedObsolete ?? []).filter(candidate => candidate === proof).length !== 1 ||
      (report.expectedObsolete ?? []).filter(candidate =>
        candidate?.failedRequest?.playwrightRequestId === record.playwrightRequestId).length !== 1 ||
      (record.rawURL !== undefined && record.rawURL !== record.url)) return false;
  let proofsByRequestID = validatedObsoleteNetworkReads.get(report);
  if (!proofsByRequestID) {
    proofsByRequestID = new Map();
    validatedObsoleteNetworkReads.set(report, proofsByRequestID);
  }
  const prior = proofsByRequestID.get(record.playwrightRequestId);
  if (prior && prior.proof !== proof) return false;
  let snapshot;
  try {
    snapshot = validationSnapshot(report, record, proof);
  } catch {
    return false;
  }
  proofsByRequestID.set(record.playwrightRequestId, Object.freeze({ proof, snapshot }));
  return true;
};

const classifyReportNetworkRecord = (report, record) => {
  if (isCodedSourceColumnReport(report) && record.errorText === 'net::ERR_ABORTED') {
    return exactValidatedObsoleteRead(report, record) ? 'cancelled' : 'unexpected-error';
  }
  return classifyNetworkRecord(record);
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
  const unexpected = report.network.filter((record) => classifyReportNetworkRecord(report, record) === 'unexpected-error');
  if (unexpected.length) {
    report.errors.push(...unexpected.map((record) => ({ kind: 'unexpected-network', ...record })));
    recordCheck(report, 'correctness', 'no unexpected network, module, or browser errors', false, { count: unexpected.length });
  } else if (report.network.some((record) => classifyReportNetworkRecord(report, record) === 'expected-injected')) {
    report.errors.push(...report.network.filter((record) => classifyReportNetworkRecord(report, record) === 'expected-injected').map((record) => ({ kind: 'expected-injected', ...record })));
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

export const adjudicatePendingLifecycle = (report) => {
  if (report.lifecycle?.status !== 'pending-final-adjudication') return report;
  report.lifecycle.status = report.status === 'passed' ? 'passed' : 'failed';
  report.lifecycle.finalReportStatus = report.status;
  if (report.lifecycle.status === 'failed') {
    const unexpectedNetwork = (report.network ?? [])
      .filter((record) => classifyReportNetworkRecord(report, record) === 'unexpected-error')
      .map(({ kind, message, status, method, url, errorText }) => ({
        kind,
        ...(message === undefined ? {} : { message }),
        ...(status === undefined ? {} : { status }),
        ...(method === undefined ? {} : { method }),
        ...(url === undefined ? {} : { url }),
        ...(errorText === undefined ? {} : { errorText }),
      }));
    report.lifecycle.failure ??= {
      finalReportStatus: report.status,
      missingRequiredChecks: [...(report.missingRequiredChecks ?? [])],
      unexpectedNetwork,
    };
  }
  return report;
};

export const writeReport = (path, report) => {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
};
