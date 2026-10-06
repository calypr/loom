import assert from 'node:assert/strict';
import { assertExactMultiset } from '../group-related-multiset.mjs';

assertExactMultiset([["Patient/1", "2"], ["Patient/2", "1"]], [["Patient/2", "1"], ["Patient/1", "2"]], 'order-independent exact population');
assertExactMultiset([["Patient/1", ["final", "preliminary"]]], [["Patient/1", ["preliminary", "final"]]], 'typed ALL value order');
assert.throws(
  () => assertExactMultiset([["Patient/1", "2"], ["Patient/1", "2"]], [["Patient/1", "2"], ["Patient/2", "1"]], 'duplicate cannot hide missing row'),
  /row values or multiplicities differ/,
  'a duplicate replacing an omitted expected row must fail',
);
assert.throws(
  () => assertExactMultiset([["Patient/1", ["final", "final"]]], [["Patient/1", ["final", "preliminary"]]], 'ALL contributor values'),
  /row values or multiplicities differ/,
  'a repeated ALL value cannot replace a distinct contributor value',
);
console.log('exact multiset checks passed, including duplicate-versus-missing rejection');
