import test from 'node:test';
import assert from 'node:assert/strict';
import { supportsOwnedDatasetReuse } from '../cli.mjs';

test('owned dataset reuse is limited to compatible read-only Builder cases', () => {
  assert.equal(supportsOwnedDatasetReuse('builder-authoring', 'authoring'), true);
  assert.equal(supportsOwnedDatasetReuse('builder-authoring', 'suggestions'), true);
  assert.equal(supportsOwnedDatasetReuse('builder-load', 'list'), true);
  assert.equal(supportsOwnedDatasetReuse('builder-load', 'state'), true);
  assert.equal(supportsOwnedDatasetReuse('builder-load', undefined), false);
  assert.equal(supportsOwnedDatasetReuse('builder-controls', 'tables'), false);
  assert.equal(supportsOwnedDatasetReuse('builder-authoring', 'cohort-recode'), false);
});
