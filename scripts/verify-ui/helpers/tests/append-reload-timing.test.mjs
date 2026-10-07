import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { lifecycleAcceptanceDrift, registry, scenarioCaseFor } from '../../registry.mjs';
import {
  combineCancellationPreservationEvidence,
  measureActionToDOMResult,
  rootedEmptyTargetAppliedExpression,
  savedAppendPreviewAppliedExpression,
} from '../builder-combine-helpers.mjs';

const require = createRequire(import.meta.url);
const { chromium } = require('playwright');

const outputId = 'out_owned_target';
const headers = ['Record ID', 'Status'];
const rows = [['observation-1', 'final'], ['patient-1', '—']];

const savedDocument = ({ labels = ['Record ID', 'Status'], operation = 'APPEND' } = {}) => ({
  output: { id: outputId, title: 'Observation' },
  rootResourceType: 'Observation',
  columns: labels.map((label, index) => ({ id: `column-${index + 1}`, name: label.toLowerCase().replaceAll(' ', '_'), label })),
  construction: {
    version: 1,
    steps: [{
      id: 'step_append',
      operation: { combine: { kind: operation, inputs: ['revision_observation', 'revision_report', 'revision_patient'] } },
      outputs: labels.map((label, index) => ({ id: `column-${index + 1}`, name: label.toLowerCase().replaceAll(' ', '_'), label })),
    }],
  },
});

const savedPreviewDocument = ({
  selected = true,
  previewStatus = 'ready',
  previewOutputId = outputId,
  actualHeaders = headers,
  actualRows = rows,
  ariaRowCount = actualRows.length + 1,
  ariaColCount = actualHeaders.length,
  proposal = false,
  editor = false,
  history = true,
  bodyText = '',
} = {}) => {
  const table = {
    getAttribute(name) {
      if (name === 'aria-rowcount') return String(ariaRowCount);
      if (name === 'aria-colcount') return String(ariaColCount);
      return null;
    },
    querySelectorAll(selector) {
      if (selector === '[role="columnheader"]') return actualHeaders.map(textContent => ({ textContent }));
      if (selector === '[role="row"]') return [
        { querySelectorAll: () => [] },
        ...actualRows.map(row => ({ querySelectorAll: () => row.map(innerText => ({ innerText })) })),
      ];
      throw new Error(`unexpected table selector ${selector}`);
    },
  };
  const scroll = { querySelectorAll: selector => selector === '[role="table"]' ? [table] : [] };
  const panel = {
    getAttribute(name) {
      if (name === 'data-preview-status') return previewStatus;
      if (name === 'data-preview-output-id') return previewOutputId;
      return null;
    },
    querySelectorAll: selector => selector === '[data-testid="preview-table-scroll"]' ? [scroll] : [],
  };
  const selectedTable = selected ? { getAttribute: name => name === 'aria-current' ? 'page' : null } : null;
  return {
    body: { innerText: bodyText },
    querySelector(selector) {
      if (selector === `[data-testid="construction-table-${outputId}"]`) return selectedTable;
      if (selector === '[data-testid="construction-proposal-panel"]') return proposal ? {} : null;
      if (selector === '[data-testid="construction-combine-editor"]') return editor ? {} : null;
      if (selector === '[data-testid="construction-history"]') return history ? {} : null;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === '[data-testid="construction-preview"]') return [panel];
      return [];
    },
  };
};

const evaluate = (expression, document) => Function('document', `return ${expression}`)(document);

test('saved APPEND readiness requires the exact selected output, headers, and row multiset', () => {
  const expression = savedAppendPreviewAppliedExpression(outputId, headers, rows);
  assert.equal(evaluate(expression, savedPreviewDocument()), true);
  assert.equal(evaluate(expression, savedPreviewDocument({ actualRows: [rows[0], ['wrong', 'final']] })), false,
    'a different visible value must not end reload timing');
  assert.equal(evaluate(expression, savedPreviewDocument({ actualHeaders: ['Record ID', 'Wrong'] })), false,
    'a matching row count with different headers must fail');
  assert.equal(evaluate(expression, savedPreviewDocument({ previewOutputId: 'out_another_target' })), false,
    'a ready preview for another output must fail');
  assert.equal(evaluate(expression, savedPreviewDocument({ selected: false })), false,
    'the expected output must be selected');
  assert.equal(evaluate(expression, savedPreviewDocument({ previewStatus: 'stale' })), false,
    'stale preview content must not satisfy readiness');
  assert.equal(evaluate(expression, savedPreviewDocument({ history: false })), false,
    'saved preview state must have construction history');
  assert.equal(evaluate(expression, savedPreviewDocument({ proposal: true })), false,
    'a proposal preview must not be mistaken for the saved result');
  assert.equal(evaluate(expression, savedPreviewDocument({ bodyText: 'Loading your table…' })), false);
  assert.equal(evaluate(expression, savedPreviewDocument({ ariaRowCount: rows.length })), false);
});

