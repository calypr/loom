import test from 'node:test';
import assert from 'node:assert/strict';
import { classifyEvidence, classifyFreshness, summarizeCoverage } from '../coverage-status.mjs';
import { registry, scenarioCaseFor } from '../../registry.mjs';

const complete = Object.fromEntries(['usability', 'correctness', 'persistence', 'performance'].map((dimension) => [dimension, { status: 'passed' }]));
const fingerprint = (sha256 = 'a'.repeat(64), files = 12) => ({ sha256, files });
const apiBuildIdentity = '1'.repeat(64) + ':' + '2'.repeat(64) + ':' + '3'.repeat(64);
const freezeAssertion = (before, after = before, status = 'passed') => ({
  name: 'watched source stayed unchanged during browser run',
  status,
  evidence: { before, after },
});


test('registry case objects own the Playwright mapping and required checks and reject unknown cases', () => {
  const registeredCases = registry.flatMap((scenario) => Object.entries(scenario.cases));
  const totalRequiredChecks = registry.reduce((total, scenario) => total + Object.values(scenario.cases).reduce((scenarioTotal, contract) => {
    const checks = contract.requiredChecks;
    return scenarioTotal + (Array.isArray(checks) ? checks.length : Object.values(checks).reduce((n, variant) => n + variant.length, 0));
  }, 0), 0);
  assert.equal(registeredCases.length, 26);
  assert.equal(totalRequiredChecks, 396);
  assert.ok(registeredCases.every(([, contract]) => contract.playwrightTest && contract.requiredChecks));
  assert.throws(() => scenarioCaseFor('builder-load', 'unknown'), /unknown case for builder-load: unknown/);
  assert.throws(() => scenarioCaseFor('unknown-scenario', 'case'), /unknown scenario: unknown-scenario/);
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
