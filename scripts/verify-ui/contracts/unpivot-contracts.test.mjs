import assert from 'node:assert/strict';
import test from 'node:test';
import {
  assertClosedReport,
  assertMeltEvidence,
  assertNoUncaughtPageExceptions,
  assertRequiredChecks,
  assertUnpivotMeltPreview,
  buildUnpivotOracle,
  UNPIVOT_KEYS,
  UNPIVOT_REQUIRED_CHECKS,
  UNPIVOT_SCREENSHOT_FLOOR,
} from './unpivot-contracts.mjs';

function observations() {
  return Array.from({ length: 1006 }, (_, index) => ({
    id: `dev-observation-${String(index).padStart(4, '0')}`,
    status: 'final',
    valueQuantity: index === 1 ? undefined : { unit: index === 0 ? 'cm' : 'kg' },
    subject: index === 1 ? undefined : { reference: `Patient/${index}` },
    valueInteger: index,
    effectiveDateTime: `2025-01-${String(index % 28 + 1).padStart(2, '0')}`,
  }));
}

function validPreview(rawObservations) {
  const headers = ['Numberinteger', 'Datedate_time', 'Variable', 'Value'];
  const rows = [];
  for (let index = 0; index < 5; index += 1) {
    const source = rawObservations[index];
    const anchor = String(source.valueInteger);
    const date = String(source.effectiveDateTime);
    rows.push([anchor, date, UNPIVOT_KEYS.id, source.id]);
    rows.push([anchor, date, UNPIVOT_KEYS.unit, source.valueQuantity?.unit ?? '\u2014']);
    rows.push([anchor, date, UNPIVOT_KEYS.subject, source.subject?.reference ?? '']);
  }
  return { headers, rows };
}

function validReport() {
  return {
    status: 'passed',
    schemaVersion: 1,
    mode: 'raw-cdp-unpivot-row-source-journey',
    checks: UNPIVOT_REQUIRED_CHECKS.map((name) => ({ name, status: 'passed', evidence: {} })),
    screenshots: Array.from({ length: UNPIVOT_SCREENSHOT_FLOOR }, (_, index) => `leg-${index}.png`),
    exceptions: [],
  };
}

function assertContractFailure(run, assertionId) {
  assert.throws(run, (error) => error.assertionId === assertionId,
    `expected the ${assertionId} contract to reject the observation`);
}

test('retains the exact required check set and screenshot floor', () => {
  assert.deepEqual(UNPIVOT_REQUIRED_CHECKS, [
    'step-0-behavior-capabilities', 'discovery-hides-date-family', 'step-1-prelude-filter-step', 'step-2-discovery', 'step-3-configure-oracle-preview-preserve',
    'step-4-cancel-byte-stable', 'step-5-apply-exact-rows', 'step-6-reload-materialized-echo', 'step-7-edit-rename-and-drop',
    'step-8-cleared-melt-lane-stays-cleared', 'step-9-re-add-honest-rematerialization', 'step-10-remove-clean', 'date-column-survives-string-melt',
    'negative-C0-public-control', 'negative-C1-signed-decorates', 'negative-C2-forged', 'negative-C3-cross-snapshot',
    'negative-C4-tampered', 'negative-C5-empty-choiceId', 'negative-C6-missing-choiceId', 'negative-C7-duplicate',
    'negative-C8-column-mismatch', 'negative-C9-over-selection', 'negative-C10-types-incompatible',
    'negative-C11-already-projected-not-500', 'negative-C12-payloadless-requires-unpivot',
    'contract-C13-non-melt-step-undecorated', 'negative-C14-new-carrier-stale-stage', 'negative-C15-signed-date-melt-refused',
    'blank-and-exact-melt-evidence',
  ]);
  assert.equal(UNPIVOT_SCREENSHOT_FLOOR, 13);
});

test('builds the browser oracle from raw observations and preserves its fixture floors', () => {
  const oracle = buildUnpivotOracle(observations());
  assert.equal(oracle.rows.length, 1006);
  assert.equal(oracle.byId.get('dev-observation-0000')?.unit, 'cm');
  assert.equal(oracle.unitBlankCount, 1);
  assert.equal(oracle.subjectBlankCount, 1);
  assert.deepEqual(oracle.effectiveValues.slice(0, 2), ['2025-01-01', '2025-01-02']);
});

