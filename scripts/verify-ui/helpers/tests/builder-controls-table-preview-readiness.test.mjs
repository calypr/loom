import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { chromium } from '@playwright/test';
import {
  currentReadyPreviewOutputId,
  currentPreviewReady,
  patientOracle,
  waitForCurrentPreviewRows,
} from '../../workflows/builder-controls.mjs';

const rowMarkup = cells => `<div role="row">${cells.map(cell =>
  `<div role="cell" style="display: block">${cell}</div>`,
).join('')}</div>`;

test('reads the current Preview output while the Add columns editor has no table tab', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    const outputId = 'out_patient_gender';
    await page.setContent(`
      <main data-testid="construction-workspace" data-draft-version="12" data-draft-digest="sha256:draft-current">
        <section aria-label="Add columns editor"></section>
        <section data-testid="construction-preview" data-preview-status="ready"
          data-preview-receipt-id="receipt-current" data-preview-output-id="${outputId}"
          data-current-draft-version="12" data-current-draft-digest="sha256:draft-current">
          <div data-testid="preview-table-scroll">
            <div role="table" aria-rowcount="2">${rowMarkup(['ROW', 'PATIENT ID'])}</div>
          </div>
        </section>
      </main>
    `);

    assert.equal(await page.locator('[data-testid^="construction-table-"][aria-current="page"]').count(), 0,
      'the table tabs are unmounted while the Add columns editor is open');
    assert.equal(await page.evaluate(currentReadyPreviewOutputId), outputId,
      'the ready Preview exposes the actual output binding without inventing a table tab');

    await page.locator('[data-testid="construction-preview"]').evaluate(node => {
      node.dataset.currentDraftDigest = 'sha256:stale-draft';
    });
    assert.equal(await page.evaluate(currentReadyPreviewOutputId), null,
      'a Preview bound to a stale draft cannot identify the selected output');
    await page.locator('[data-testid="construction-preview"]').evaluate(node => {
      node.dataset.currentDraftDigest = 'sha256:draft-current';
      node.dataset.previewReceiptId = '';
    });
    assert.equal(await page.evaluate(currentReadyPreviewOutputId), null,
      'a Preview without a receipt cannot identify a current output');
  } finally {
    await browser.close();
  }
});

test('waits for selected current-draft rows instead of same-count stale Patient values', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    const outputId = 'out_patient_preview_readiness';
    const staleOutputId = 'out_previous_patient_preview';
    const fixtureDir = fileURLToPath(new URL('../../../../testdata/devloop-fixture', import.meta.url));
    const oracle = patientOracle({ fixtureDir });
    const report = {
      dimensions: { correctness: { status: 'untested', evidence: [] } },
      assertions: [],
    };
    const checkCalls = [];
    const check = (...args) => checkCalls.push(args);
    const waitArgs = {
      page,
      report,
      outputId,
      expectedIDs: oracle.ids,
      expectedGenderByID: oracle.genderByID,
      check,
      timeoutMs: 5000,
    };

    await page.setContent(`
      <div data-testid="construction-workspace" data-draft-version="8" data-draft-digest="sha256:draft-current">
        <button data-testid="construction-table-${staleOutputId}" aria-current="page">Old Patients</button>
      </div>
      <div role="status">Loading the preview</div>
      <section data-testid="construction-preview" data-preview-status="ready"
        data-preview-receipt-id="receipt-old" data-preview-output-id="${staleOutputId}"
        data-current-draft-version="7" data-current-draft-digest="sha256:draft-old">
        <div data-testid="preview-table-scroll">
          <div role="table" aria-rowcount="3">
            ${rowMarkup(['ROW', 'PATIENT ID', 'GENDER'])}
            ${rowMarkup(['1', 'stale-patient-id', 'unknown'])}
            ${rowMarkup(['2', 'another-stale-patient-id', 'male'])}
          </div>
        </div>
      </section>
    `);

    assert.equal(await page.evaluate(currentPreviewReady, outputId), false,
      'a ready-looking preview for an old output and draft is not current for this selection');
    let settled = false;
    const pending = waitForCurrentPreviewRows(waitArgs).then(rows => {
      settled = true;
      return { rows };
    }, error => {
      settled = true;
      return { error };
    });
    await page.waitForTimeout(30);
    assert.equal(settled, false, 'the old selected output and draft must not settle the current preview wait');

    await page.evaluate(({ outputId, staleOutputId }) => {
      const workspace = document.querySelector('[data-testid="construction-workspace"]');
      workspace.querySelector(`[data-testid="construction-table-${staleOutputId}"]`).removeAttribute('aria-current');
      const selected = document.createElement('button');
      selected.setAttribute('data-testid', `construction-table-${outputId}`);
      selected.setAttribute('aria-current', 'page');
      selected.textContent = 'Patients';
      workspace.append(selected);
    }, { outputId, staleOutputId });
    assert.equal(await page.evaluate(currentPreviewReady, outputId), false,
      'changing selection alone must not accept the stale preview');

    await page.evaluate(() => {
      const workspace = document.querySelector('[data-testid="construction-workspace"]');
      workspace.dataset.draftVersion = '9';
      workspace.dataset.draftDigest = 'sha256:draft-current-9';
    });
    assert.equal(await page.evaluate(currentPreviewReady, outputId), false,
      'the previous preview draft must not be accepted after the workspace draft changes');
    await page.waitForTimeout(30);
    assert.equal(settled, false);

    await page.evaluate(outputId => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      preview.dataset.previewReceiptId = 'receipt-current';
      preview.dataset.previewOutputId = outputId;
      preview.dataset.currentDraftVersion = '9';
      preview.dataset.currentDraftDigest = 'sha256:draft-current-9';
    }, outputId);
    assert.equal(await page.evaluate(currentPreviewReady, outputId), false,
      'a matching binding remains pending while the preview-loading status is visible');
    await page.locator('[role="status"]').evaluate(node => node.remove());
    assert.equal(await page.evaluate(currentPreviewReady, outputId), true,
      'the readiness predicate accepts only the selected output and current draft');
    await page.waitForTimeout(30);
    assert.equal(settled, false,
      'same-count stale Patient IDs and Gender values must not settle the current preview wait');

    const expectedRows = [
      ['ROW', 'PATIENT ID', 'GENDER'],
      ...oracle.ids.map((id, index) => [String(index + 1), id, oracle.genderByID[id] ?? '—']),
    ];
    await page.evaluate(rows => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      table.replaceChildren(...rows.map(cells => {
        const row = document.createElement('div');
        row.setAttribute('role', 'row');
        for (const value of cells) {
          const cell = document.createElement('div');
          cell.setAttribute('role', 'cell');
          cell.style.display = 'block';
          cell.textContent = value;
          row.append(cell);
        }
        return row;
      }));
      table.setAttribute('aria-rowcount', String(rows.length));
    }, expectedRows);

    const result = await pending;
    if ('error' in result) throw result.error;
    assert.deepEqual(result.rows.slice(1), [
      '1\ndev-patient-001\nfemale',
      '2\ndev-patient-002\n—',
    ]);
    assert.deepEqual(checkCalls[0]?.[3].patientIDs, oracle.ids);
    assert.deepEqual(checkCalls[0]?.[3].expectedGenderByID, oracle.genderByID);
    assert.equal(report.assertions[0]?.name, 'automatic Preview is visible after authoring');
    assert.equal(report.assertions[0]?.status, 'passed');

  } finally {
    await browser.close();
  }
});
