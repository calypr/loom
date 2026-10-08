import assert from 'node:assert/strict';
import test from 'node:test';
import { chromium } from 'playwright';
import {
  expectedGroupAddFieldsRows,
  groupAddFieldsPreviewHeaders,
  groupAddFieldsPreviewWaitState,
  matchesGroupAddFieldsRemovalRequest,
  matchesGroupAddFieldsRenameRequest,
} from '../../workflows/verify-cda-group-add-fields-browser.mjs';

const commandPath = '/api/v1/projects/loom_dev_cda_fhir/explorers/qa-reshape-group-add-fields-f3a413a5-cd66-4085-b83e-878076cc1238/authoring/v2/commands';
const renameExpected = {
  commandPath,
  draftVersion: 7,
  draftDigest: 'sha256:9bf00f78ea3660f3277e38161a5ba0734226bcb9e72b47809cdd2583953f1c46',
  outputId: 'out_0ce2ec4739afb2d74801b704',
  stepId: 'group_6ef5835a-5b06-4b98-89f8-5343fdcb850d',
  sourceColumnId: 'source_b40816f95982f462646f7c04',
  outputColumnId: 'row_value_e885c152162968a4b1a6fb70',
  label: 'Specimen Resource Type',
};
const renameRequest = {
  method: 'POST',
  path: commandPath,
  body: {
    expectedDraftVersion: renameExpected.draftVersion,
    expectedDraftDigest: renameExpected.draftDigest,
    commands: [{
      type: 'UPDATE_CONSTRUCTION_OUTPUT',
      outputId: renameExpected.outputId,
      constructionOutput: {
        stepId: renameExpected.stepId,
        columnId: renameExpected.outputColumnId,
        label: renameExpected.label,
      },
    }],
  },
};

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

test('Group Add Fields correlates native rename and removal with distinct source and Group output identities', () => {
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, renameExpected), true,
    'The retained successful UPDATE_CONSTRUCTION_OUTPUT command must match its derived row-value output');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, outputColumnId: renameExpected.sourceColumnId,
  }), false, 'The authored source field identity must not be mistaken for the derived Group output identity');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, outputId: 'out-other',
  }), false, 'A command for another output must not satisfy the rename wait');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, stepId: 'group-other',
  }), false, 'A command for another Group step must not satisfy the rename wait');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, draftVersion: 6,
  }), false, 'A stale draft version must not satisfy the rename wait');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, draftDigest: 'sha256:stale',
  }), false, 'A stale draft digest must not satisfy the rename wait');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, commandPath: '/api/v1/projects/other/explorers/qa/authoring/v2/commands',
  }), false, 'A command from another Explorer must not satisfy the rename wait');

  const removalExpected = {
    commandPath,
    draftVersion: 8,
    draftDigest: 'sha256:32f286b1a37f44962c611d9694522e42c91d4d1074848a6c6bac680f8f99dc5a',
    outputId: renameExpected.outputId,
    sourceColumn: 'col_cc83aceeefd5acaaad33fc87',
    sourceColumnId: renameExpected.sourceColumnId,
    outputColumnId: renameExpected.outputColumnId,
  };
  const removalRequest = {
    method: 'POST',
    path: commandPath,
    body: {
      expectedDraftVersion: removalExpected.draftVersion,
      expectedDraftDigest: removalExpected.draftDigest,
      commands: [{
        type: 'REMOVE_COLUMN',
        outputId: removalExpected.outputId,
        column: removalExpected.sourceColumn,
      }],
    },
  };
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, removalExpected), true,
    'Removing the Group row-value must target its exact authored source column');
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, {
    ...removalExpected, sourceColumn: removalExpected.outputColumnId,
  }), false, 'The remove adapter must use the authored physical column name, not the derived output ID');
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, {
    ...removalExpected, outputId: 'out-other',
  }), false, 'A removal for another output must not satisfy the removal wait');
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, {
    ...removalExpected, draftVersion: 7,
  }), false, 'A stale draft version must not satisfy the removal wait');
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, {
    ...removalExpected, draftDigest: 'sha256:stale',
  }), false, 'A stale draft digest must not satisfy the removal wait');
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
