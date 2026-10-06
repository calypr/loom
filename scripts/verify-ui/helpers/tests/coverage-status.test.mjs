import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, renameSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';
import { classifyEvidence, classifyFreshness, readReports, summarizeCoverage } from '../coverage-status.mjs';
import { caseNamesFor, registry, scenarioCaseFor } from '../../registry.mjs';

const complete = Object.fromEntries(['usability', 'correctness', 'persistence', 'performance'].map((dimension) => [dimension, { status: 'passed' }]));
const fingerprint = (sha256 = 'a'.repeat(64), files = 12) => ({ sha256, files });
const apiBuildIdentity = '1'.repeat(64) + ':' + '2'.repeat(64) + ':' + '3'.repeat(64);
const freezeAssertion = (before, after = before, status = 'passed') => ({
  name: 'watched source stayed unchanged during browser run',
  status,
  evidence: { before, after },
});


test('every registry case resolves its Playwright mapping and owned/custom checks', () => {
  const registeredCases = registry.flatMap((scenario) => Object.entries(scenario.cases).map(([caseName, contract]) => ({ scenario, caseName, contract })));
  const resolvedCases = registry.flatMap((scenario) => caseNamesFor(scenario).map((caseName) => ({
    scenario,
    caseName,
    owned: scenarioCaseFor(scenario, caseName),
    custom: scenarioCaseFor(scenario, caseName, true),
  })));
  assert.equal(resolvedCases.length, registeredCases.length);
  assert.deepEqual(resolvedCases.map(({ scenario, caseName }) => `${scenario.id}/${caseName}`), registeredCases.map(({ scenario, caseName }) => `${scenario.id}/${caseName}`));
  const rawCheckEntries = registeredCases.reduce((total, { contract }) => {
    const checks = contract.requiredChecks;
    if (Array.isArray(checks)) return total + checks.length;
    const customChecks = checks.custom ?? checks.owned;
    return total + checks.owned.length + (JSON.stringify(customChecks) === JSON.stringify(checks.owned) ? 0 : customChecks.length);
  }, 0);
  const resolvedCheckEntries = resolvedCases.reduce((total, { owned, custom }) => total + owned.requiredChecks.length + (
    JSON.stringify(custom.requiredChecks) === JSON.stringify(owned.requiredChecks) ? 0 : custom.requiredChecks.length
  ), 0);
  assert.equal(resolvedCheckEntries, rawCheckEntries);
  for (const { scenario, caseName, contract } of registeredCases) {
    const checks = contract.requiredChecks;
    const ownedChecks = Array.isArray(checks) ? checks : checks.owned;
    const customChecks = Array.isArray(checks) ? checks : (checks.custom ?? checks.owned);
    assert.ok(contract.playwrightTest, `${scenario.id}/${caseName} has a native Playwright mapping`);
    assert.deepEqual(scenarioCaseFor(scenario, caseName).requiredChecks, ownedChecks);
    assert.deepEqual(scenarioCaseFor(scenario, caseName, true).requiredChecks, customChecks);
  }
  assert.throws(() => scenarioCaseFor('builder-load', 'unknown'), /unknown case for builder-load: unknown/);
  assert.throws(() => scenarioCaseFor('unknown-scenario', 'case'), /unknown scenario: unknown-scenario/);
});

test('partial long-route collection repair owns one registered case while legacy variants stay unregistered', () => {
  const scenario = registry.find((entry) => entry.id === 'cda-collection-repair-partial');
  assert.ok(scenario, 'the exact partial long-route variant has a registry contract');
  const contract = scenarioCaseFor(scenario, 'partial-long-route-repair-and-reload');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs');
  assert.equal(contract.requiredChecks.length, 11);
  assert.equal(new Set(contract.requiredChecks).size, contract.requiredChecks.length);
  assert.equal(registry.some((entry) => entry.id === 'cda-collection-repair'), false,
    'legacy default and long-route reports retain their previously unregistered scenario identity');
  assert.throws(() => scenarioCaseFor(scenario, 'long-route-repair-and-reload'), /unknown case/);
  assert.throws(() => scenarioCaseFor(scenario, 'unmapped-parent-repair-and-reload'), /unknown case/);
});

test('a scenario pass with untested dimensions is only partial evidence', () => {
  assert.equal(classifyEvidence({ status: 'passed', dimensions: { ...complete, persistence: { status: 'untested' } } }), 'partial');
  assert.equal(classifyEvidence({ status: 'passed', dimensions: complete }), 'passed');
});

test('current reports use passing named requirements while keeping optional dimension gaps out of case status', () => {
  assert.equal(classifyEvidence({
    schemaVersion: 2,
    status: 'passed',
    assertions: [{ name: 'required transition', status: 'passed' }],
    dimensions: { ...complete, persistence: { status: 'untested' } },
  }, ['required transition']), 'passed');
});