test('saved APPEND readiness matches a local Playwright page with the PreviewTable DOM contract', async () => {
  const browserRows = [
    ['combine-observation-final-1', 'final', '—'],
    ['combine-observation-final-2', 'final', '—'],
    ['combine-observation-preliminary', 'preliminary', '—'],
    ['combine-observation-unmatched', 'unknown', '—'],
    ['combine-observation-final-1', 'final', '—'],
    ['combine-observation-final-2', 'final', '—'],
    ['combine-observation-preliminary', 'preliminary', '—'],
    ['combine-fixture-patient', '—', 'female'],
  ];
  const browserHeaders = ['Record ID', 'Status', 'Patient sex'];
  const output = 'out_append_browser_fixture';
  const escapeHTML = (value) => String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
  const previewHTML = (headers = browserHeaders, rows = browserRows) => {
    const headerCells = headers.map((value) => `<div role="columnheader">${escapeHTML(value)}</div>`).join('');
    const dataRows = rows.map((row, index) => `<div role="row"><span>${index + 1}</span>${row.map((value) =>
      `<div role="cell"><div title="${escapeHTML(value)}">${escapeHTML(value)}</div></div>`).join('')}</div>`).join('');
    return `<button type="button" data-testid="construction-table-${output}" aria-current="page">Observation combined</button>
      <div data-testid="construction-history">Saved construction</div>
      <section aria-label="Table result" data-testid="construction-preview" data-preview-status="ready" data-preview-receipt-id="receipt-test" data-preview-output-id="${output}">
        <div data-testid="preview-table-scroll">
          <div role="table" aria-rowcount="${rows.length + 1}" aria-colcount="${headers.length}">
            <div role="row"><div>Row</div>${headerCells}</div>${dataRows}
          </div>
        </div>
      </section>`;
  };
  const expression = savedAppendPreviewAppliedExpression(output, browserHeaders, browserRows);
  const launchOptions = { headless: true, channel: 'chrome' };
  if (process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE) {
    delete launchOptions.channel;
    launchOptions.executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
  }
  const browser = await chromium.launch(launchOptions);
  try {
    const page = await browser.newPage();
    await page.setContent(previewHTML());
    assert.equal(await page.evaluate(expression), true, 'the source-shaped eight-row APPEND DOM must match');

    await page.locator('[data-testid="construction-preview"]').evaluate((node) => {
      node.setAttribute('data-preview-output-id', 'out_another_target');
    });
    assert.equal(await page.evaluate(expression), false, 'the ready preview must belong to the expected output');

    await page.locator('[data-testid="construction-preview"]').evaluate((node) => {
      node.setAttribute('data-preview-output-id', 'out_append_browser_fixture');
    });
    await page.locator('[role="table"] [role="row"]').nth(8).locator('[role="cell"]').evaluateAll((cells) => {
      const firstRowCells = [...document.querySelectorAll('[role="table"] [role="row"]')[1].querySelectorAll('[role="cell"]')];
      cells.forEach((cell, index) => {
        cell.querySelector('[title]').textContent = firstRowCells[index].querySelector('[title]').textContent;
      });
    });
    assert.equal(await page.evaluate(expression), false, 'same row count with a duplicate and omitted row must fail the multiset');

    await page.setContent(previewHTML());
    await page.locator('[role="columnheader"]').nth(1).evaluate((node) => {
      node.textContent = 'Wrong status';
    });
    assert.equal(await page.evaluate(expression), false, 'row values cannot hide a mismatched public header');
  } finally {
    await browser.close();
  }
});

