import test from 'node:test';
import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { chromium } from '@playwright/test';
import {
  BASIC_CODED_PIVOT_EXPECTED_IDS,
  BASIC_CODED_PIVOT_EXPECTED_PATIENT_ID,
  BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS,
  assertBasicCodedPivotRendered,
  assertBasicCodedPivotPreviewRendered,
  readBasicCodedPivotFixture,
  readBasicCodedPivotPatientFixture,
} from '../basic-coded-pivot-fixture.mjs';
import {
  normalizeRenderedHeader,
  outputIdFromInitialTableCaptures,
  openCodedPivotAdvancedValueHandling,
  proposalReady,
} from '../../workflows/basic-coded-pivot.mjs';

const fixturePath = resolve('testdata/basic-coded-pivot/Observation.ndjson');
const patientFixturePath = resolve('testdata/basic-coded-pivot/Patient.ndjson');

test('Basic coded Pivot fixture satisfies the ordinary dev loader Patient requirement', () => {
  const patient = readBasicCodedPivotPatientFixture(patientFixturePath);
  assert.equal(patient.id, BASIC_CODED_PIVOT_EXPECTED_PATIENT_ID);
  assert.equal(patient.sha256, '480a1925b4319f97b8def5b49aa4ddd962c5e6958f722f6855faba74691193a7');
});

test('Basic coded Pivot fixture has four literal source identities and exact paired values', () => {
  const fixture = readBasicCodedPivotFixture(fixturePath);
  assert.deepEqual(fixture.ids, ['basic-pivot-001', 'basic-pivot-002', 'basic-pivot-003', 'basic-pivot-004']);
  assert.deepEqual(fixture.rows, [
    ['basic-pivot-001', '2', '10'],
    ['basic-pivot-002', '3', '—'],
    ['basic-pivot-003', '—', '12'],
    ['basic-pivot-004', '2', '10'],
  ]);
  assert.equal(fixture.sha256, 'bd7c2591546b90c27e4443d3263a3bd78e7d07507b7dd362dcfe177c101eeb1a');
});

test('initial table output identity follows the captured command result into ADD_COLUMN', () => {
  const tableCapture = {
    request: {
      commandId: 'cmd-create-table-001',
      semanticsVersion: 10,
      snapshotToken: 'snapshot-001',
      expectedDraftVersion: 0,
      commands: [{ type: 'CREATE_TABLE', title: 'Observations', rootNodeId: 'Observation' }],
    },
    body: {
      commandId: 'cmd-create-table-001',
      results: [{ type: 'TABLE_CREATED', outputId: 'out_observations_001', occurrenceId: 'base', tabId: 'tab_observations_001' }],
    },
  };
  const idColumnCapture = {
    request: {
      commandId: 'cmd-add-observation-id-001',
      semanticsVersion: 10,
      snapshotToken: 'snapshot-001',
      expectedDraftVersion: 1,
      commands: [{ type: 'ADD_COLUMN', outputId: 'out_observations_001', occurrenceId: 'base', candidateId: 'observation-id' }],
    },
    body: {
      commandId: 'cmd-add-observation-id-001',
      results: [{ type: 'COLUMN_ADDED', outputId: 'out_observations_001', occurrenceId: 'base', column: 'id' }],
    },
  };

  assert.equal(Object.hasOwn(tableCapture.request, 'outputId'), false);
  assert.equal(outputIdFromInitialTableCaptures(tableCapture, idColumnCapture), 'out_observations_001');
  assert.throws(() => outputIdFromInitialTableCaptures(tableCapture, {
    ...idColumnCapture,
    request: { ...idColumnCapture.request, commands: [{ ...idColumnCapture.request.commands[0], outputId: 'wrong-output' }] },
  }), /Expected values to be strictly equal/);
});

test('native table header normalization accepts the rendered uppercase header and strips its type line', () => {
  assert.equal(normalizeRenderedHeader('OBSERVATION ID'), 'observation id');
  assert.equal(normalizeRenderedHeader('ALPHA\nINTEGER'), 'alpha');
});