test('latest case result controls coverage and missing cases remain untested', () => {
  const scenarios = [{ id: 'builder', cases: { load: { playwrightTest: 'builder.spec.mjs', requiredChecks: ['load'] }, edit: { playwrightTest: 'builder.spec.mjs', requiredChecks: ['edit'] } } }];
  const reports = [
    { path: 'old.json', report: { scenario: 'builder', case: 'load', finishedAt: '2026-01-01', status: 'passed', dimensions: complete } },
    { path: 'new.json', report: { scenario: 'builder', case: 'load', finishedAt: '2026-01-02', status: 'failed', dimensions: complete } },
  ];
  assert.deepEqual(summarizeCoverage(scenarios, reports).map(({ status, report }) => [status, report]), [['failed', 'new.json'], ['untested', null]]);
});

const groupScenario = registry.find((scenario) => scenario.id === 'builder-authoring');
assert(Object.hasOwn(groupScenario?.cases ?? {}, 'group-entry'), 'Expected the registered direct Group entry case.');
const groupEntryChecks = scenarioCaseFor(groupScenario, 'group-entry').requiredChecks;

const summarizeGroupEntry = (assertions) => summarizeCoverage([groupScenario], [{
  path: 'builder-authoring-group-entry.json',
  report: {
    schemaVersion: 2,
    scenario: 'builder-authoring',
    case: 'group-entry',
    finishedAt: '2026-10-04T00:00:00.000Z',
    status: 'passed',
    target: { kind: 'owned-dev-fixture' },
    requiredChecks: groupEntryChecks,
    assertions,
  },
}]).find((entry) => entry.path === 'builder-authoring/group-entry');

test('the real group-entry report shape passes only with every registered named assertion', () => {
  const assertions = groupEntryChecks.map((name) => ({ name, status: 'passed' }));
  assert.equal(summarizeGroupEntry(assertions)?.status, 'passed');
});

test('a current report missing a registered named assertion is partial despite its passed summary', () => {
  const assertions = groupEntryChecks.slice(0, -1).map((name) => ({ name, status: 'passed' }));
  assert.equal(summarizeGroupEntry(assertions)?.status, 'partial');
});

test('a current report with a failed registered named assertion cannot be classified as passed', () => {
  const assertions = groupEntryChecks.map((name, index) => ({ name, status: index === 0 ? 'failed' : 'passed' }));
  assert.equal(summarizeGroupEntry(assertions)?.status, 'failed');
});

test('currentness requires exact source and API build identities while preserving report status', () => {
  const sourceFingerprint = fingerprint();
  const report = {
    schemaVersion: 2,
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
    assertions: [freezeAssertion(sourceFingerprint), { name: 'required transition', status: 'passed' }],
  };
  const freshness = classifyFreshness(report, { sourceFingerprint, apiBuildIdentity });
  assert.deepEqual(freshness, { status: 'current', source: 'current', build: 'current' });
  assert.equal(classifyEvidence(report, ['required transition']), 'passed');
});

test('mismatched source or API build identity is historical without changing pass status', () => {
  const sourceFingerprint = fingerprint();
  const report = {
    scenario: 'builder-load',
    case: 'list',
    finishedAt: '2026-10-01T12:00:00Z',
    schemaVersion: 2,
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
    assertions: [
      ...scenarioCaseFor(registry.find((scenario) => scenario.id === 'builder-load'), 'list').requiredChecks
        .map((name) => ({ name, status: 'passed' })),
      freezeAssertion(sourceFingerprint),
    ],
  };
  const staleSource = classifyFreshness(report, { sourceFingerprint: fingerprint('b'.repeat(64)), apiBuildIdentity });
  const staleBuild = classifyFreshness(report, {
    sourceFingerprint,
    apiBuildIdentity: '4'.repeat(64) + ':' + '5'.repeat(64) + ':' + '6'.repeat(64),
  });
  assert.deepEqual(staleSource, { status: 'historical', source: 'historical', build: 'current' });
  assert.deepEqual(staleBuild, { status: 'historical', source: 'current', build: 'historical' });
  const scenario = registry.find((entry) => entry.id === report.scenario);
  const rows = summarizeCoverage([scenario], [{ path: 'pass.json', report }], { sourceFingerprint: fingerprint('b'.repeat(64)), apiBuildIdentity });
  assert.equal(rows[0].status, 'passed');
  assert.deepEqual(rows[0].freshness, { status: 'historical', source: 'historical', build: 'current' });
});

