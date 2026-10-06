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
  for (const { path, report, closure } of reports) {
    if (!report?.scenario || !report?.case || (!report?.finishedAt && !compactReport(report))) continue;
    const key = `${report.scenario}/${report.case}`;
    const compact = compactReport(report);
    const requiredChecks = (() => {
      const scenario = scenarios.find((item) => item.id === report.scenario);
      return scenario ? registeredChecksFor(scenario, report.case, report) : undefined;
    })();
    const normalized = compact ? compactReportForCoverage(report, closure, requiredChecks) : report;
    const order = compact ? { kind: 'epoch', value: report.epoch } : { kind: 'time', value: report.finishedAt };
    const previous = latest.get(key);
    if (!previous) {
      latest.set(key, { path, report, normalized, compact, order });
      continue;
    }
    if (previous.ambiguousOrder) {
      latest.set(key, { ...previous, path: `${previous.path},${path}` });
      continue;
    }
    if (previous.order.kind !== order.kind) {
      latest.set(key, { path: `${previous.path},${path}`, report, normalized: null, compact: true, order, ambiguousOrder: true });
    } else if (order.value > previous.order.value) {
      latest.set(key, { path, report, normalized, compact, order });
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
          ? 'partial'
          : classifyEvidence(evidence.normalized ?? evidence.report, requiredChecks)
        : 'untested',
      freshness: evidence && !evidence.ambiguousOrder && evidence.normalized
        ? classifyFreshness(evidence.normalized, baseline)
        : { status: 'unknown', source: 'unknown', build: 'unknown' },
      finishedAt: evidence?.report.finishedAt ?? null,
      report: evidence?.path ?? null,
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

export const readReports = (directory, { cwd = process.cwd() } = {}) => {
  let names;
  try { names = readdirSync(directory).filter((name) => name.endsWith('.json')); }
  catch (error) { if (error.code === 'ENOENT') return []; throw error; }
  return names.flatMap((name) => {
    const path = resolve(directory, name);
    try {
      const report = JSON.parse(readFileSync(path, 'utf8'));
      if (!compactReport(report)) return [{ path, report }];
      const root = resolve(cwd);
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
  const rows = summarizeCoverage(registry, readReports(directory), currentCoverageBaseline());
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
