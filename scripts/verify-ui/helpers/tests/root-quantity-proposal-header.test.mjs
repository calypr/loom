import assert from 'node:assert/strict';
import test from 'node:test';
import { assertProposalPanelHeaders, readProposalPanelSnapshot } from '../../workflows/root-quantity-pivot-workflow.mjs';

const readHeadersForCells = cells => {
  const panel = {
    querySelectorAll(selector) {
      assert.equal(selector, 'th');
      return cells;
    },
  };
  const documentLike = {
    querySelector(selector) {
      assert.equal(selector, '[data-testid="construction-proposal-preview"]');
      return panel;
    },
    querySelectorAll(selector) {
      assert.equal(selector, '[data-testid="construction-proposal-preview-row"]');
      return [];
    },
  };
  return new Function('document', `return (${readProposalPanelSnapshot.toString()})();`)(documentLike).headers;
};

const headerCell = (...spanTexts) => ({
  querySelectorAll(selector) {
    assert.equal(selector, ':scope > span');
    return spanTexts.map(innerText => ({ innerText }));
  },
});

test('proposal header reads the rendered label and logical type spans and rejects missing or wrong types', () => {
  const columns = [{ label: 'Quantity total', logicalType: 'decimal' }];
  const actual = readHeadersForCells([headerCell('Quantity total', 'decimal')]);

  assert.deepEqual(actual, [{ label: 'Quantity total', logicalType: 'decimal', spanCount: 2 }]);
  assertProposalPanelHeaders(actual, columns, 'Typed Pivot');
  assert.throws(
    () => assertProposalPanelHeaders(readHeadersForCells([headerCell('Quantity total')]), columns, 'Typed Pivot'),
    /exact native labels and logical types/,
  );
  assert.throws(
    () => assertProposalPanelHeaders(readHeadersForCells([headerCell('Quantity total', 'string')]), columns, 'Typed Pivot'),
    /exact native labels and logical types/,
  );
});