test('missing report identity or missing current baseline stays unknown, never current', () => {
  const sourceFingerprint = fingerprint();
  const report = {
    status: 'passed',
    target: { sourceFingerprint },
    assertions: [freezeAssertion(sourceFingerprint)],
  };
  assert.deepEqual(classifyFreshness(report, { sourceFingerprint, apiBuildIdentity }), {
    status: 'unknown', source: 'current', build: 'unknown',
  });
  assert.deepEqual(classifyFreshness({
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
  }, { sourceFingerprint, apiBuildIdentity }), {
    status: 'unknown', source: 'unknown', build: 'current',
  });
  assert.deepEqual(classifyFreshness({
    ...report,
    apiBuildIdentity,
  }, { apiBuildIdentity }), {
    status: 'unknown', source: 'unknown', build: 'current',
  });
});

test('a source fingerprint that changed during the report is historical even when its start matched', () => {
  const sourceFingerprint = fingerprint();
  const changedFingerprint = fingerprint('b'.repeat(64));
  const freshness = classifyFreshness({
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
    assertions: [freezeAssertion(sourceFingerprint, changedFingerprint, 'failed')],
  }, { sourceFingerprint, apiBuildIdentity });
  assert.deepEqual(freshness, { status: 'historical', source: 'historical', build: 'current' });
});

const writeCompactRun = (root, { epoch = 78, source = fingerprint(), build = apiBuildIdentity, status = 'passed', integritySource = source } = {}) => {
  const scenario = registry.find((entry) => entry.id === 'cda-current-draft-upstream-append');
  const required = scenarioCaseFor(scenario, 'upstream-append').requiredChecks;
  const reportDir = join(root, 'docs/verification/playwright/runtime');
  mkdirSync(reportDir, { recursive: true });
  const stem = `upstream-append-epoch${epoch}`;
  const reportPath = join(reportDir, `${stem}-report.json`);
  const closurePath = join(reportDir, `${stem}-closure.json`);
  const target = { project: 'loom_dev_cda_fhir', composeProject: 'loom-test-compose', generation: 'cda-fhir-v1', sourceRoot: root };
  const report = {
    epoch,
    scenario: scenario.id,
    case: 'upstream-append',
    title: 'durable compact report fixture',
    status,
    runnerStatus: status,
    coverageStatus: status,
    requiredChecks: { passed: required.length, failed: 0, total: required.length },
    assertions: { passed: 215, failed: 0, total: 215 },
    dimensions: { usability: 'passed', correctness: 'passed', persistence: 'passed', performance: 'passed' },
    network: { unexpectedNetworkErrors: 0, domainErrors: 0 },
    target,
    integrity: {
      closureStatus: 'PASS',
      sourceBeforeAfter: { ...integritySource, unchanged: true },
      apiBuildIdentityUnchanged: true,
      ownedMounts: { before: 'PASS', after: 'PASS', targetUnchanged: true },
      health: { before: { status: 'PASS', samples: 3 }, after: { status: 'PASS', samples: 3 } },
    },
    durableClosurePath: `docs/verification/playwright/runtime/${stem}-closure.json`,
  };
  const closure = {
    epoch,
    status: 'CLOSED_PASS',
    integrityClosure: {
      status: 'PASS',
      source: { before: source, after: source, manifestsEqual: true, changedPaths: [] },
      apiBuildIdentity: { before: build, after: build, precheck: build, unchanged: true },
      ownedMounts: { before: 'PASS', after: 'PASS', targetUnchanged: true, target },
      health: { before: { status: 'PASS', samples: 3 }, after: { status: 'PASS', samples: 3 } },
    },
    case: {
      scenarioId: scenario.id,
      caseName: 'upstream-append',
      status: 'passed',
      requiredChecks: {
        passed: required.length,
        total: required.length,
        missingOrFailed: 0,
        evidence: required.map((name) => ({ name, status: 'passed' })),
      },
    },
  };
  writeFileSync(reportPath, JSON.stringify(report));
  writeFileSync(closurePath, JSON.stringify(closure));
  return { reportPath, closurePath, report, closure, scenario, required };
};

