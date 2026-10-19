import assert from 'node:assert/strict';
import test from 'node:test';
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