test('rooted-empty readiness rejects a wrong selected target, output identity, or preview state', () => {
  const expression = rootedEmptyTargetAppliedExpression(outputId);
  const documentFor = ({ selected = true, output = outputId, status = 'empty', text = 'Add a column to see your table.' } = {}) => ({
    querySelector(selector) {
      if (selector === `[data-testid="construction-table-${outputId}"]`) {
        return selected ? { getAttribute: name => name === 'aria-current' ? 'page' : null } : null;
      }
      if (selector === '[data-testid="construction-proposal-panel"]' ||
          selector === '[data-testid="construction-history"]' ||
          selector === '[data-testid="construction-combine-editor"]') return null;
      return null;
    },
    querySelectorAll(selector) {
      if (selector !== '[data-testid="construction-preview"]') return [];
      return [{
        getAttribute: name => name === 'data-preview-status' ? status : name === 'data-preview-output-id' ? output : null,
        querySelectorAll: nested => nested === '[data-testid="preview-table-scroll"]' ? [{ textContent: text }] : [],
      }];
    },
  });
  assert.equal(evaluate(expression, documentFor()), true);
  assert.equal(evaluate(expression, documentFor({ selected: false })), false);
  assert.equal(evaluate(expression, documentFor({ output: 'out_another_target' })), false);
  assert.equal(evaluate(expression, documentFor({ status: 'ready' })), false);
  assert.equal(evaluate(expression, documentFor({ text: 'Preview did not complete for this draft: stale' })), false);
});

test('action-to-DOM timing awaits the visible result and excludes later Builder reads', async () => {
  let now = 100;
  const trace = [];
  const measurement = await measureActionToDOMResult({
    now: () => now,
    action: async () => { trace.push('reload'); },
    waitForResult: async budgetMs => {
      trace.push(`wait:${budgetMs}`);
      await new Promise(resolve => setTimeout(resolve, 5));
      now += 125;
      trace.push('exact DOM result');
    },
  });
  trace.push('independent Builder read');
  assert.deepEqual(trace, ['reload', 'wait:5000', 'exact DOM result', 'independent Builder read']);
  assert.deepEqual(measurement, { elapsedMs: 125, budgetMs: 5000, withinBudget: true });
  const defaultClock = await measureActionToDOMResult({ action: async () => {}, waitForResult: async () => {} });
  assert.equal(defaultClock.withinBudget, true);
  assert.ok(Number.isFinite(defaultClock.elapsedMs) && defaultClock.elapsedMs >= 0);
});

test('action-to-DOM timing rejects timeout and reports an over-budget result', async () => {
  let now = 0;
  await assert.rejects(measureActionToDOMResult({
    now: () => now,
    action: async () => {},
    waitForResult: async () => { throw Object.assign(new Error('exact DOM result timed out'), { name: 'TimeoutError' }); },
  }), /exact DOM result timed out/);
  const late = await measureActionToDOMResult({
    now: () => now,
    action: async () => { now += 5100; },
    waitForResult: async () => {},
  });
  assert.deepEqual(late, { elapsedMs: 5100, budgetMs: 5000, withinBudget: false });
  let clockRead = 0;
  const nonMonotonic = await measureActionToDOMResult({
    now: () => clockRead++ === 0 ? 10 : 9,
    action: async () => {},
    waitForResult: async () => {},
  });
  assert.deepEqual(nonMonotonic, { elapsedMs: -1, budgetMs: 5000, withinBudget: false });
});

test('initial and removal proposal Cancel preserve exact saved document and draft state', () => {
  const before = savedDocument();
  const unchanged = combineCancellationPreservationEvidence({
    beforeDocument: before,
    afterDocument: structuredClone(before),
    beforeDraftVersion: 7,
    afterDraftVersion: 7,
    beforeDraftDigest: 'digest-a',
    afterDraftDigest: 'digest-a',
    visibleResultMatches: true,
  });
  assert.equal(unchanged.ok, true);
  const changedSchema = savedDocument({ labels: ['Record ID', 'Changed'] });
  assert.equal(combineCancellationPreservationEvidence({
    beforeDocument: before,
    afterDocument: changedSchema,
    beforeDraftVersion: 7,
    afterDraftVersion: 7,
    beforeDraftDigest: 'digest-a',
    afterDraftDigest: 'digest-a',
    visibleResultMatches: true,
  }).ok, false, 'a changed persisted output schema must fail cancellation evidence');
  assert.equal(combineCancellationPreservationEvidence({
    beforeDocument: before,
    afterDocument: structuredClone(before),
    beforeDraftVersion: 7,
    afterDraftVersion: 8,
    beforeDraftDigest: 'digest-a',
    afterDraftDigest: 'digest-b',
    visibleResultMatches: true,
  }).ok, false, 'a changed persisted draft must fail cancellation evidence');
  assert.equal(combineCancellationPreservationEvidence({
    beforeDocument: before,
    afterDocument: structuredClone(before),
    beforeDraftVersion: 7,
    afterDraftVersion: 7,
    beforeDraftDigest: 'digest-a',
    afterDraftDigest: 'digest-a',
    visibleResultMatches: false,
  }).ok, false, 'wrong visible rows or headers must fail cancellation evidence');
});

