import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { runInNewContext } from 'node:vm';
import test from 'node:test';
import { existingRootChangeOracle, normalizeRootChangePreviewCell, rootChangePreviewRowsMatch, rootOption } from '../playwright-authoring.mjs';
import { previewHeaderIndex } from '../playwright-preview-rows.mjs';

test('root-change preview matching ignores order while retaining duplicate rows and exact values', () => {
  const expected = [
    { patientId: 'dev-patient-001', gender: 'female' },
    { patientId: 'dev-patient-002', gender: null },
    { patientId: 'dev-patient-001', gender: 'female' },
    { patientId: null, gender: null },
  ];
  const rendered = [expected[2], expected[3], expected[1], expected[0]];
  assert.equal(rootChangePreviewRowsMatch(rendered, expected), true);
  assert.equal(rootChangePreviewRowsMatch(rendered.slice(1), expected), false,
    'The root preview comparison must retain duplicate source rows.');
  assert.equal(rootChangePreviewRowsMatch(rendered.map(row => ({ ...row, gender: 'unknown' })), expected), false,
    'The root preview comparison must retain exact nullable source values.');
});

test('existing-table root-change oracle covers every Observation root and nullable Patient bindings', () => {
  const fixtureDir = resolve('testdata/builder-coded-source-column');
  const oracle = existingRootChangeOracle({ fixtureDir });

  assert.deepEqual(oracle.rows, [
    { observationId: 'dev-observation-001', patientId: 'dev-patient-001', gender: 'female' },
    { observationId: 'dev-observation-002', patientId: 'dev-patient-002', gender: null },
    { observationId: 'dev-observation-003', patientId: 'dev-patient-001', gender: 'female' },
    { observationId: 'dev-pair-001', patientId: null, gender: null },
    { observationId: 'dev-pair-002', patientId: null, gender: null },
    { observationId: 'dev-pair-003', patientId: null, gender: null },
  ]);
  assert.equal(oracle.patient.path, join(fixtureDir, 'Patient.ndjson'));
  assert.equal(oracle.observation.path, join(fixtureDir, 'Observation.ndjson'));
  assert.match(oracle.patient.sha256, /^[a-f0-9]{64}$/);
  assert.match(oracle.observation.sha256, /^[a-f0-9]{64}$/);
  assert.equal(existsSync(join(fixtureDir, 'j01-concepts.fixture.json')), false,
    'the isolated root-change fixture must not append generated J01 Observation roots');
  assert.deepEqual(oracle.patientRows, [
    { patientId: 'dev-patient-001', gender: 'female' },
    { patientId: 'dev-patient-002', gender: null },
  ]);
});

test('existing-root preview resolves CSS-uppercase headers uniquely', () => {
  const headers = ['ROW', 'PATIENT ID', 'GENDER'];
  assert.equal(previewHeaderIndex(headers, 'Patient ID'), 1);
  assert.equal(previewHeaderIndex(headers, 'Gender'), 2);
  assert.throws(() => previewHeaderIndex(['Patient ID', 'PATIENT ID'], 'Patient ID'), /exactly once/);
});

test('existing-root preview normalizes missing Patient ID and Gender display cells to null', () => {
  assert.deepEqual([
    normalizeRootChangePreviewCell('—'), normalizeRootChangePreviewCell('—'),
    normalizeRootChangePreviewCell('dev-patient-001'), normalizeRootChangePreviewCell('female'),
  ], [null, null, 'dev-patient-001', 'female']);
});

test('existing-root option matching keeps resource type outside the serialized DOM callback', async () => {
  const optionNodes = [
    { value: 'current', textContent: 'Patient (current)', disabled: true },
    { value: 'node:observation-node', textContent: 'Observation via Patient → Subject → Observation', disabled: false },
    { value: 'node:encounter-node', textContent: 'Encounter via Patient → Encounter → Service → Encounter', disabled: false },
  ];
  const selector = {
    locator: name => {
      assert.equal(name, 'option');
      return {
        evaluateAll: callback => JSON.parse(JSON.stringify(
          runInNewContext(`(${callback.toString()})(options)`, { options: optionNodes }),
        )),
      };
    },
  };

  assert.deepEqual(await rootOption(selector, 'Observation'), {
    value: 'node:observation-node', label: 'Observation via Patient → Subject → Observation', disabled: false,
  });
});

test('existing-root preservation selects once and opens the receipt-bound preview without a Review action', async () => {
  const workflowSource = readFileSync(new URL('../../helpers/playwright-authoring.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource,
    /const proposeRoot = async \(\{ selector, choice, rootResourceType, expectedRows, selectionLabel, previewCheck \}\) =>/);
  assert.match(workflowSource,
    /await act\(selectionLabel, selector, \(\) => selector\.selectOption\(choice\.value\), \{[\s\S]*?await expect\(panel\)\.toBeVisible/);
  assert.match(workflowSource,
    /toHaveAttribute\('data-preview-status', 'ready',[\s\S]*?getByRole\('dialog', \{ name: 'Row definition settings' \}\)\)\.toHaveCount\(0/);
  assert.match(workflowSource,
    /getByRole\('button', \{ name: 'Review changes', exact: true \}\)\.count\(\), 0/);
  assert.doesNotMatch(workflowSource, /review\.click\(\)|review: applyReview|review: reviewRootChange/);
});
