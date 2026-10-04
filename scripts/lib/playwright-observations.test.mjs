import assert from 'node:assert/strict';
import test from 'node:test';
import { waitForCondition } from './playwright-observations.mjs';

test('observable waits pass a callback and structured condition to Playwright', async () => {
  let observed;
  const page = {
    waitForFunction: async (predicate, condition, options) => {
      observed = { predicate, condition, options };
      return 'observed';
    },
  };
  const condition = { kind: 'enabled', selector: 'button[aria-label="Apply"]' };
  assert.equal(await waitForCondition(page, condition, 4500), 'observed');
  assert.equal(typeof observed.predicate, 'function');
  assert.deepEqual(observed.condition, condition);
  assert.deepEqual(observed.options, { timeout: 4500 });
});
