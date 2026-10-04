import { readdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { registry, requiredChecksFor } from './registry.mjs';

const dimensions = ['usability', 'correctness', 'persistence', 'performance'];

const registeredChecksFor = (scenario, caseName, report) => {
  try {
    return requiredChecksFor(scenario, caseName, report.target?.kind === 'read-only-custom');
  } catch (error) {
    if (error instanceof Error && error.message === `missing required checks for ${scenario.id}/${caseName}`) return undefined;
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

export const summarizeCoverage = (scenarios, reports) => {
  const latest = new Map();
  for (const { path, report } of reports) {
    if (!report?.scenario || !report?.case || !report?.finishedAt) continue;
    const key = `${report.scenario}/${report.case}`;
    const previous = latest.get(key);
    if (!previous || report.finishedAt > previous.report.finishedAt) latest.set(key, { path, report });
  }
  return scenarios.flatMap((scenario) => scenario.cases.map((caseName) => {
    const evidence = latest.get(`${scenario.id}/${caseName}`);
    const requiredChecks = evidence ? registeredChecksFor(scenario, caseName, evidence.report) : undefined;
    return {
      path: `${scenario.id}/${caseName}`,
      status: evidence ? classifyEvidence(evidence.report, requiredChecks) : 'untested',
      finishedAt: evidence?.report.finishedAt ?? null,
      report: evidence?.path ?? null,
    };
  }));
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
  const rows = summarizeCoverage(registry, readReports(directory));
  for (const row of rows) console.log(`${row.status}\t${row.path}\t${row.report ?? '-'}`);
  console.log(`${rows.filter((row) => row.status === 'passed').length}/${rows.length} registered browser cases have passing report evidence (historical reports use four dimensions; new reports use case requirements)`);
  const gaps = registry.flatMap((scenario) => scenario.coverage
    .filter((feature) => feature.status !== 'implemented')
    .map((feature) => `${scenario.id}\t${feature.feature}`));
  console.log(`${gaps.length} declared feature gaps have no implemented browser coverage`);
  for (const gap of gaps) console.log(`gap\t${gap}`);
}
