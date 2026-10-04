import { readdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { registry } from './registry.mjs';
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

export const classifyEvidence = (report) => {
  if (report.status !== 'passed') return report.status;
  if (report.schemaVersion >= 2) return 'passed';
  return dimensions.every((dimension) => report.dimensions?.[dimension]?.status === 'passed')
    ? 'passed'
    : 'partial';
};

export const summarizeCoverage = (scenarios, reports, baseline = {}) => {
  const latest = new Map();
  for (const { path, report } of reports) {
    if (!report?.scenario || !report?.case || !report?.finishedAt) continue;
    const key = `${report.scenario}/${report.case}`;
    const previous = latest.get(key);
    if (!previous || report.finishedAt > previous.report.finishedAt) latest.set(key, { path, report });
  }
  return scenarios.flatMap((scenario) => scenario.cases.map((caseName) => {
    const evidence = latest.get(`${scenario.id}/${caseName}`);
    return {
      path: `${scenario.id}/${caseName}`,
      status: evidence ? classifyEvidence(evidence.report) : 'untested',
      freshness: evidence ? classifyFreshness(evidence.report, baseline) : { status: 'unknown', source: 'unknown', build: 'unknown' },
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

const readReports = (directory) => {
  let names;
  try { names = readdirSync(directory).filter((name) => name.endsWith('.json')); }
  catch (error) { if (error.code === 'ENOENT') return []; throw error; }
  return names.flatMap((name) => {
    const path = resolve(directory, name);
    try { return [{ path, report: JSON.parse(readFileSync(path, 'utf8')) }]; }
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
    .filter((feature) => feature.status !== 'implemented')
    .map((feature) => `${scenario.id}\t${feature.feature}`));
  console.log(`${gaps.length} declared feature gaps have no implemented browser coverage`);
  for (const gap of gaps) console.log(`gap\t${gap}`);
}
