import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { browserURL } from '../../workflows/builder-url.mjs';
import { assertPatientRows, patientOracle } from '../../workflows/builder-controls.mjs';
import { scenarioCaseFor } from '../../registry.mjs';
import { classifyEvidence, summarizeRenderCheckpoints } from '../coverage-status.mjs';

test('browserURL preserves unrelated query parameters and scopes the Builder route', () => {
  const result = new URL(browserURL(
    { uiUrl: 'https://loom.example/app?keep=yes#builder' }, 'project one', 'explorer/two', 'builder',
  ));
  assert.equal(result.origin, 'https://loom.example');
  assert.equal(result.searchParams.get('keep'), 'yes');
  assert.equal(result.searchParams.get('project'), 'project one');
  assert.equal(result.searchParams.get('explorer'), 'explorer/two');
  assert.equal(result.searchParams.get('mode'), 'builder');
  assert.equal(result.hash, '#builder');
});

test('first-table reload row oracle requires both exact independent fixture Patient IDs', () => {
  const fixtureDir = fileURLToPath(new URL('../../../../testdata/devloop-fixture', import.meta.url));
  const oracle = patientOracle({ fixtureDir });
  assert.deepEqual(oracle.ids, ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual(oracle.genderByID, { 'dev-patient-001': 'female', 'dev-patient-002': null });
  const reversedRows = [
    'dev-patient-002 Grace Builder',
    'dev-patient-001 Ada Example',
  ];
  assert.deepEqual(assertPatientRows(reversedRows, oracle.ids), oracle.ids,
    'rendered row order may vary while every full row must retain the exact independent ID');
  assert.throws(() => assertPatientRows([
    'dev-patient-001 Ada Example',
    'dev-patient-001 Ada Example',
  ], oracle.ids), /exact independent fixture Patient identities/,
  'duplicate rows cannot substitute for the missing second fixture Patient');
});

test('tables workflow compares exact fixture Gender values and preserves null as the empty-cell marker', () => {
  const fixtureDir = fileURLToPath(new URL('../../../../testdata/devloop-fixture', import.meta.url));
  const oracle = patientOracle({ fixtureDir });
  const headerRow = 'ROW\nPATIENT ID\nGENDER';
  const rows = ['1\ndev-patient-002\n—', '2\ndev-patient-001\nfemale'];
  assert.deepEqual(assertPatientRows(rows, oracle.ids, oracle.genderByID, headerRow), oracle.ids);
  assert.throws(() => assertPatientRows([
    '1\ndev-patient-002\n—', '2\ndev-patient-001\nmale',
  ], oracle.ids, oracle.genderByID, headerRow), /exact independent fixture Patient Gender values/,
  'a wrong value cannot pass by retaining the expected Patient IDs');
  assert.throws(() => assertPatientRows([
    '1\ndev-patient-002\nfemale', '2\ndev-patient-001\nfemale',
  ], oracle.ids, oracle.genderByID, headerRow), /exact independent fixture Patient Gender values/,
  'repeating one Patient Gender value cannot stand in for the source null');
  assert.throws(() => assertPatientRows([
    '1\ndev-patient-002\n—', '2\ndev-patient-001\nfemale', '3\ndev-patient-001\nfemale',
  ], oracle.ids, oracle.genderByID, headerRow), /exact independent fixture Patient identities/,
  'duplicate values cannot substitute for a complete exact Patient row set');
  assert.throws(() => assertPatientRows([
    '1\ndev-patient-002\nnull', '2\ndev-patient-001\nfemale',
  ], oracle.ids, oracle.genderByID, headerRow), /exact independent fixture Patient Gender values/,
  'the displayed empty-cell marker represents source null and literal text null is not equivalent');
  assert.throws(() => assertPatientRows(rows, oracle.ids, oracle.genderByID, 'ROW\nPATIENT ID\nSTATUS'),
    /must show the Gender column header/,
    'correct-looking values under another column cannot satisfy the source-value oracle');
});

test('first-table reload timing is a required performance check with the five-second limit', () => {
  const contract = scenarioCaseFor('builder-controls', 'first-table');
  const persistenceChecks = [
    'first Patient table remains selected after reload',
    'first Patient ID field survives reload',
    'reloaded Preview renders both exact independent fixture Patients',
  ];
  const performanceCheck = 'first-table reload-to-exact-rows within five seconds';
  for (const name of [...persistenceChecks, performanceCheck]) {
    assert.ok(contract.requiredChecks.includes(name), `registered first-table case requires ${name}`);
  }
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/builder-controls.spec.mjs');
  assert.equal(contract.playwrightGrep, 'Add columns waits for a current-draft preview');

  const reportFor = elapsedMs => ({
    schemaVersion: 2,
    status: 'passed',
    dimensions: {
      usability: { status: 'passed' },
      correctness: { status: 'passed' },
      persistence: { status: 'passed' },
      performance: { status: 'passed' },
    },
    assertions: contract.requiredChecks.map(name => ({
      name,
      status: 'passed',
      dimension: name === performanceCheck ? 'performance' : 'persistence',
      ...(name === performanceCheck ? { evidence: { elapsedMs, budgetMs: 5000 } } : {}),
    })),
  });
  const atBoundary = reportFor(5000);
  const summary = summarizeRenderCheckpoints(atBoundary, { requiredCheckNames: [performanceCheck] });
  assert.deepEqual(summary.checkpoints.map(({ durationMs, budgetMs }) => [durationMs, budgetMs]), [[5000, 5000]]);
  assert.equal(classifyEvidence(atBoundary, contract.requiredChecks, contract), 'passed');
  assert.equal(classifyEvidence(reportFor(5001), contract.requiredChecks, contract), 'failed',
    'a longer reload must not pass just because the case summary says passed');
});