test('rejects empty and row-changing fixture inputs with the oracle assertion ID', () => {
  assertContractFailure(() => buildUnpivotOracle([]), 'unpivot.oracle.source');
  const changed = observations();
  changed[0].status = 'amended';
  assertContractFailure(() => buildUnpivotOracle(changed), 'unpivot.oracle.source');
});

test('accepts rendered preview rows against the same raw-data oracle used by the workflow', () => {
  const raw = observations();
  const oracle = buildUnpivotOracle(raw);
  const result = assertUnpivotMeltPreview(validPreview(raw), 'offline contract',
    [UNPIVOT_KEYS.id, UNPIVOT_KEYS.unit, UNPIVOT_KEYS.subject], oracle);
  assert.equal(result.rows.length, 15);
  assert.ok(result.evidence.blankMeltCells > 0);
  assert.ok(result.evidence.exactMeltCells > 0);
  assert.equal(result.evidence.idMeltCells, 5);
  assert.equal(result.evidence.dateSurvivalCells, 15);
});

test('rejects zero rows and an oracle value mutant through the live preview contract', () => {
  const raw = observations();
  const oracle = buildUnpivotOracle(raw);
  const activeKeys = [UNPIVOT_KEYS.id, UNPIVOT_KEYS.unit, UNPIVOT_KEYS.subject];
  assert.throws(
    () => assertUnpivotMeltPreview({ ...validPreview(raw), rows: [] }, 'zero preview', activeKeys, oracle),
    (error) => error.assertionId === 'unpivot.preview.oracle' && /zero rows matched/u.test(error.message),
    'the real preview contract must reject vacuous zero-row observations with its stable assertion ID',
  );

  const mutated = validPreview(raw);
  mutated.rows[1][3] = 'ZZZ';
  assert.throws(
    () => assertUnpivotMeltPreview(mutated, 'oracle mutant', activeKeys, oracle),
    (error) => error.assertionId === 'unpivot.preview.oracle' && /valueQuantity\.unit cell "ZZZ" != oracle cm/u.test(error.message),
    'the real preview contract must catch a wrong source value with its stable assertion ID',
  );
});

test('requires each named check to have passed before and after aggregate evidence recording', () => {
  const report = validReport();
  assert.doesNotThrow(() => assertRequiredChecks(report.checks));
  report.checks.find((entry) => entry.name === 'negative-C2-forged').status = undefined;
  assertContractFailure(() => assertRequiredChecks(report.checks), 'unpivot.report.required-checks');

  const incomplete = validReport();
  incomplete.checks = incomplete.checks.filter((entry) => entry.name !== 'negative-C15-signed-date-melt-refused');
  assertContractFailure(() => assertRequiredChecks(incomplete.checks, true), 'unpivot.report.required-checks');
});

test('closes only a complete passed report with exact metadata and the screenshot floor', () => {
  const report = validReport();
  assert.doesNotThrow(() => assertClosedReport(report));

  const missingStatus = validReport();
  delete missingStatus.status;
  assertContractFailure(() => assertClosedReport(missingStatus), 'unpivot.report.closed');

  const wrongMetadata = validReport();
  wrongMetadata.mode = 'raw-cdp-unpivot-row-source';
  assertContractFailure(() => assertClosedReport(wrongMetadata), 'unpivot.report.closed');
  const wrongSchemaVersion = validReport();
  wrongSchemaVersion.schemaVersion = 2;
  assertContractFailure(() => assertClosedReport(wrongSchemaVersion), 'unpivot.report.closed');

  const short = validReport();
  short.screenshots.pop();
  assertContractFailure(() => assertClosedReport(short), 'unpivot.report.closed');
  const duplicate = validReport();
  duplicate.screenshots[12] = duplicate.screenshots[0];
  assertContractFailure(() => assertClosedReport(duplicate), 'unpivot.report.closed');
});

test('requires non-vacuous melt evidence and no uncaught page exceptions', () => {
  assert.doesNotThrow(() => assertMeltEvidence({ blankMeltCells: 1, exactMeltCells: 1, idMeltCells: 1 }));
  assertContractFailure(() => assertMeltEvidence({ blankMeltCells: 1, exactMeltCells: 0, idMeltCells: 1 }),
    'unpivot.report.melt-evidence');
  assert.doesNotThrow(() => assertNoUncaughtPageExceptions([]));
  assertContractFailure(() => assertNoUncaughtPageExceptions(['TypeError: bad page state']),
    'unpivot.report.page-exceptions');
});
