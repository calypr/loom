import test from 'node:test';
import assert from 'node:assert/strict';
import { classifyEvidence, classifyFreshness, summarizeCoverage } from '../coverage-status.mjs';

const complete = Object.fromEntries(['usability', 'correctness', 'persistence', 'performance'].map((dimension) => [dimension, { status: 'passed' }]));
const fingerprint = (sha256 = 'a'.repeat(64), files = 12) => ({ sha256, files });
const apiBuildIdentity = '1'.repeat(64) + ':' + '2'.repeat(64) + ':' + '3'.repeat(64);
const freezeAssertion = (before, after = before, status = 'passed') => ({
  name: 'watched source stayed unchanged during browser run',
  status,
  evidence: { before, after },
});

test('a scenario pass with untested dimensions is only partial evidence', () => {
  assert.equal(classifyEvidence({ status: 'passed', dimensions: { ...complete, persistence: { status: 'untested' } } }), 'partial');
  assert.equal(classifyEvidence({ status: 'passed', dimensions: complete }), 'passed');
});

test('current reports trust case requirements while keeping optional gaps visible', () => {
  assert.equal(classifyEvidence({ schemaVersion: 2, status: 'passed', dimensions: { ...complete, persistence: { status: 'untested' } } }), 'passed');
});

test('latest case result controls coverage and missing cases remain untested', () => {
  const scenarios = [{ id: 'builder', cases: ['load', 'edit'] }];
  const reports = [
    { path: 'old.json', report: { scenario: 'builder', case: 'load', finishedAt: '2026-01-01', status: 'passed', dimensions: complete } },
    { path: 'new.json', report: { scenario: 'builder', case: 'load', finishedAt: '2026-01-02', status: 'failed', dimensions: complete } },
  ];
  assert.deepEqual(summarizeCoverage(scenarios, reports).map(({ status, report }) => [status, report]), [['failed', 'new.json'], ['untested', null]]);
});

test('currentness requires exact source and API build identities while preserving report status', () => {
  const sourceFingerprint = fingerprint();
  const report = {
    schemaVersion: 2,
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
    assertions: [freezeAssertion(sourceFingerprint)],
  };
  const freshness = classifyFreshness(report, { sourceFingerprint, apiBuildIdentity });
  assert.deepEqual(freshness, { status: 'current', source: 'current', build: 'current' });
  assert.equal(classifyEvidence(report), 'passed');
});

test('mismatched source or API build identity is historical without changing pass status', () => {
  const sourceFingerprint = fingerprint();
  const report = {
    scenario: 'builder',
    case: 'load',
    finishedAt: '2026-10-01T12:00:00Z',
    schemaVersion: 2,
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
    assertions: [freezeAssertion(sourceFingerprint)],
  };
  const staleSource = classifyFreshness(report, { sourceFingerprint: fingerprint('b'.repeat(64)), apiBuildIdentity });
  const staleBuild = classifyFreshness(report, {
    sourceFingerprint,
    apiBuildIdentity: '4'.repeat(64) + ':' + '5'.repeat(64) + ':' + '6'.repeat(64),
  });
  assert.deepEqual(staleSource, { status: 'historical', source: 'historical', build: 'current' });
  assert.deepEqual(staleBuild, { status: 'historical', source: 'current', build: 'historical' });
  const rows = summarizeCoverage([{ id: 'builder', cases: ['load'] }], [{ path: 'pass.json', report }], { sourceFingerprint: fingerprint('b'.repeat(64)), apiBuildIdentity });
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
