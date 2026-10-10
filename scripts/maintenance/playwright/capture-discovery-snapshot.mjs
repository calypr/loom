#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const repositoryRoot = resolve(scriptsRoot, '..');
const inputDirectory = resolve(process.argv[2] ?? '');
const outputPath = resolve(process.argv[3] ?? join(repositoryRoot, 'docs/verification/playwright/discovery.snapshot.json'));
if (!process.argv[2]) throw new Error('Usage: node capture-discovery-snapshot.mjs <directory-of-official-list-json-files> [output.json]');

const sha256 = value => createHash('sha256').update(value).digest('hex');
const fileHash = path => sha256(readFileSync(path));
const sessions = [
  { sessionID: 'main', input: 'main.json', configPath: 'scripts/playwright.config.mjs', rootDir: 'scripts/verify-ui/specs', environment: {} },
  { sessionID: 'construction-preview-bench', input: 'benchmark.json', configPath: 'scripts/measurements/construction-preview/construction-preview-bench.config.mjs', rootDir: 'scripts/measurements/construction-preview', environment: {} },
  { sessionID: 'collection-partial-long-route', input: 'collection-partial-long-route.json', configPath: 'scripts/playwright.config.mjs', rootDir: 'scripts/verify-ui/specs', environment: { LOOM_COLLECTION_PARTIAL_LONG_ROUTE: '1' } },
  { sessionID: 'related-one-all-basic-status', input: 'related-basic-status.json', configPath: 'scripts/playwright.config.mjs', rootDir: 'scripts/verify-ui/specs', environment: { LOOM_RELATED_ONE_ALL_MODE: 'basic', LOOM_RELATED_ONE_ALL_FIELD: 'status' } },
  { sessionID: 'related-one-all-cda-status', input: 'related-cda-status.json', configPath: 'scripts/playwright.config.mjs', rootDir: 'scripts/verify-ui/specs', environment: { LOOM_RELATED_ONE_ALL_MODE: 'cda', LOOM_RELATED_ONE_ALL_FIELD: 'status' } },
  { sessionID: 'related-one-all-cda-specimen-reference', input: 'related-cda-specimen-reference.json', configPath: 'scripts/playwright.config.mjs', rootDir: 'scripts/verify-ui/specs', environment: { LOOM_RELATED_ONE_ALL_MODE: 'cda', LOOM_RELATED_ONE_ALL_FIELD: 'specimen-reference' } },
];

function normalizeSession(definition) {
  const reportPath = join(inputDirectory, definition.input);
  const report = JSON.parse(readFileSync(reportPath, 'utf8'));
  const casesByPath = new Map();
  function visit(suites, inheritedFile = '') {
    for (const suite of suites ?? []) {
      const file = suite.file || inheritedFile;
      for (const spec of suite.specs ?? []) {
        const specFile = spec.file || file;
        if (!specFile || !spec.title || !spec.id) throw new Error(`${definition.sessionID}: list output contains a test without file/title/id`);
        const relativeSpec = specFile.startsWith('scripts/') ? specFile : join(definition.rootDir, specFile).split(sep).join('/');
        const cases = casesByPath.get(relativeSpec) ?? [];
        cases.push({ title: spec.title, id: spec.id, line: spec.line, column: spec.column,
          occurrence: cases.filter(item => item.title === spec.title).length + 1 });
        casesByPath.set(relativeSpec, cases);
      }
      visit(suite.suites, file);
    }
  }
  visit(report.suites);
  const cases = [...casesByPath.entries()].sort(([left], [right]) => left.localeCompare(right));
  const testCount = cases.reduce((total, [, rows]) => total + rows.length, 0);
  if (!testCount || report.errors?.length || report.stats?.expected !== 0 || report.stats?.unexpected !== 0 || report.stats?.skipped !== testCount) {
    throw new Error(`${definition.sessionID}: expected a clean --list-only report; got ${JSON.stringify({ testCount, errors: report.errors?.length, stats: report.stats })}`);
  }
  const specs = cases.map(([path, rows]) => ({ path, sha256: fileHash(join(repositoryRoot, path)), cases: rows }));
  const caseDigestInput = JSON.stringify(specs.map(({ path, cases: rows }) => ({ path, cases: rows })));
  const configFile = join(repositoryRoot, definition.configPath);
  return {
    sessionID: definition.sessionID,
    configPath: definition.configPath,
    configSha256: fileHash(configFile),
    environment: definition.environment,
    testCount,
    specFileCount: specs.length,
    discoveredCasesSha256: sha256(caseDigestInput),
    specs,
  };
}

const normalizedSessions = sessions.map(normalizeSession);
const main = normalizedSessions.find(session => session.sessionID === 'main');
const benchmark = normalizedSessions.find(session => session.sessionID === 'construction-preview-bench');
const relatedSessions = normalizedSessions.filter(item => item.sessionID.startsWith('related-one-all-'));
if (relatedSessions.length !== 3 || relatedSessions.some(item => item.testCount !== 1)) {
  throw new Error('Related ONE/ALL static variants must each contain exactly one discovered test');
}
const collectionPartial = normalizedSessions.find(item => item.sessionID === 'collection-partial-long-route');
if (collectionPartial?.testCount !== 1 || collectionPartial.specFileCount !== 1
  || collectionPartial.specs[0]?.path !== 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs'
  || collectionPartial.specs[0]?.cases[0]?.title !== 'remove an unmapped selected resource, verify the saved route, and reload') {
  throw new Error('Partial long-route collection static variant must contain its exact standalone CDA test');
}
const totalDeclaredTestCount = normalizedSessions.reduce((total, session) => total + session.testCount, 0);
const document = {
  schemaVersion: 1,
  kind: 'official-playwright-static-discovery',
  sourceRoot: '.',
  baseCommit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repositoryRoot, encoding: 'utf8' }).trim(),
  status: 'discovery-only',
  runtimeStatus: 'not-run',
  commandTemplate: '<repo>/scripts/node_modules/.bin/playwright test --config <configPath> --list --reporter=json',
  note: `Captured from ${normalizedSessions.length} official Playwright --list --reporter=json outputs. These reports list registrations only and skip every test body; no lifecycle ran. The ${normalizedSessions.length} session counts sum to ${totalDeclaredTestCount} entries.`,
  mainDiscoveryTestCount: main.testCount,
  mainDiscoverySpecFileCount: main.specFileCount,
  dedicatedBenchmarkTestCount: benchmark.testCount,
  dedicatedBenchmarkSpecFileCount: benchmark.specFileCount,
  totalDeclaredTestCount,
  sessions: normalizedSessions,
};
mkdirSync(dirname(outputPath), { recursive: true });
writeFileSync(outputPath, `${JSON.stringify(document, null, 2)}\n`);
console.log(JSON.stringify({ output: outputPath, baseCommit: document.baseCommit,
  sessions: normalizedSessions.map(({ sessionID, testCount, specFileCount }) => ({ sessionID, testCount, specFileCount })),
  totalDeclaredTestCount }, null, 2));
