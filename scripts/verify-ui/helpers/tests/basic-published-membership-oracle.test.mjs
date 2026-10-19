import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import test from 'node:test';
import { chromium } from '@playwright/test';
import { isPublishedMembershipIDColumn, rootedEmptyTargetAppliedExpression } from '../builder-combine-helpers.mjs';
import { loadBasicPublishedMembershipOracle } from '../basic-published-membership-oracle.mjs';
import {
  publishedMembershipReadinessDiagnosticInPage,
  publishedMembershipRowsReady,
  publishedMembershipSelectedOutputReady,
} from '../../workflows/builder-combine-draft.mjs';

const fixtureDir = resolve('testdata/verify-combine');

test('Basic published Membership derives exact INCLUDE and EXCLUDE rows from independent NDJSON IDs', () => {
  const oracle = loadBasicPublishedMembershipOracle(fixtureDir);
  assert.equal(oracle.ok, true);
  assert.equal(oracle.observations.sha256, '748ad78ede41544be13892ffc7e937a7317f96d13a93ecd982a71646b226b4d0');
  assert.equal(oracle.reports.sha256, 'd143cc20a8623a72c165c9c946632f340b297c5b6e43a7fb29ad72898fce5b2f');
  assert.deepEqual(oracle.observations.ids, [
    'combine-observation-final-1',
    'combine-observation-final-2',
    'combine-observation-preliminary',
    'combine-observation-unmatched',
  ]);
  assert.deepEqual(oracle.reports.ids, [
    'combine-observation-final-1',
    'combine-observation-final-2',
    'combine-observation-preliminary',
  ]);
  assert.deepEqual(oracle.includeRows, [
    ['combine-observation-final-1'],
    ['combine-observation-final-2'],
    ['combine-observation-preliminary'],
  ]);
  assert.deepEqual(oracle.excludeRows, [['combine-observation-unmatched']]);
});

test('published Membership accepts consistent nullable scalar String ID columns and rejects malformed shapes', () => {
  assert.equal(isPublishedMembershipIDColumn({
    name: 'col_37b02f934b57a1c9b0e96c6e', label: 'Observation ID', semanticPath: 'id',
    type: 'string', clickhouseType: 'String', nullable: false, repeated: false,
  }), true);
  assert.equal(isPublishedMembershipIDColumn({
    id: 'group-key_6ecd7402-e79f-4524-aa69-edee29c82742',
    name: 'col_37b02f934b57a1c9b0e96c6e', label: 'Observation ID', semanticPath: 'id',
    type: 'string', clickhouseType: 'Nullable(String)', nullable: true, repeated: false,
  }), true);
  assert.equal(isPublishedMembershipIDColumn({
    semanticPath: 'id',
    type: 'string', clickhouseType: 'Nullable(String)', nullable: false, repeated: false,
  }), false);
  assert.equal(isPublishedMembershipIDColumn({
    semanticPath: 'id',
    type: 'string', clickhouseType: 'String', nullable: true, repeated: false,
  }), false);
  assert.equal(isPublishedMembershipIDColumn({
    semanticPath: 'id',
    type: 'integer', clickhouseType: 'Nullable(Int64)', nullable: true, repeated: false,
  }), false);
  assert.equal(isPublishedMembershipIDColumn({
    semanticPath: 'id',
    type: 'string', clickhouseType: 'Nullable(String)', nullable: true, repeated: true,
  }), false);
  assert.equal(isPublishedMembershipIDColumn({
    type: 'string', clickhouseType: 'Nullable(String)', nullable: true, repeated: false,
  }), false);
  assert.equal(isPublishedMembershipIDColumn({
    semanticPath: 'gender',
    type: 'string', clickhouseType: 'Nullable(String)', nullable: true, repeated: false,
  }), false);
});

