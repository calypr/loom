#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { basename, isAbsolute, relative, resolve } from 'node:path';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const DEFAULT_OUTPUT = 'verification/builder-interactions/browser-evidence.json';

const args = process.argv.slice(2);
const valueFor = (name) => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
};
const reportPaths = [];
for (let index = 0; index < args.length; index += 1) {
  if (args[index] === '--report') reportPaths.push(args[++index]);
}
const output = valueFor('--output') ?? DEFAULT_OUTPUT;
const check = args.includes('--check');
const fail = (message) => {
  console.error('FAIL ' + message);
  process.exit(1);
};

if (reportPaths.length === 0) fail('usage: node scripts/generate-builder-browser-evidence.mjs --report <report.json> [--report <report.json>] [--output <path>] [--check]');

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const displayPath = (absolute) => {
  const path = relative(ROOT, absolute);
  return path.startsWith('..') ? basename(absolute) : path.replaceAll('\\', '/');
};
const normalizeRoute = (path) =>
  path
    .replace(/\?.*$/, '')
    .replace(/^\/api\/v1\/projects\/[^/]+/, '/api/v1/projects/{project}')
    .replace(/\/explorers\/[^/]+/, '/explorers/{explorerId}');
const routeKey = (method, path) => String(method).toUpperCase() + ' ' + normalizeRoute(String(path));

const cases = [];
const generatedFrom = [];
for (const reportPath of reportPaths) {
  const absolute = isAbsolute(reportPath) ? reportPath : resolve(process.cwd(), reportPath);
  if (!existsSync(absolute)) fail('browser report does not exist: ' + reportPath);
  const bytes = readFileSync(absolute);
  let report;
  try {
    report = JSON.parse(bytes.toString('utf8'));
  } catch (error) {
    fail('browser report is not valid JSON: ' + reportPath + ': ' + error.message);
  }
  if (!report || typeof report !== 'object') fail('browser report must be an object: ' + reportPath);
  if (report.status !== 'passed') fail(reportPath + ': browser report status is ' + report.status + ', expected passed');
  const scenario = report.scenario;
  const caseName = report.case;
  if (typeof scenario !== 'string' || scenario.length === 0) fail(reportPath + ': scenario is required');
  if (typeof caseName !== 'string' || caseName.length === 0) fail(reportPath + ': case is required');

  const source = report.target?.sourceFingerprint;
  if (!source || !/^[0-9a-f]{64}$/.test(source.sha256) || !Number.isInteger(source.files)) {
    fail(reportPath + ': target.sourceFingerprint must have a 64-character sha256 and an integer file count');
  }
  const apiBuildIdentity = report.target?.apiBuildIdentity;
  if (typeof apiBuildIdentity !== 'string' || !/^[0-9a-f]{64}:[0-9a-f]{64}:[0-9a-f]{64}$/.test(apiBuildIdentity)) {
    fail(reportPath + ': target.apiBuildIdentity must have three 64-character SHA-256 parts');
  }

  const nativeRequests = Array.isArray(report.nativeRequests) ? report.nativeRequests : [];
  const injectedRequests = Array.isArray(report.injectedRequests) ? report.injectedRequests : [];
  const coveredRoutes = [...new Set(nativeRequests
    .filter((request) => request.method && request.path)
    .map((request) => routeKey(request.method, request.path)))].sort();
  const injectedRoutes = [...new Set(injectedRequests
    .filter((request) => request.method && request.path)
    .map((request) => routeKey(request.method, request.path)))].sort();
  if (coveredRoutes.length === 0) fail(reportPath + ': report has no native API routes');

  const assertions = (Array.isArray(report.assertions) ? report.assertions : []).map((assertion) => ({
    name: assertion.name,
    status: assertion.status,
    dimension: assertion.dimension ?? null,
  }));
  const failedAssertions = assertions.filter((assertion) => assertion.status !== 'passed');
  if (failedAssertions.length > 0) {
    fail(reportPath + ': report has failed assertions: ' + failedAssertions.map((assertion) => assertion.name).join(', '));
  }

  const id = scenario + ':' + caseName;
  if (cases.some((entry) => entry.id === id)) fail('duplicate browser case id: ' + id);
  cases.push({
    id,
    scenario,
    case: caseName,
    name: scenario + ' ' + caseName,
    status: 'passed',
    recordedAt: report.finishedAt ?? report.startedAt ?? null,
    sourceFingerprint: source.sha256,
    sourceFileCount: source.files,
    apiBuildIdentity,
    coveredRoutes,
    injectedRoutes,
    assertions,
    report: displayPath(absolute),
    reportSha256: sha256(bytes),
  });
  generatedFrom.push(displayPath(absolute));
}

const payload = {
  schemaVersion: 2,
  generatedFrom: [...new Set(generatedFrom)].sort(),
  cases: cases.sort((left, right) => left.id.localeCompare(right.id)),
};
const serialized = JSON.stringify(payload, null, 2) + '\n';
const absoluteOutput = resolve(ROOT, output);
if (check) {
  if (!existsSync(absoluteOutput)) fail(output + ' does not exist; run the generator without --check');
  if (readFileSync(absoluteOutput, 'utf8') !== serialized) {
    fail(output + ' is stale; run node scripts/generate-builder-browser-evidence.mjs ' + reportPaths.map((path) => '--report ' + path).join(' '));
  }
  console.log('PASS ' + output + ' is current: ' + cases.length + ' browser case(s)');
} else {
  writeFileSync(absoluteOutput, serialized);
  console.log('wrote ' + output + ' with ' + cases.length + ' browser case(s) and ' +
    cases.reduce((sum, entry) => sum + entry.coveredRoutes.length, 0) + ' covered routes');
}