test('Basic coded Pivot output preserves exact projected pairs and duplicate-row multiplicity', () => {
  const rendered = {
    headers: ['ALPHA\nINTEGER', 'BETA\nINTEGER'],
    rows: [
      ['—', '12'],
      ['2', '10'],
      ['3', '—'],
      ['2', '10'],
    ],
  };
  assert.deepEqual(assertBasicCodedPivotRendered(rendered), BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS);
  assert.deepEqual(BASIC_CODED_PIVOT_EXPECTED_IDS, ['basic-pivot-001', 'basic-pivot-002', 'basic-pivot-003', 'basic-pivot-004']);
  assert.equal(BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS.filter(row => JSON.stringify(row) === JSON.stringify(['2', '10'])).length, 2);
  assert.throws(() => assertBasicCodedPivotRendered({ ...rendered, rows: rendered.rows.slice(1) }), /exact generated value pairs and multiplicity/);
  assert.throws(() => assertBasicCodedPivotRendered({ ...rendered, rows: [['—', '12'], ['2', '10'], ['3', '—'], ['3', '—']] }), /exact generated value pairs and multiplicity/);
  assert.throws(() => assertBasicCodedPivotRendered({ ...rendered, headers: ['Observation ID', 'Alpha', 'Beta'] }), /exactly its two generated category columns/);
});

test('Basic coded Pivot proposal preview binds its exact generated columns and row multiset', () => {
  const rendered = {
    headers: ['ALPHA\nINTEGER', 'BETA\nINTEGER'],
    rows: [
      ['—', '12'],
      ['3', '—'],
      ['2', '10'],
      ['2', '10'],
    ],
  };
  assert.deepEqual(assertBasicCodedPivotPreviewRendered(rendered), [...BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS]
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right))));
  assert.throws(() => assertBasicCodedPivotPreviewRendered({
    ...rendered,
    rows: [['—', '12'], ['3', '—'], ['3', '—']],
  }), /exact generated value pairs and multiplicity/);
  assert.throws(() => assertBasicCodedPivotPreviewRendered({
    ...rendered,
    rows: [['—', '12'], ['3', '—'], ['2', '—']],
  }), /exact generated value pairs and multiplicity/);
  assert.throws(() => assertBasicCodedPivotPreviewRendered({
    ...rendered,
    headers: ['Observation ID', 'Alpha', 'Beta'],
  }), /exactly its two generated category columns/);
});

test('native Coded Pivot proposal and saved settings preserve projected pairs and duplicate multiplicity', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`
      <section data-testid="construction-proposal-panel" data-proposal-status="ready">
        <table data-testid="construction-proposal-preview">
          <thead><tr>
            <th><span style="display:block">Alpha</span><span style="display:block">INTEGER</span></th>
            <th><span style="display:block">Beta</span><span style="display:block">INTEGER</span></th>
          </tr></thead>
          <tbody>
            <tr data-testid="construction-proposal-preview-row"><td>2</td><td>10</td></tr>
            <tr data-testid="construction-proposal-preview-row"><td>3</td><td>—</td></tr>
            <tr data-testid="construction-proposal-preview-row"><td>—</td><td>12</td></tr>
            <tr data-testid="construction-proposal-preview-row"><td>2</td><td>10</td></tr>
          </tbody>
        </table>
      </section>
      <button data-testid="construction-apply-proposal">Apply</button>
    `);

    const snapshot = await proposalReady(page, BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS, () => 2_000);
    assert.deepEqual(snapshot.rows, [
      ['2', '10'], ['3', '—'], ['—', '12'], ['2', '10'],
    ]);
    await assert.rejects(
      proposalReady(page, BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS.slice(1), () => 2_000),
      /exact generated value pairs and multiplicity/,
    );

    await page.setContent(`
      <section aria-label="Coded values as columns">
        <details><summary>Advanced value handling</summary>
          <label>When several values match
            <select><option value="ERROR" selected>Stop if several values match</option><option value="SUM">Add values</option></select>
          </label>
        </details>
      </section>
    `);
    const editor = page.getByRole('region', { name: 'Coded values as columns' });
    const policy = await openCodedPivotAdvancedValueHandling(editor, () => 2_000);
    assert.equal(await editor.locator('details').getAttribute('open'), '');
    assert.equal(await policy.isVisible(), true);
    assert.equal(await policy.inputValue(), 'ERROR');
    await page.close();
  } finally {
    await browser.close();
  }
});
