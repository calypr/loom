import { createHash } from 'node:crypto';
import { readdirSync, readFileSync, realpathSync } from 'node:fs';
import { basename, dirname, isAbsolute, relative, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { caseNamesFor, hasLifecycleContract, registry, requiresLifecycleAcceptance, scenarioCaseFor } from '../registry.mjs';
import { sourceFingerprint } from './source-fingerprint.mjs';

const dimensions = ['usability', 'correctness', 'persistence', 'performance'];
const sourceFreezeCheck = 'watched source stayed unchanged during browser run';
const apiBuildIdentityPattern = /^[a-f0-9]{64}(?::[a-f0-9]{64}){2}$/i;

const isFingerprint = (value) =>
  typeof value?.sha256 === 'string'
  && /^[a-f0-9]{64}$/i.test(value.sha256)
  && Number.isInteger(value.files)
  && value.files > 0;

const sameFingerprint = (left, right) =>
  isFingerprint(left)
  && isFingerprint(right)
  && left.sha256.toLowerCase() === right.sha256.toLowerCase()
  && left.files === right.files;

const sourceFreshness = (report, expected) => {
  if (!isFingerprint(expected)) return 'unknown';
  const captured = report?.target?.sourceFingerprint;
  if (!isFingerprint(captured)) return 'unknown';
  if (!sameFingerprint(captured, expected)) return 'historical';

  const assertion = report.assertions?.find((item) => item.name === sourceFreezeCheck);
  const before = assertion?.evidence?.before;
  const after = assertion?.evidence?.after;
  if (!isFingerprint(before) || !isFingerprint(after)) return 'unknown';
  if (!sameFingerprint(before, captured) || !sameFingerprint(after, captured)) return 'historical';
  if (assertion.status !== 'passed') return 'historical';
  return 'current';
};

const normalizeApiBuildIdentity = (value) =>
  typeof value === 'string' && apiBuildIdentityPattern.test(value.trim())
    ? value.trim().toLowerCase()
    : null;

const buildFreshness = (report, expected) => {
  const expectedIdentity = normalizeApiBuildIdentity(expected);
  if (!expectedIdentity) return 'unknown';
  const capturedIdentity = normalizeApiBuildIdentity(report?.apiBuildIdentity ?? report?.target?.apiBuildIdentity);
  if (!capturedIdentity) return 'unknown';
  return capturedIdentity === expectedIdentity ? 'current' : 'historical';
};

export const classifyFreshness = (report, baseline = {}) => {
  const source = sourceFreshness(report, baseline.sourceFingerprint);
  const build = buildFreshness(report, baseline.apiBuildIdentity);
  const status = source === 'historical' || build === 'historical'
    ? 'historical'
    : source === 'current' && build === 'current' ? 'current' : 'unknown';
  return { status, source, build };
};

const compactReport = (report) => Number.isSafeInteger(report?.epoch)
  && typeof report?.scenario === 'string'
  && typeof report?.case === 'string'
  && typeof report?.durableClosurePath === 'string';

const compactReportForCoverage = (report, closure, requiredChecks) => {
  if (!compactReport(report) || !closure || !Array.isArray(requiredChecks) || requiredChecks.length === 0) return null;
  if (closure.epoch !== report.epoch
    || closure.case?.scenarioId !== report.scenario
    || closure.case?.caseName !== report.case) return null;

  const integrity = closure.integrityClosure;
  const sourceBefore = integrity?.source?.before;
  const sourceAfter = integrity?.source?.after;
  const api = integrity?.apiBuildIdentity;
  const mounts = integrity?.ownedMounts;
  const health = integrity?.health;
  if (typeof closure.status !== 'string' || !closure.status.startsWith('CLOSED_') || integrity?.status !== 'PASS'
    || integrity.source?.manifestsEqual !== true
    || !Array.isArray(integrity.source.changedPaths) || integrity.source.changedPaths.length !== 0
    || !sameFingerprint(sourceBefore, sourceAfter)
    || !normalizeApiBuildIdentity(api?.before)
    || api.before !== api.after || api.before !== api.precheck || api.unchanged !== true
    || mounts?.before !== 'PASS' || mounts?.after !== 'PASS' || mounts.targetUnchanged !== true
    || health?.before?.status !== 'PASS' || health?.after?.status !== 'PASS') return null;

  const reportSource = report.integrity?.sourceBeforeAfter;
  const reportSourceBefore = reportSource?.before ?? reportSource;
  const reportSourceAfter = reportSource?.after ?? reportSource;
  const reportSourceContradicts = reportSource?.unchanged === false
    || (reportSource?.manifestsEqual !== undefined && reportSource.manifestsEqual !== true)
    || (reportSource?.changedPaths !== undefined
      && (!Array.isArray(reportSource.changedPaths) || reportSource.changedPaths.length !== 0));
  const reportSourceUnchanged = !reportSourceContradicts && (reportSource?.unchanged === true
      || (reportSource?.manifestsEqual === true && Array.isArray(reportSource?.changedPaths)
        && reportSource.changedPaths.length === 0 && sameFingerprint(reportSourceBefore, reportSourceAfter)));
  const reportApi = report.integrity?.apiBuildIdentity;
  const reportApiUnchanged = report.integrity?.apiBuildIdentityUnchanged !== false && (reportApi
    ? reportApi.unchanged === true && reportApi.before === api.before && reportApi.after === api.after
    : report.integrity?.apiBuildIdentityUnchanged === true);
  const mountTarget = mounts.target;
  const ownedStackTarget = report.ownedStackValidationTarget ?? report.target;
  if (report.integrity?.closureStatus !== 'PASS' || !reportApiUnchanged
    || !reportSourceUnchanged || !sameFingerprint(reportSourceBefore, sourceAfter)
    || !sameFingerprint(reportSourceAfter, sourceAfter)
    || report.integrity.ownedMounts?.before !== 'PASS' || report.integrity.ownedMounts?.after !== 'PASS'
    || report.integrity.ownedMounts?.targetUnchanged !== true
    || report.integrity.health?.before?.status !== 'PASS' || report.integrity.health?.after?.status !== 'PASS'
    || ['project', 'composeProject', 'generation', 'sourceRoot'].some((key) =>
      Object.hasOwn(ownedStackTarget ?? {}, key) && ownedStackTarget[key] !== mountTarget?.[key])) return null;

  const checkSummary = report.requiredChecks;
  const closedChecks = closure.case.requiredChecks;
  const passedChecks = Array.isArray(closedChecks?.evidence) ? closedChecks.evidence : [];
  if (!Number.isInteger(checkSummary?.total)
    || checkSummary.total !== requiredChecks.length
    || closedChecks?.total !== requiredChecks.length
    || checkSummary.passed !== closedChecks.passed
    || (report.status === 'passed' && passedChecks.length !== closedChecks.passed)
    || passedChecks.length > closedChecks.passed) return null;

  const evidenceNames = new Set();
  for (const item of passedChecks) {
    if (!requiredChecks.includes(item?.name) || evidenceNames.has(item.name)
      || !['passed', 'failed'].includes(item.status)) return null;
    evidenceNames.add(item.name);
  }
  const checkFailures = passedChecks.filter((item) => item.status === 'failed').length;
  const reportedFailures = checkSummary.failed ?? 0;
  const reportedUnrun = checkSummary.unrun ?? 0;
  if (checkFailures !== reportedFailures
    || closedChecks.passed + reportedFailures + reportedUnrun !== requiredChecks.length
    || (closedChecks.missingOrUnrun !== undefined && closedChecks.missingOrUnrun !== reportedUnrun)
    || (closedChecks.missingOrFailed !== undefined && closedChecks.missingOrFailed !== reportedFailures)) return null;

  if (report.status === 'passed') {
    if (report.runnerStatus !== 'passed' || report.coverageStatus !== 'passed'
      || closure.status !== 'CLOSED_PASS' || closure.case.status !== 'passed'
      || checkSummary.passed !== requiredChecks.length || reportedFailures !== 0 || reportedUnrun !== 0
      || requiredChecks.some((name) => !evidenceNames.has(name))) return null;
  } else if (report.status.startsWith('skipped_')) {
    if (!closure.status.startsWith('CLOSED_SKIP') || !closure.case.status.startsWith('skipped_')) return null;
  } else if (report.status.startsWith('partial_') || report.status.startsWith('failed_')) {
    if (!closure.status.startsWith('CLOSED_FAILED') || !closure.case.status.startsWith('failed_')) return null;
  } else {
    return null;
  }

  const target = {
    ...report.target,
    sourceFingerprint: sourceAfter,
    apiBuildIdentity: api.after,
  };
  const assertions = passedChecks.map(({ name, status }) => ({ name, status }));
  assertions.push({
    name: sourceFreezeCheck,
    status: 'passed',
    evidence: { before: sourceBefore, after: sourceAfter },
  });
  return { ...report, schemaVersion: 2, target, apiBuildIdentity: api.after, assertions };
};

const legacyClosureFingerprint = (integrity) => {
  const source = integrity?.source;
  const before = source?.before ?? (source?.beforeSha256 ? { sha256: source.beforeSha256, files: source.files } : null);
  const after = source?.after ?? (source?.afterSha256 ? { sha256: source.afterSha256, files: source.files } : null);
  const api = integrity?.apiBuildIdentity;
  const mounts = integrity?.ownedMounts;
  const mountsBefore = mounts?.before ?? mounts?.beforeStatus;
  const mountsAfter = mounts?.after ?? mounts?.afterStatus;
  const health = integrity?.health;
  const healthBeforeStatus = health?.before?.status ?? health?.beforeStatus;
  const healthAfterStatus = health?.after?.status ?? health?.afterStatus;
  const healthBeforeSamples = health?.before?.samples ?? health?.beforeSamples;
  const healthAfterSamples = health?.after?.samples ?? health?.afterSamples;
  if (integrity?.status !== 'PASS' || !sameFingerprint(before, after)
    || source?.manifestsEqual === false || source?.manifestUnchanged === false
    || !Array.isArray(source?.changedPaths) || source.changedPaths.length !== 0
    || !normalizeApiBuildIdentity(api?.before) || api.before !== api.after
    || (api.precheck !== undefined && api.before !== api.precheck) || api.unchanged !== true
    || mountsBefore !== 'PASS' || mountsAfter !== 'PASS' || mounts.targetUnchanged !== true
    || healthBeforeStatus !== 'PASS' || healthAfterStatus !== 'PASS'
    || !Number.isInteger(healthBeforeSamples) || healthBeforeSamples < 1
    || !Number.isInteger(healthAfterSamples) || healthAfterSamples < 1) return null;
  return { sourceBefore: before, sourceAfter: after, apiIdentity: api.after, target: mounts.target };
};

const legacyCaseIdentityMatches = (report, closure, linkKind) => {
  const details = closure?.case;
  if (!details || typeof details !== 'object') return false;
  const explicitScenario = details.scenarioId ?? details.scenarioID;
  const pathScenario = typeof details.scenario === 'string' ? details.scenario : null;
  const explicitCase = details.caseName;
  if (explicitScenario !== undefined && explicitScenario !== report.scenario) return false;
  if (explicitCase !== undefined && explicitCase !== report.case) return false;
  if (pathScenario !== null && pathScenario !== `${report.scenario}/${report.case}`) return false;
  return explicitScenario !== undefined || explicitCase !== undefined || pathScenario !== null
    || linkKind === 'lifecycle-report-sha256';
};

const legacyCheckEvidence = (report, closure, requiredChecks) => {
  const reported = report.requiredChecks;
  const closed = closure?.case?.requiredChecks;
  if (!reported || !closed || !Array.isArray(requiredChecks) || requiredChecks.length === 0) return null;
  const reportNames = Array.isArray(reported) ? reported : null;
  const reportCounts = reportNames
    ? { passed: reportNames.length, failed: 0, missing: 0, notRun: 0, total: reportNames.length }
    : {
      passed: reported.passed,
      failed: reported.failed ?? 0,
      missing: reported.missing ?? 0,
      notRun: reported.notRun ?? 0,
      total: reported.total,
    };
  const closedMissing = Array.isArray(closed.missing) ? closed.missing.length : (closed.missing ?? 0);
  if (reportCounts.total !== requiredChecks.length || reportCounts.passed !== requiredChecks.length
    || reportCounts.failed !== 0 || reportCounts.missing !== 0 || reportCounts.notRun !== 0
    || closed.total !== requiredChecks.length || closed.passed !== requiredChecks.length
    || (closed.failed ?? 0) !== 0 || closedMissing !== 0 || (closed.notRun ?? 0) !== 0) return null;
  const closureStatus = closure.case.status ?? closure.case.runnerStatus ?? closure.case.reportStatus;
  if (closureStatus !== 'passed' || closure.case.runnerStatus === 'failed'
    || closure.case.reportStatus === 'failed') return null;

  if (!reportNames) return { named: false };
  if (reportNames.length !== requiredChecks.length
    || reportNames.some((name, index) => name !== requiredChecks[index])
    || (Array.isArray(report.missingRequiredChecks) && report.missingRequiredChecks.length !== 0)
    || !Array.isArray(report.assertions)) return null;
  const byName = new Map();
  for (const assertion of report.assertions) {
    if (typeof assertion?.name !== 'string' || !['passed', 'failed'].includes(assertion.status)) continue;
    if (!requiredChecks.includes(assertion.name)) continue;
    const prior = byName.get(assertion.name);
    if (prior === 'failed' || assertion.status === 'failed') byName.set(assertion.name, 'failed');
    else byName.set(assertion.name, 'passed');
  }
  if (requiredChecks.some((name) => byName.get(name) !== 'passed')) return null;
  return { named: true };
};

const normalizeLegacyPairedReport = (entry, requiredChecks) => {
  const { report, closure, legacyPair } = entry;
  if (!legacyPair || !closure || !Number.isSafeInteger(report?.epoch)
    || closure.epoch !== report.epoch || report.status !== 'passed') return null;
  const linkKind = legacyPair.linkKind;
  if (!linkKind || !legacyCaseIdentityMatches(report, closure, linkKind)) return null;
  if (typeof closure.status !== 'string'
    || !(closure.status.startsWith('CLOSED_PASS') || closure.status.startsWith('CLOSED_WITH_CASE_PASS'))) return null;
  const closureCase = closure.case ?? {};
  if ([closureCase.unexpectedErrors, closureCase.networkErrors, closureCase.runtimeErrors, closureCase.unexpectedNetworkErrors]
    .some((count) => Number.isFinite(count) && count !== 0)) return null;
  const integrity = legacyClosureFingerprint(closure.integrityClosure);
  const checkEvidence = legacyCheckEvidence(report, closure, requiredChecks);
  if (!integrity || !checkEvidence) return null;

  const claimedFingerprints = [
    report.sourceFingerprint,
    report.target?.sourceFingerprint,
    report.before?.sourceFingerprint,
    report.after?.sourceFingerprint,
  ].filter((value) => value !== undefined);
  if (report.integrity?.sourceFingerprintSha256 !== undefined) {
    claimedFingerprints.push({ sha256: report.integrity.sourceFingerprintSha256, files: report.integrity.sourceFiles });
  }
  if (claimedFingerprints.some((value) => !sameFingerprint(value, integrity.sourceAfter))) return null;

  const claimedIdentities = [
    report.apiBuildIdentity,
    report.target?.apiBuildIdentity,
    report.apiBuildFreeze?.before,
    report.apiBuildFreeze?.after,
    report.before?.apiBuildIdentity,
    report.after?.apiBuildIdentity,
    report.integrity?.apiBuildIdentity,
  ].filter((value) => value !== undefined);
  if (claimedIdentities.some((value) => normalizeApiBuildIdentity(value) !== integrity.apiIdentity)) return null;

  const target = {
    ...report.target,
    sourceFingerprint: integrity.sourceAfter,
    apiBuildIdentity: integrity.apiIdentity,
  };
  const assertions = Array.isArray(report.assertions) ? [...report.assertions] : [];
  if (!assertions.some((item) => item?.name === sourceFreezeCheck)) assertions.push({
    name: sourceFreezeCheck,
    status: 'passed',
    evidence: { before: integrity.sourceBefore, after: integrity.sourceAfter },
  });
  const normalized = {
    ...report,
    schemaVersion: 2,
    target,
    apiBuildIdentity: integrity.apiIdentity,
    assertions,
  };
  return {
    report: normalized,
    note: checkEvidence.named ? null : 'legacy closure verifies passing check totals, but this report has no named required-check outcomes; retained as partial',
  };
};

const withinRoot = (candidate, root) => {
  const fromRoot = relative(root, candidate);
  return fromRoot === '' || (fromRoot !== '..' && !fromRoot.startsWith(`..${sep}`) && !isAbsolute(fromRoot));
};

const hashConfinedFile = (path, { cwd, evidenceRoots }) => {
  if (typeof path !== 'string' || path.length === 0) return null;
  try {
    const resolvedPath = resolve(cwd, path);
    const realPath = realpathSync(resolvedPath);
    const roots = [cwd, ...evidenceRoots].flatMap((candidate) => {
      try { return [realpathSync(candidate)]; } catch { return []; }
    });
    if (!roots.some((root) => withinRoot(realPath, root))) return null;
    return createHash('sha256').update(readFileSync(realPath)).digest('hex');
  } catch {
    return null;
  }
};

const legacyClosureLinkKind = ({ report, closure, relativeReportPath, reportSha256, cwd, evidenceRoots }) => {
  if (closure?.lifecycleReport === relativeReportPath
    && closure.lifecycleReportSha256 === reportSha256) return 'lifecycle-report-sha256';
  const rawEvidenceSha256 = hashConfinedFile(report?.evidenceReportPath, { cwd, evidenceRoots });
  if (typeof report?.evidenceReportPath === 'string'
    && typeof report.evidenceReportSha256 === 'string'
    && closure?.case?.reportPath === report.evidenceReportPath
    && /^[a-f0-9]{64}$/i.test(report.evidenceReportSha256)
    && typeof closure.case.reportSha256 === 'string'
    && closure.case.reportSha256.toLowerCase() === report.evidenceReportSha256.toLowerCase()
    && rawEvidenceSha256 === report.evidenceReportSha256.toLowerCase()) return 'evidence-report-sha256';
  return null;
};

const readLegacySiblingClosure = ({ path, report, root, directory, evidenceRoots }) => {
  if (!Number.isSafeInteger(report?.epoch) || typeof report?.scenario !== 'string'
    || typeof report?.case !== 'string' || !basename(path).endsWith('-report.json')) return null;
  const candidateWithoutClosure = { legacyPair: { candidate: true, linkKind: null } };
  const expectedClosureName = basename(path).replace(/-report\.json$/, '-closure.json');
  const siblingClosurePath = resolve(directory, expectedClosureName);
  if (basename(siblingClosurePath) !== expectedClosureName) return null;
  try {
    const realRoot = realpathSync(root);
    const realReportPath = realpathSync(path);
    const realClosurePath = realpathSync(siblingClosurePath);
    const withinRuntimeRoot = (candidate) => withinRoot(candidate, realRoot);
    if (!withinRuntimeRoot(realReportPath)) return null;
    if (!withinRuntimeRoot(realClosurePath) || dirname(realReportPath) !== dirname(realClosurePath)
      || basename(realClosurePath) !== expectedClosureName) return candidateWithoutClosure;
    const reportBytes = readFileSync(realReportPath);
    const closure = JSON.parse(readFileSync(realClosurePath, 'utf8'));
    const relativeReportPath = relative(realRoot, realReportPath).split(sep).join('/');
    const reportSha256 = createHash('sha256').update(reportBytes).digest('hex');
    return {
      closure,
      legacyPair: {
        candidate: true,
        closurePath: realClosurePath,
        linkKind: legacyClosureLinkKind({ report, closure, relativeReportPath, reportSha256, cwd: root, evidenceRoots }),
      },
    };
  } catch {
    try {
      const realRoot = realpathSync(root);
      const realReportPath = realpathSync(path);
      const fromRoot = relative(realRoot, realReportPath);
      if (fromRoot === '..' || fromRoot.startsWith(`..${sep}`) || isAbsolute(fromRoot)) return null;
      return candidateWithoutClosure;
    } catch {
      return null;
    }
  }
};

const registeredChecksFor = (scenario, caseName, report) => {
  try {
    return scenarioCaseFor(scenario, caseName, report.target?.kind === 'read-only-custom').requiredChecks;
  } catch (error) {
    if (error instanceof Error && (error.message === `missing required checks for ${scenario.id}/${caseName}` || error.message === `unknown case for ${scenario.id}: ${caseName}`)) return undefined;
    throw error;
  }
};

export const classifyEvidence = (report, requiredChecks) => {
  if (report.status !== 'passed') return report.status;
  if (report.schemaVersion >= 2) {
    if (!Array.isArray(requiredChecks) || requiredChecks.length === 0 || !Array.isArray(report.assertions)) return 'partial';
    if (requiredChecks.some((name) => report.assertions.some((assertion) => assertion?.name === name && assertion?.status === 'failed'))) return 'failed';
    return requiredChecks.every((name) => report.assertions.some((assertion) => assertion?.name === name && assertion?.status === 'passed'))
      ? 'passed'
      : 'partial';
  }
  return dimensions.every((dimension) => report.dimensions?.[dimension]?.status === 'passed')
    ? 'passed'
    : 'partial';
};

export const summarizeCoverage = (scenarios, reports, baseline = {}) => {
  const latest = new Map();
  for (const { path, report, closure, legacyPair } of reports) {
    if (!report?.scenario || !report?.case || (!report?.finishedAt && !compactReport(report) && !legacyPair?.candidate)) continue;
    const key = `${report.scenario}/${report.case}`;
    const compact = compactReport(report);
    const legacy = !compact && legacyPair?.candidate === true;
    const requiredChecks = (() => {
      const scenario = scenarios.find((item) => item.id === report.scenario);
      return scenario ? registeredChecksFor(scenario, report.case, report) : undefined;
    })();
    const legacyResult = legacy ? normalizeLegacyPairedReport({ report, closure, legacyPair }, requiredChecks) : null;
    const normalized = compact ? compactReportForCoverage(report, closure, requiredChecks)
      : legacy ? legacyResult?.report ?? null : report;
    const note = legacy
      ? legacyResult ? legacyResult.note : 'legacy report and adjacent closure lack exact case, report-hash, or complete integrity linkage'
      : null;
    const order = compact || legacy ? { kind: 'epoch', value: report.epoch } : { kind: 'time', value: report.finishedAt };
    const previous = latest.get(key);
    if (!previous) {
      latest.set(key, { path, report, normalized, compact, legacy, note, order });
      continue;
    }
    if (previous.ambiguousOrder) {
      latest.set(key, { ...previous, path: `${previous.path},${path}` });
      continue;
    }
    if (previous.order.kind !== order.kind) {
      latest.set(key, { path: `${previous.path},${path}`, report, normalized: null, compact: true, order, ambiguousOrder: true });
    } else if (order.value > previous.order.value) {
      latest.set(key, { path, report, normalized, compact, legacy, note, order });
    } else if (order.value === previous.order.value && previous.path !== path) {
      latest.set(key, { path: `${previous.path},${path}`, report, normalized: null, compact: true, order, ambiguousOrder: true });
    }
  }
  return scenarios.flatMap((scenario) => caseNamesFor(scenario).map((caseName) => {
    const evidence = latest.get(`${scenario.id}/${caseName}`);
    const requiredChecks = evidence ? registeredChecksFor(scenario, caseName, evidence.normalized ?? evidence.report) : undefined;
    return {
      path: `${scenario.id}/${caseName}`,
      status: evidence
        ? evidence.ambiguousOrder || (evidence.compact && !evidence.normalized)
          || (evidence.legacy && evidence.report.status === 'passed'
            && (!evidence.normalized || evidence.note !== null))
          ? 'partial'
          : classifyEvidence(evidence.normalized ?? evidence.report, requiredChecks)
        : 'untested',
      freshness: evidence && !evidence.ambiguousOrder && evidence.normalized
        ? classifyFreshness(evidence.normalized, baseline)
        : { status: 'unknown', source: 'unknown', build: 'unknown' },
      finishedAt: evidence?.report.finishedAt ?? null,
      report: evidence?.path ?? null,
      evidenceNote: evidence?.note ?? null,
    };
  }));
};

export const currentCoverageBaseline = ({ cwd = process.cwd(), env = process.env } = {}) => {
  let currentSourceFingerprint = null;
  try { currentSourceFingerprint = sourceFingerprint(cwd); } catch {}
  return {
    sourceFingerprint: currentSourceFingerprint,
    // The running API identity is intentionally opt-in. Reports need the exact
    // three-part digest emitted by loom-dev-build-stamp.sh --check; an absent
    // value leaves build freshness unknown instead of guessing from a freeze.
    apiBuildIdentity: normalizeApiBuildIdentity(env.LOOM_VERIFY_UI_API_BUILD_IDENTITY),
  };
};

export const readReports = (directory, { cwd = process.cwd(), evidenceRoots = [] } = {}) => {
  let names;
  try { names = readdirSync(directory).filter((name) => name.endsWith('.json')); }
  catch (error) { if (error.code === 'ENOENT') return []; throw error; }
  return names.flatMap((name) => {
    const path = resolve(directory, name);
    try {
      const report = JSON.parse(readFileSync(path, 'utf8'));
      const root = resolve(cwd);
      if (!compactReport(report)) {
        const legacyPair = readLegacySiblingClosure({ path, report, root, directory, evidenceRoots });
        return [{ path, report, ...(legacyPair ?? {}) }];
      }
      const closurePath = report.durableClosurePath;
      if (isAbsolute(closurePath)) return [{ path, report }];
      const resolvedClosurePath = resolve(root, closurePath);
      const fromRoot = relative(root, resolvedClosurePath);
      if (!fromRoot || fromRoot === '..' || fromRoot.startsWith(`..${process.platform === 'win32' ? '\\' : '/'}`) || isAbsolute(fromRoot)) {
        return [{ path, report }];
      }
      const expectedClosureName = basename(name).replace(/-report\.json$/, '-closure.json');
      if (expectedClosureName === basename(name) || basename(resolvedClosurePath) !== expectedClosureName) {
        return [{ path, report }];
      }
      try {
        const realRoot = realpathSync(root);
        const realReportPath = realpathSync(path);
        const realClosurePath = realpathSync(resolvedClosurePath);
        const withinRoot = (candidate) => {
          const fromRoot = relative(realRoot, candidate);
          return fromRoot === '' || (fromRoot !== '..' && !fromRoot.startsWith(`..${sep}`) && !isAbsolute(fromRoot));
        };
        if (!withinRoot(realReportPath) || !withinRoot(realClosurePath)
          || dirname(realReportPath) !== dirname(realClosurePath)
          || basename(realClosurePath) !== expectedClosureName) return [{ path, report }];
        return [{ path, report, closure: JSON.parse(readFileSync(resolvedClosurePath, 'utf8')) }];
      } catch {
        return [{ path, report }];
      }
    }
    catch { return []; }
  });
};

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const directory = resolve(process.argv[2] ?? '.artifacts/loom-dev/verify-ui');
  const evidenceRoots = (process.env.LOOM_VERIFY_UI_EVIDENCE_ROOTS ?? '').split(process.platform === 'win32' ? ';' : ':').filter(Boolean);
  const rows = summarizeCoverage(registry, readReports(directory, { evidenceRoots }), currentCoverageBaseline());
  for (const row of rows) console.log(`${row.status}\t${row.freshness.status}\t${row.path}\t${row.report ?? '-'}`);
  const currentPasses = rows.filter((row) => row.status === 'passed' && row.freshness.status === 'current').length;
  const historicalPasses = rows.filter((row) => row.status === 'passed' && row.freshness.status === 'historical').length;
  const unknownFreshnessPasses = rows.filter((row) => row.status === 'passed' && row.freshness.status === 'unknown').length;
  console.log(`${currentPasses}/${rows.length} registered browser cases have passing report evidence for the current source and API build`);
  console.log(`${historicalPasses} historical passing reports preserved; ${unknownFreshnessPasses} passing reports have unknown freshness`);
  const gaps = registry.flatMap((scenario) => scenario.coverage
    .filter((feature) => !['implemented', 'passed'].includes(feature.status)
      || (requiresLifecycleAcceptance(feature) && !hasLifecycleContract(feature, scenario)))
    .map((feature) => `${scenario.id}\t${feature.feature}`));
  console.log(`${gaps.length} declared gaps or row-lifecycle claims lack a complete required-check contract`);
  for (const gap of gaps) console.log(`gap\t${gap}`);
}
