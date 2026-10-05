import assert from 'node:assert/strict';
import { test } from 'node:test';
import { playwrightDiscoveryCountIssues } from './lib/playwright-discovery-counts.mjs';

const spec = (...titles) => ({ cases: titles.map(title => ({ title })) });
const fixture = () => ({
  sessions: [
    { sessionID: 'main', testCount: 2, specFileCount: 1, specs: [spec('case one', 'case two')] },
    { sessionID: 'construction-preview-bench', testCount: 1, specFileCount: 1, specs: [spec('benchmark')] },
    { sessionID: 'related-one-all-basic-status', testCount: 1, specFileCount: 1, specs: [spec('related basic')] },
    { sessionID: 'related-one-all-cda-status', testCount: 1, specFileCount: 1, specs: [spec('related CDA')] },
    { sessionID: 'related-one-all-cda-specimen-reference', testCount: 1, specFileCount: 1, specs: [spec('related specimen')] },
  ],
  mainDiscoveryTestCount: 2,
  mainDiscoverySpecFileCount: 1,
  dedicatedBenchmarkTestCount: 1,
  dedicatedBenchmarkSpecFileCount: 1,
  totalDeclaredTestCount: 6,
});

test('literal five-session case arrays reconcile with all summary counts', () => {
  assert.deepEqual(playwrightDiscoveryCountIssues(fixture()), []);
});

test('pre-correction main and all-session totals are rejected against session arrays', () => {
  const snapshot = fixture();
  snapshot.mainDiscoveryTestCount = 155;
  snapshot.mainDiscoverySpecFileCount = 22;
  snapshot.totalDeclaredTestCount = 156;
  assert.deepEqual(playwrightDiscoveryCountIssues(snapshot), [
    'discovery snapshot summary count drift: main session totals must match its literal spec case arrays',
    'discovery snapshot total count drift: totalDeclaredTestCount must equal actual case arrays from every session',
  ]);
});

test('each related session count is checked against its literal case array', () => {
  const snapshot = fixture();
  snapshot.sessions.find(session => session.sessionID === 'related-one-all-cda-status').testCount = 0;
  assert.deepEqual(playwrightDiscoveryCountIssues(snapshot), [
    'discovery case/spec count drift: related-one-all-cda-status; session counts must match its literal spec case arrays',
  ]);
});

test('benchmark top-level counts are reconciled separately', () => {
  const snapshot = fixture();
  snapshot.dedicatedBenchmarkTestCount = 0;
  snapshot.dedicatedBenchmarkSpecFileCount = 0;
  assert.deepEqual(playwrightDiscoveryCountIssues(snapshot), [
    'discovery snapshot summary count drift: construction-preview-bench session totals must match its literal spec case arrays',
  ]);
});

test('duplicate session ids are rejected and still contribute all literal cases to the total', () => {
  const snapshot = fixture();
  snapshot.sessions.push({ sessionID: 'main', testCount: 1, specFileCount: 1, specs: [spec('duplicate id case')] });
  assert.deepEqual(playwrightDiscoveryCountIssues(snapshot), [
    'duplicate discovery session id: main',
    'discovery snapshot total count drift: totalDeclaredTestCount must equal actual case arrays from every session',
  ]);
});
