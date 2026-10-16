import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { playwrightDiscoveryCountIssues } from '../../lib/playwright-discovery-counts.mjs';
import { caseNamesFor, registry, scenarioCaseFor } from '../../verify-ui/registry.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const conversionLedger = JSON.parse(readFileSync(resolve(repositoryRoot, 'docs/verification/playwright/source-conversion-manifest.json'), 'utf8'));

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

test('conversion ledger preserves owned and distinct custom registry assertion contracts', () => {
  const scenario = registry.find(item => item.id === 'viewer-query');
  const owned = scenarioCaseFor(scenario, 'output');
  const custom = scenarioCaseFor(scenario, 'output', true);
  assert.notDeepEqual(custom.requiredChecks, owned.requiredChecks);
  const ledgerCase = conversionLedger.registryCases.find(item => item.scenario === 'viewer-query' && item.case === 'output');
  assert.ok(ledgerCase);
  assert.deepEqual(ledgerCase.preservedAssertions, owned.requiredChecks);
  assert.deepEqual(ledgerCase.customPreservedAssertions, custom.requiredChecks);

  const registryContracts = registry.flatMap(item => caseNamesFor(item).map(caseName => {
    const ownedCase = scenarioCaseFor(item, caseName);
    const customCase = scenarioCaseFor(item, caseName, true);
    return {
      scenario: item.id,
      case: caseName,
      preservedAssertions: ownedCase.requiredChecks,
      customPreservedAssertions: JSON.stringify(customCase.requiredChecks) === JSON.stringify(ownedCase.requiredChecks)
        ? null
        : customCase.requiredChecks,
    };
  }));
  assert.deepEqual(conversionLedger.registryCases.map(item => ({
    scenario: item.scenario,
    case: item.case,
    preservedAssertions: item.preservedAssertions,
    customPreservedAssertions: item.customPreservedAssertions ?? null,
  })), registryContracts);
  const totalEntries = registryContracts.reduce((total, item) => total + item.preservedAssertions.length + (item.customPreservedAssertions?.length ?? 0), 0);
  const customVariants = registryContracts.filter(item => item.customPreservedAssertions !== null).map(item => `${item.scenario}/${item.case}`);
  assert.deepEqual(customVariants, ['viewer-query/output']);
  assert.equal(conversionLedger.counts.registryRequiredCheckEntries, totalEntries);
  assert.equal(conversionLedger.counts.registryCustomAssertionVariants, customVariants.length);
});
