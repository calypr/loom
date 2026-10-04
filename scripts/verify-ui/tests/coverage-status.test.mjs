import test from 'node:test';
import assert from 'node:assert/strict';
import { classifyEvidence, summarizeCoverage } from '../coverage-status.mjs';

const complete = Object.fromEntries(['usability', 'correctness', 'persistence', 'performance'].map((dimension) => [dimension, { status: 'passed' }]));

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