test('durable compact report plus matching closure contributes current or historical evidence only against exact baselines', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-compact-'));
  try {
    const source = fingerprint('c'.repeat(64), 1518);
    const fixture = writeCompactRun(root, { source });
    const reports = readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root });
    const loaded = reports.find((entry) => entry.path === fixture.reportPath);
    assert.ok(loaded?.closure, 'reader pairs the report with its repo-relative durable closure');
    const baseline = { sourceFingerprint: source, apiBuildIdentity };
    const summarize = (current) => summarizeCoverage([fixture.scenario], reports, current)[0];

    assert.deepEqual(
      (({ status, freshness }) => ({ status, freshness }))(summarize(baseline)),
      { status: 'passed', freshness: { status: 'current', source: 'current', build: 'current' } },
    );
    assert.deepEqual(summarize({ sourceFingerprint: fingerprint('d'.repeat(64), 1518), apiBuildIdentity }).freshness,
      { status: 'historical', source: 'historical', build: 'current' });
    assert.deepEqual(summarize({ sourceFingerprint: source, apiBuildIdentity: '4'.repeat(64) + ':' + '5'.repeat(64) + ':' + '6'.repeat(64) }).freshness,
      { status: 'historical', source: 'current', build: 'historical' });
    assert.deepEqual(summarize({ sourceFingerprint: source }).freshness,
      { status: 'unknown', source: 'current', build: 'unknown' });

    const mixedFormat = summarizeCoverage([fixture.scenario], [
      ...reports,
      {
        path: 'later-full-report.json',
        report: {
          scenario: fixture.scenario.id,
          case: 'upstream-append',
          finishedAt: '2026-10-06T12:00:00.000Z',
          schemaVersion: 2,
          status: 'failed',
          assertions: [],
        },
      },
    ], baseline)[0];
    assert.equal(mixedFormat.status, 'partial');
    assert.equal(mixedFormat.freshness.status, 'unknown', 'incomparable timestamp and epoch ordering cannot claim current coverage');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('a compact report with a mismatched closure is partial and never current', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-compact-mismatch-'));
  try {
    const source = fingerprint('e'.repeat(64), 1518);
    const fixture = writeCompactRun(root, { source });
    fixture.closure.epoch += 1;
    writeFileSync(fixture.closurePath, JSON.stringify(fixture.closure));
    const reports = readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root });
    const row = summarizeCoverage([fixture.scenario], reports, { sourceFingerprint: source, apiBuildIdentity })[0];
    assert.equal(row.status, 'partial');
    assert.deepEqual(row.freshness, { status: 'unknown', source: 'unknown', build: 'unknown' });

    rmSync(fixture.closurePath);
    const missingClosureReports = readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root });
    const missingClosure = summarizeCoverage([fixture.scenario], missingClosureReports, { sourceFingerprint: source, apiBuildIdentity })[0];
    assert.equal(missingClosure.status, 'partial');
    assert.equal(missingClosure.freshness.status, 'unknown');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('compact reports cannot override contradictory integrity summaries or resolve a nonsibling closure', () => {
  const source = fingerprint('f'.repeat(64), 1518);
  const build = apiBuildIdentity;
  const rejectedRow = (fixture, root) => summarizeCoverage(
    [fixture.scenario],
    readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root }),
    { sourceFingerprint: source, apiBuildIdentity: build },
  )[0];
  const assertRejected = (mutate) => {
    const root = mkdtempSync(join(tmpdir(), 'coverage-compact-contradiction-'));
    try {
      const fixture = writeCompactRun(root, { source, build });
      mutate(fixture, root);
      const row = rejectedRow(fixture, root);
      assert.equal(row.status, 'partial');
      assert.deepEqual(row.freshness, { status: 'unknown', source: 'unknown', build: 'unknown' });
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  };

  assertRejected(({ report, reportPath }) => {
    report.target.composeProject = 'different-compose-project';
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.target.composeProject = '';
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.target.sourceRoot = '';
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.integrity.apiBuildIdentityUnchanged = true;
    report.integrity.apiBuildIdentity = {
      before: '0'.repeat(64) + ':' + '2'.repeat(64) + ':' + '3'.repeat(64),
      after: build,
      unchanged: true,
    };
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.integrity.sourceBeforeAfter.manifestsEqual = false;
    report.integrity.sourceBeforeAfter.changedPaths = ['internal/server/example.go'];
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.integrity.sourceBeforeAfter.changedPaths = { length: 0 };
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ closure, closurePath }) => {
    closure.integrityClosure.source.manifestsEqual = 'false';
    writeFileSync(closurePath, JSON.stringify(closure));
  });
  assertRejected(({ closure, closurePath }) => {
    closure.integrityClosure.source.changedPaths = { length: 0 };
    writeFileSync(closurePath, JSON.stringify(closure));
  });
  assertRejected(({ closure, closurePath }) => {
    closure.integrityClosure.apiBuildIdentity.unchanged = 'false';
    writeFileSync(closurePath, JSON.stringify(closure));
  });
  assertRejected(({ closure, closurePath }) => {
    closure.integrityClosure.ownedMounts.targetUnchanged = 'false';
    writeFileSync(closurePath, JSON.stringify(closure));
  });
  assertRejected(({ closurePath }, root) => {
    const alternateDirectory = join(root, 'alternate');
    mkdirSync(alternateDirectory);
    const redirected = join(alternateDirectory, basename(closurePath));
    renameSync(closurePath, redirected);
    symlinkSync(redirected, closurePath);
  });
});
