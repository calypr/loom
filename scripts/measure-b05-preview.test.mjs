import test from 'node:test';
import assert from 'node:assert/strict';
import { routeCount } from './measure-b05-preview.mjs';

test('routeCount includes every nested authored traversal', () => {
  assert.equal(routeCount({
    children: [
      { children: [{ children: [] }] },
      { children: [] },
    ],
  }), 3);
});