test('published Membership timeout breakdown matches proposal DOM and isolates the active-output gate', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    const outputId = 'out_membership_target';
    const rows = [
      ['combine-observation-final-1'],
      ['combine-observation-final-2'],
      ['combine-observation-preliminary'],
    ];
    await page.setContent(`<!doctype html><html><body>
      <section data-testid="construction-workspace">
        <section aria-label="Combine editor" data-testid="construction-operation-editor" data-output-id="${outputId}">
          <div data-testid="construction-operation-editor">Nested generic editor</div>
          <section aria-label="Proposed change" data-testid="construction-proposal-panel" data-proposal-status="ready" data-proposal-id="receipt-membership-1"></section>
        </section>
      </section>
      <section aria-label="Table result" data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="${outputId}" data-preview-receipt-id="receipt-membership-1">
        <div data-testid="construction-proposal-preview" data-preview-status="ready" data-preview-receipt-id="receipt-membership-1" data-preview-output-id="${outputId}">
          <table><thead><tr><th scope="col"><span>Observation ID</span><span>string</span></th></tr></thead><tbody>
            ${rows.map(([id]) => `<tr data-testid="construction-proposal-preview-row"><td>${id}</td></tr>`).join('')}
          </tbody></table>
        </div>
      </section>
    </body></html>`);

    const readinessExpression = publishedMembershipRowsReady('proposal', outputId, rows);
    const evaluateReadiness = () => page.evaluate((source) => Function('return ' + source)(), readinessExpression);
    const evaluateSelection = (kind) => page.evaluate(
      (source) => Function('return ' + source)(),
      publishedMembershipSelectedOutputReady(kind, outputId),
    );
    assert.equal(await page.locator(`[data-testid="construction-table-${outputId}"]`).count(), 0);
    let diagnostic = await page.evaluate(publishedMembershipReadinessDiagnosticInPage, { outputId, rows });
    assert.equal(await evaluateReadiness(), true);
    assert.equal(await evaluateSelection('proposal'), true);
    assert.equal(await evaluateSelection('saved'), true);
    assert.equal(diagnostic.predicate, true);
    assert.deepEqual(diagnostic.checks, {
      oneReadyProposalPreview: true,
      exactPreviewOutput: true,
      proposalReceiptPresent: true,
      proposalIdentityPresent: true,
      targetOutputSelected: true,
      exactHeaders: true,
      exactRows: true,
      bodyHasNoLoadingOrPreviewFailure: true,
    });
    assert.deepEqual(diagnostic.observed.actualRows, rows);
    assert.equal(diagnostic.observed.operationEditorCount, 1);

    await page.locator('[data-testid="construction-operation-editor"][data-output-id]').evaluate((node) => node.setAttribute('data-output-id', 'out_wrong_target'));
    diagnostic = await page.evaluate(publishedMembershipReadinessDiagnosticInPage, { outputId, rows });
    assert.equal(await evaluateReadiness(), false);
    assert.equal(await evaluateSelection('proposal'), false);
    assert.equal(await evaluateSelection('saved'), false);
    assert.equal(diagnostic.predicate, false);
    assert.equal(diagnostic.checks.targetOutputSelected, false);
    assert.equal(diagnostic.observed.operationEditorOutputId, 'out_wrong_target');
    assert.deepEqual(Object.entries(diagnostic.checks).filter(([, passed]) => !passed).map(([name]) => name), ['targetOutputSelected']);

    await page.locator('[data-testid="construction-operation-editor"][data-output-id]').evaluate((node, id) => node.setAttribute('data-output-id', id), outputId);
    await page.locator('[data-testid="construction-workspace"]').evaluate((node) => node.append(
      node.querySelector('[data-testid="construction-operation-editor"][data-output-id]').cloneNode(true),
    ));
    diagnostic = await page.evaluate(publishedMembershipReadinessDiagnosticInPage, { outputId, rows });
    assert.equal(await evaluateReadiness(), false);
    assert.equal(await evaluateSelection('proposal'), false);
    assert.equal(await evaluateSelection('saved'), false);
    assert.equal(diagnostic.checks.targetOutputSelected, false);
    assert.equal(diagnostic.observed.operationEditorCount, 2);

    const proposalRowsMarkup = rows.map(([id]) => '<tr data-testid="construction-proposal-preview-row"><td>' + id + '</td></tr>').join('');
    await page.setContent(
      '<!doctype html><html><body><nav aria-label="Tables"><button type="button" aria-current="page" data-testid="construction-table-' + outputId + '">Published Observation ID source combined</button></nav>' +
      '<section data-testid="construction-proposal-panel" data-proposal-id="receipt-membership-1"></section>' +
      '<div data-testid="construction-proposal-preview" data-preview-status="ready" data-preview-output-id="' + outputId + '" data-preview-receipt-id="receipt-membership-1">' +
      '<table><thead><tr><th><span>Observation ID</span></th></tr></thead><tbody>' + proposalRowsMarkup + '</tbody></table></div></body></html>',
    );
    diagnostic = await page.evaluate(publishedMembershipReadinessDiagnosticInPage, { outputId, rows });
    assert.equal(await evaluateReadiness(), false);
    assert.equal(await evaluateSelection('proposal'), false);
    assert.equal(await evaluateSelection('saved'), true);
    assert.equal(diagnostic.checks.targetOutputSelected, false);
    assert.equal(diagnostic.observed.operationEditorCount, 0);

    const savedRowsMarkup = rows.map(([id]) => '<div role="row"><div role="cell">' + id + '</div></div>').join('');
    await page.setContent(
      '<!doctype html><html><body><section data-testid="construction-workspace">' +
      '<section data-testid="construction-operation-editor" data-output-id="' + outputId + '"><div data-testid="construction-operation-editor">Nested generic editor</div></section>' +
      '<section data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="' + outputId + '">' +
      '<div data-testid="preview-table-scroll"><div role="table" aria-rowcount="' + (rows.length + 1) + '">' +
      '<div role="row"><div role="columnheader">Observation ID</div></div>' + savedRowsMarkup +
      '</div></div></section></section></body></html>',
    );
    const savedWithEditorExpression = publishedMembershipRowsReady('saved', outputId, rows);
    const evaluateSavedWithEditor = () => page.evaluate((source) => Function('return ' + source)(), savedWithEditorExpression);
    assert.equal(await evaluateSavedWithEditor(), true);
    assert.equal(await evaluateSelection('saved'), true);
    await page.locator('[data-testid="construction-operation-editor"][data-output-id]').evaluate(
      (node) => node.setAttribute('data-output-id', 'out_wrong_target'),
    );
    assert.equal(await evaluateSavedWithEditor(), false);
    assert.equal(await evaluateSelection('saved'), false);

    await page.setContent(
      '<!doctype html><html><body><section data-testid="construction-workspace">' +
      '<nav aria-label="Tables"><button type="button" aria-current="page" data-testid="construction-table-' + outputId + '">Published Observation ID source combined</button></nav>' +
      '<section data-testid="construction-history"></section>' +
      '<section data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="' + outputId + '">' +
      '<div data-testid="preview-table-scroll"><div role="table" aria-rowcount="' + (rows.length + 1) + '">' +
      '<div role="row"><div role="columnheader">Observation ID</div></div>' + savedRowsMarkup +
      '</div></div></section></section></body></html>',
    );
    const savedExpression = publishedMembershipRowsReady('saved', outputId, rows);
    const evaluateSaved = () => page.evaluate((source) => Function('return ' + source)(), savedExpression);
    assert.equal(await evaluateSaved(), true);
    assert.equal(await evaluateSelection('saved'), true);
    assert.equal(await evaluateSelection('proposal'), false);
    await page.locator('nav[aria-label="Tables"]').evaluate((nav, id) => nav.insertAdjacentHTML(
      'beforeend',
      '<button type="button" aria-current="page" data-testid="construction-table-' + id + '">Duplicate target</button>',
    ), outputId);
    assert.equal(await evaluateSaved(), false);
    assert.equal(await evaluateSelection('saved'), false);
    await page.locator('[data-testid="construction-table-' + outputId + '"]').nth(1).evaluate((node) => node.remove());
    await page.locator('nav[aria-label="Tables"]').evaluate((nav) => nav.insertAdjacentHTML(
      'beforeend',
      '<button type="button" aria-current="page" data-testid="construction-table-other">Other selected table</button>',
    ));
    assert.equal(await evaluateSaved(), false);
    assert.equal(await evaluateSelection('saved'), false);

    await page.setContent(
      '<!doctype html><html><body><nav aria-label="Tables"><button type="button" aria-current="page" data-testid="construction-table-' + outputId + '">Published Observation ID source combined</button></nav>' +
      '<section data-testid="construction-preview" data-preview-status="empty" data-preview-output-id="' + outputId + '"><div data-testid="preview-table-scroll">Add a column to see your table.</div></section></body></html>',
    );
    const emptyTargetExpression = rootedEmptyTargetAppliedExpression(outputId) + '&&' +
      publishedMembershipSelectedOutputReady('saved', outputId);
    const evaluateEmptyTarget = () => page.evaluate((source) => Function('return ' + source)(), emptyTargetExpression);
    assert.equal(await evaluateEmptyTarget(), true);
    await page.locator('nav[aria-label="Tables"]').evaluate((nav, id) => nav.insertAdjacentHTML(
      'beforeend',
      '<button type="button" aria-current="page" data-testid="construction-table-' + id + '">Duplicate target</button>',
    ), outputId);
    assert.equal(await evaluateEmptyTarget(), false);
  } finally {
    await browser.close();
  }
});
