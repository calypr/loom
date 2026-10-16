import assert from 'node:assert/strict';
import { chromium } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  BASIC_ADD_COLUMN_COVERAGE_FIXTURE_DIR,
  readBasicAddColumnCoverageOracle,
} from '../basic-add-column-coverage-oracle.mjs';
import { builderDocument, requireUniqueVisibleTarget } from '../../workflows/builder-add-column-coverage.mjs';

test('raw Add Columns oracle retains three source identities and UI-populated coverage', () => {
  const oracle = readBasicAddColumnCoverageOracle({
    fixtureDir: BASIC_ADD_COLUMN_COVERAGE_FIXTURE_DIR,
  });
  const patientRecords = readFileSync(
    join(BASIC_ADD_COLUMN_COVERAGE_FIXTURE_DIR, 'Patient.ndjson'),
    'utf8',
  ).split(/\r?\n/).filter(line => line.trim()).map(line => JSON.parse(line));

  assert.deepEqual(patientRecords, [{
    resourceType: 'Patient',
    id: 'basic-add-column-coverage-inert-patient',
  }], 'The required Patient support record must remain inert and unlinked');

  assert.deepEqual(oracle, {
    resourceType: 'Observation',
    rows: [
      {
        id: 'basic-add-column-coverage-present',
        state: 'present',
        value: 'd',
      },
      {
        id: 'basic-add-column-coverage-null',
        state: 'null',
        value: null,
      },
      {
        id: 'basic-add-column-coverage-missing',
        state: 'missing',
      },
    ],
    populatedCount: 1,
    displayedCount: 3,
    populatedExamples: ['d'],
  });

  const [present, explicitNull, missing] = oracle.rows;
  assert.equal(Object.hasOwn(explicitNull, 'value'), true,
    'Explicit JSON null must remain an owned value');
  assert.equal(Object.hasOwn(missing, 'value'), false,
    'An omitted JSON member must remain absent in the raw oracle');
  assert.deepEqual([present.id, explicitNull.id, missing.id], [
    'basic-add-column-coverage-present',
    'basic-add-column-coverage-null',
    'basic-add-column-coverage-missing',
  ]);
});


test('Builder output lookup binds the documented nested output ID uniquely', () => {
  const document = {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'out_observation_coverage', title: 'Observation coverage' },
    rootResourceType: 'Observation',
    columns: [{ columnId: 'source-id', column: 'col-id', label: 'Observation ID' }],
  };
  const builder = { workspace: { documents: [document] } };

  assert.equal(builderDocument(builder, 'out_observation_coverage'), document);
  assert.throws(() => builderDocument(builder, 'out_other'), /exactly one document/);
  assert.throws(() => builderDocument({ workspace: { documents: [document, document] } },
    'out_observation_coverage'), /exactly one document/);
});

test('native target uniqueness waits for a delayed Builder toolbar control', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<!doctype html><html><body>
      <main><h2>Build your first table</h2><div id="toolbar"></div></main>
    </body></html>`);
    const newExplorer = page.getByText('New explorer', { exact: true });
    assert.equal(await newExplorer.count(), 0,
      'The toolbar trigger must be absent during the initial document-ready state');
    const waitForTrigger = requireUniqueVisibleTarget(newExplorer);
    await page.evaluate(() => {
      const details = document.createElement('details');
      const summary = document.createElement('summary');
      summary.textContent = 'New explorer';
      details.append(summary);
      document.querySelector('#toolbar').append(details);
    });
    await waitForTrigger;
    assert.equal(await newExplorer.count(), 1,
      'The native wait must find exactly one trigger after the delayed Builder toolbar render');
  } finally {
    await browser.close();
  }
});
