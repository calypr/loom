import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { readReports, summarizeCoverage } from '../coverage-status.mjs';

const sourceFreezeCheck = 'watched source stayed unchanged during browser run';
const source = { sha256: 'a'.repeat(64), files: 12 };
const changedSource = { sha256: 'b'.repeat(64), files: 12 };
const apiBuildIdentity = `${'1'.repeat(64)}:${'2'.repeat(64)}:${'3'.repeat(64)}`;

const writeCompactFixture = (root) => {
  const scenario = {
    id: 'coverage-reader-freshness',
    cases: {
      'source-freeze': {
        playwrightTest: 'coverage-status-compact-freshness.test.mjs',
        requiredChecks: [sourceFreezeCheck],
      },
    },
  };
  const directory = join(root, 'docs/verification/playwright/runtime');
  mkdirSync(directory, { recursive: true });
  const stem = 'source-freeze-epoch142';
  const reportPath = join(directory, `${stem}-report.json`);
  const closurePath = join(directory, `${stem}-closure.json`);
  const target = {
    project: 'coverage-reader-fixture',
    composeProject: 'coverage-reader-fixture',
    generation: 'fixture-v1',
    sourceRoot: root,
  };
  writeFileSync(reportPath, JSON.stringify({
    epoch: 142,
    scenario: scenario.id,
    case: 'source-freeze',
    status: 'passed',
    runnerStatus: 'passed',
    coverageStatus: 'passed',
    requiredChecks: { passed: 1, total: 1 },
    target: { ...target, sourceFingerprint: source, apiBuildIdentity },
    integrity: {
      closureStatus: 'PASS',
      sourceBeforeAfter: {
        before: source,
        after: source,
        manifestsEqual: true,
        changedPaths: [],
        unchanged: true,
      },
      apiBuildIdentityUnchanged: true,
      ownedMounts: { before: 'PASS', after: 'PASS', targetUnchanged: true },
      health: { before: { status: 'PASS' }, after: { status: 'PASS' } },
    },
    durableClosurePath: `docs/verification/playwright/runtime/${stem}-closure.json`,
  }));
  writeFileSync(closurePath, JSON.stringify({
    epoch: 142,
    status: 'CLOSED_PASS',
    integrityClosure: {
      status: 'PASS',
      source: { before: source, after: source, manifestsEqual: true, changedPaths: [] },
      apiBuildIdentity: {
        before: apiBuildIdentity,
        after: apiBuildIdentity,
        precheck: apiBuildIdentity,
        unchanged: true,
      },
      ownedMounts: { before: 'PASS', after: 'PASS', targetUnchanged: true, target },
      health: { before: { status: 'PASS' }, after: { status: 'PASS' } },
    },
    case: {
      scenarioId: scenario.id,
      caseName: 'source-freeze',
      status: 'passed',
      requiredChecks: {
        passed: 1,
        total: 1,
        missingOrFailed: 0,
        evidence: [{ name: sourceFreezeCheck, status: 'passed' }],
      },
    },
  }));
  return { scenario, directory, reportPath };
};

test('compact required source-freeze evidence carries freshness through the coverage reader', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-compact-freshness-'));
  try {
    const fixture = writeCompactFixture(root);
    const reports = readReports(fixture.directory, { cwd: root });
    assert.ok(reports.find(({ path }) => path === fixture.reportPath)?.closure,
      'readReports pairs the compact report with its durable closure');
    const summarize = (baseline) => summarizeCoverage([fixture.scenario], reports, baseline)[0];

    const current = summarize({ sourceFingerprint: source, apiBuildIdentity });
    assert.equal(current.status, 'passed');
    assert.deepEqual(current.freshness, { status: 'current', source: 'current', build: 'current' });

    const historical = summarize({ sourceFingerprint: changedSource, apiBuildIdentity });
    assert.deepEqual(historical.freshness, { status: 'historical', source: 'historical', build: 'current' });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
