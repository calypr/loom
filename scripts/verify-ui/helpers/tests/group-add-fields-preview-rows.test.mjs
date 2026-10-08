import assert from 'node:assert/strict';
import test from 'node:test';
import { chromium } from 'playwright';
import {
  expectedGroupAddFieldsRows,
  groupAddFieldsPreviewHeaders,
  groupAddFieldsPreviewWaitState,
} from '../../workflows/verify-cda-group-add-fields-browser.mjs';

test('Group Add Columns expectations follow the applied presentation order and retain raw counts', () => {
  const rawGroupedRows = [
    ['raw-specimen-1', '12'],
    ['raw-specimen-2', '3'],
  ];

  const expected = expectedGroupAddFieldsRows(rawGroupedRows, 'Specimen');

  assert.deepEqual(groupAddFieldsPreviewHeaders, ['Specimen ID', 'Row count', 'Resource Type']);
  assert.deepEqual(expected, [
    ['raw-specimen-1', '12', 'Specimen'],
    ['raw-specimen-2', '3', 'Specimen'],
  ]);
  assert(expected.every(row => row.length === groupAddFieldsPreviewHeaders.length), 'Expected rows must match all three displayed columns');
  assert.notDeepEqual(expected, [
    ['raw-specimen-1', 'Specimen', '12'],
    ['raw-specimen-2', 'Specimen', '3'],
  ], 'The applied Group aggregate precedes the row-value field in output order');
});

test('Group Add Fields reload wait matches semantic headers and rejects stale or incomplete previews', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`
      <div data-testid="preview-table-scroll" style="width: 360px; height: 120px; overflow: auto">
        <div role="table" aria-rowcount="2" aria-colcount="3">
          <div role="row">
            <div role="columnheader" style="text-transform: uppercase">Specimen ID</div>
            <div role="columnheader" style="text-transform: uppercase">Row count</div>
            <div role="columnheader" style="text-transform: uppercase">Resource Type</div>
          </div>
          <div role="row">
            <div role="cell">00001c68-2c20-5003-a144-b2442469d8de</div>
            <div role="cell">12</div>
            <div role="cell">Specimen</div>
          </div>
        </div>
      </div>
    `);

    const expected = { dataRowCount: 1, columnCount: 3, header: 'Resource Type' };
    await page.waitForFunction(groupAddFieldsPreviewWaitState, expected, { timeout: 1000 });
    const ready = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(ready.ready, true);
    assert.deepEqual(ready.headers, groupAddFieldsPreviewHeaders);
    assert.deepEqual(ready.visibleHeaders, ['SPECIMEN ID', 'ROW COUNT', 'RESOURCE TYPE']);

    const wrongHeader = await page.evaluate(groupAddFieldsPreviewWaitState, {
      ...expected, header: 'Wrong label', diagnostic: true,
    });
    assert.equal(wrongHeader.headerMatches, false);
    assert.equal(wrongHeader.ready, false);

    await page.locator('[role="table"]').evaluate(table => table.setAttribute('aria-rowcount', '3'));
    const wrongRowCount = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(wrongRowCount.rowCountMatches, false);
    assert.equal(wrongRowCount.ready, false);

    await page.locator('[role="table"]').evaluate(table => {
      table.setAttribute('aria-rowcount', '2');
      table.setAttribute('aria-colcount', '4');
    });
    const wrongColumnCount = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(wrongColumnCount.columnCountMatches, false);
    assert.equal(wrongColumnCount.ready, false);

    await page.locator('[role="table"]').evaluate(table => table.setAttribute('aria-colcount', '3'));
    await page.locator('body').evaluate(body => {
      const loading = document.createElement('p');
      loading.textContent = 'Loading your table…';
      loading.id = 'preview-loading';
      body.append(loading);
    });
    const loading = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(loading.globalLoadingSentinel, true);
    assert.equal(loading.ready, false);

    await page.locator('#preview-loading').evaluate(node => node.remove());
    await page.locator('body').evaluate(body => {
      const error = document.createElement('p');
      error.textContent = 'Preview failed: fixture failure';
      error.id = 'preview-error';
      body.append(error);
    });
    const previewError = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(previewError.globalPreviewErrorSentinel, true);
    assert.equal(previewError.ready, false);
  } finally {
    await browser.close();
  }
});
