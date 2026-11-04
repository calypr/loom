import assert from 'node:assert/strict';
import test from 'node:test';
import { lifecycleAcceptanceDrift, registry, scenarioCaseFor } from '../../registry.mjs';
import {
  combineCancellationPreservationEvidence,
  measureActionToDOMResult,
  rootedEmptyTargetAppliedExpression,
  savedAppendPreviewAppliedExpression,
} from '../builder-combine-helpers.mjs';

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

test('saved APPEND readiness preserves the exact eight-row multiset and public headers', () => {
  const expectedRows = [
    ['combine-observation-final-1', 'final', '—'],
    ['combine-observation-final-2', 'final', '—'],
    ['combine-observation-preliminary', 'preliminary', '—'],
    ['combine-observation-unmatched', 'unknown', '—'],
    ['combine-observation-final-1', 'final', '—'],
    ['combine-observation-final-2', 'final', '—'],
    ['combine-observation-preliminary', 'preliminary', '—'],
    ['combine-fixture-patient', '—', 'female'],
  ];
  const expectedHeaders = ['Record ID', 'Status', 'Patient sex'];
  const expression = savedAppendPreviewAppliedExpression(outputId, expectedHeaders, expectedRows);
  const documentFor = overrides => savedPreviewDocument({ actualHeaders: expectedHeaders, actualRows: expectedRows, ...overrides });
  assert.equal(evaluate(expression, documentFor()), true);
  assert.equal(evaluate(expression, documentFor({ actualRows: [...expectedRows].reverse() })), true,
    'an unauthored row order must not change the exact saved multiset');
  assert.equal(evaluate(expression, documentFor({ previewOutputId: 'out_another_target' })), false,
    'ready data must belong to the selected output');
  assert.equal(evaluate(expression, documentFor({ actualRows: [...expectedRows.slice(0, 7), expectedRows[0]] })), false,
    'equal row counts cannot hide a duplicate and an omitted Patient row');
  assert.equal(evaluate(expression, documentFor({ actualHeaders: ['Record ID', 'Wrong status', 'Patient sex'] })), false,
    'matching row values cannot hide a mismatched public header');
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
