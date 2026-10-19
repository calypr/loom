import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { runInNewContext } from 'node:vm';
import { rootQuantityPivotLifecycleEvidence } from '../../workflows/root-quantity-pivot-workflow.mjs';

const stringDIdentity = JSON.stringify({ kind: 'STRING', string: 'd' });
const lifecycleOracle = () => ({
  categoryIdentities: ['MISSING', stringDIdentity],
  categories: [
    { present: false, value: null, rowCount: 778185 },
    { present: true, value: 'd', rowCount: 37076 },
  ],
  duplicateWitness: {
    status: 'final',
    present: true,
    value: 'd',
    rowCount: 37076,
    numericCount: 37076,
    valueSum: 2619628033,
    valueMax: 3444672,
  },
});

const lifecycleRequests = () => [
  { endpoint: 'construction-category-discoveries', status: 200 },
  { endpoint: 'construction-category-discoveries', status: 200 },
  { endpoint: 'construction-proposals', status: 422, response: { error: { code: 'TABLE_PIVOT_CELL_CARDINALITY' } } },
  { endpoint: 'construction-proposals', status: 200 },
  { endpoint: 'construction-proposals', status: 422, response: { error: { code: 'TABLE_PIVOT_CELL_CARDINALITY' } } },
  { endpoint: 'construction-proposals', status: 200 },
  { endpoint: 'construction-proposals', status: 200 },
  { endpoint: 'construction-proposals', status: 200 },
  { endpoint: 'commands', status: 200 },
  { endpoint: 'commands', status: 200 },
  { endpoint: 'commands', status: 200 },
];

test('full-population Pivot evidence preserves exact request counts and raw MISSING/string d values', () => {
  const evidence = rootQuantityPivotLifecycleEvidence(lifecycleRequests(), lifecycleOracle());

  assert.deepEqual(evidence.requestCounts, {
    categoryDiscoveries: 2,
    proposals: 6,
    expectedCardinalityValidations: 2,
    successfulProposals: 4,
    commands: 3,
  });
  assert.deepEqual(evidence.rawCategoryIdentities, ['MISSING', stringDIdentity]);
  assert.deepEqual(evidence.rawCategoryRows, { missing: 778185, stringD: 37076 });
  assert.deepEqual(evidence.duplicateWitness, {
    category: stringDIdentity,
    status: 'final',
    rowCount: 37076,
    numericCount: 37076,
    sum: 2619628033,
    max: 3444672,
  });
  assert.notEqual(evidence.duplicateWitness.sum, evidence.duplicateWitness.max);
});

test('full-population Pivot evidence rejects request drift and a collapsed MISSING category', () => {
  assert.throws(() => rootQuantityPivotLifecycleEvidence(lifecycleRequests().slice(0, -1), lifecycleOracle()));

  const collapsedOracle = lifecycleOracle();
  collapsedOracle.categoryIdentities = [stringDIdentity];
  collapsedOracle.categories = collapsedOracle.categories.filter(category => category.present);
  assert.throws(() => rootQuantityPivotLifecycleEvidence(lifecycleRequests(), collapsedOracle), /MISSING category/);
});

test('reopening a Pivot after Cancel reads browser state through the page snapshot boundary', async () => {
  const source = await readFile(new URL('../../workflows/root-quantity-pivot-workflow.mjs', import.meta.url), 'utf8');
  const start = source.indexOf('const openPivotEditor = async () => {');
  const end = source.indexOf('\n};', start);
  assert(start >= 0 && end > start, 'Workflow must retain the focused Pivot reopening helper');
  const helper = source.slice(start, end + 3);

  const runHelper = async ({ settingsOpen, reshapePressed }) => {
    const clicks = [];
    const openPivotEditor = runInNewContext(`${helper}\nopenPivotEditor`, {
      assert,
      isFixture: false,
      page: { locator: () => ({ getAttribute: async () => reshapePressed ? 'true' : 'false' }) },
      inspectPage: async (_page, inspect) => {
        assert.equal(typeof inspect, 'function', 'DOM inspection must be passed across the Playwright page boundary');
        return { pivotEditorOpen: false, proposalOpen: false, settingsOpen };
      },
      waitForObservable: async () => {},
      clickNative: async (_page, selector) => clicks.push(selector),
    });
    await openPivotEditor();
    return clicks;
  };

  assert.deepEqual(await runHelper({ settingsOpen: true, reshapePressed: true }), [
    '[data-testid="construction-action-table-pivot-rows"]',
  ]);
  assert.deepEqual(await runHelper({ settingsOpen: false, reshapePressed: false }), [
    '[data-testid="construction-rows-settings-trigger"]',
    '[data-testid="construction-row-intent-reshape"]',
    '[data-testid="construction-action-table-pivot-rows"]',
  ]);
});