test('the published APPEND driver cancels before reconfiguration and removal Apply', () => {
  const source = readFileSync(new URL('../../workflows/builder-combine.mjs', import.meta.url), 'utf8');
  const checkpointStart = source.indexOf('const appendReloadToVisibleResultWithPlaywright = async');
  const checkpointEnd = source.indexOf('const editSavedStepWithPlaywright = async', checkpointStart);
  const checkpoint = source.slice(checkpointStart, checkpointEnd);
  assert.match(checkpoint, /measureActionToDOMResult\(/);
  assert.match(checkpoint, /waitForResult: timeoutMs => page\.waitForFunction\(expression, undefined, \{ timeout: timeoutMs \}\)/,
    'the exact saved-row or rooted-empty expression must be awaited inside the measured interval');
  assert.doesNotMatch(checkpoint, /readBuilder\(/, 'independent Builder persistence reads must happen after the DOM timing ends');
  const firstCancel = source.indexOf("Cancel the initial APPEND proposal before configuring the saved schema");
  const restartTarget = source.indexOf('const restartedTarget = await startCombineTargetWithPlaywright', firstCancel);
  const initialCancelCheck = source.indexOf('Canceling initial APPEND proposal preserves the exact fresh rooted target before creating a distinct target for reconfiguration');
  const reconfigure = source.indexOf("await chooseOperationWithPlaywright(page, action, 'APPEND', sourceRevisions)", restartTarget);
  const removalCancel = source.indexOf("Cancel the APPEND removal proposal before actual removal");
  const removalCancelCheck = source.indexOf("Canceling APPEND removal preserves the exact saved schema, step, and rows before removal");
  const actualRemoval = source.indexOf("await removeCombineAndRestoreEmptyRootWithPlaywright(context, page, action, report, explorer, target, emptyTargetBaseline, savedStepId, 'APPEND');");
  assert.ok(firstCancel >= 0 && firstCancel < restartTarget && restartTarget < initialCancelCheck && initialCancelCheck < reconfigure,
    'initial proposal must be canceled and its target checked before a distinct target is created and reconfigured');
  assert.ok(removalCancel >= 0 && removalCancel < removalCancelCheck && removalCancelCheck < actualRemoval,
    'removal cancellation and its persisted-state assertion must precede the actual removal proposal');
  assert.equal((source.slice(source.indexOf('export const appendWorkflow'), actualRemoval).match(/appendReloadToVisibleResultWithPlaywright\(/g) ?? []).length, 4,
    'the four pre-removal APPEND reloads must use the exact DOM timing helper');
  assert.ok(source.includes("name: 'Initial APPEND proposal Cancel reload reaches the exact rooted empty target within five seconds'"));
  assert.ok(source.includes("? 'APPEND removal reload reaches the exact rooted empty target within five seconds'"));
});

test('the registry requires the 45-check APPEND extension and points to its lifecycle evidence', () => {
  const contract = scenarioCaseFor(registry.find(entry => entry.id === 'builder-combine'), 'append');
  assert.equal(contract.requiredChecks.length, 45);
  assert.deepEqual(contract.requiredChecks.slice(-8), [
    'initial APPEND proposal previews the literal eight-row ID union before Cancel',
    'Canceling initial APPEND proposal preserves the exact fresh rooted target before creating a distinct target for reconfiguration',
    'Canceling APPEND removal preserves the exact saved schema, step, and rows before removal',
    'Initial APPEND proposal Cancel reload reaches the exact rooted empty target within five seconds',
    'APPEND Apply reload reaches the exact saved rows and headers within five seconds',
    'APPEND saved-edit Cancel reload reaches the exact saved rows and headers within five seconds',
    'APPEND edit Apply reload reaches the exact saved rows and headers within five seconds',
    'APPEND removal reload reaches the exact rooted empty target within five seconds',
  ]);
  const coverage = registry.find(entry => entry.id === 'builder-combine').coverage
    .find(entry => entry.feature === 'published-table APPEND three-input null-padding lifecycle');
  assert.deepEqual(coverage.acceptance.checks, {
    choice: 12, proposal: 16, cancel: 38, apply: 19, savedRows: 19, reload: 41, edit: 24, restoration: 27,
  });
  assert.deepEqual(lifecycleAcceptanceDrift(registry), []);
});
