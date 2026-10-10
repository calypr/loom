import test from 'node:test';
import assert from 'node:assert/strict';
import { proposalPreviewReadinessExpression } from '../proposal-preview-readiness.mjs';

const siblingProposalDocument = ({ proposalId, receiptId, outputId, rowCount }) => {
  const panel = {
    getAttribute(name) {
      if (name === 'data-proposal-id') return proposalId;
      if (name === 'data-proposal-status') return 'ready';
      return null;
    },
    querySelector() {
      throw new Error('proposal preview must be found as a document-level sibling');
    },
  };
  const preview = {
    getAttribute(name) {
      if (name === 'data-preview-receipt-id') return receiptId;
      if (name === 'data-preview-output-id') return outputId;
      if (name === 'data-preview-status') return 'ready';
      return null;
    },
    querySelectorAll() {
      return Array.from({ length: rowCount }, () => ({}));
    },
  };

  return {
    querySelector(selector) {
      if (selector.includes('construction-proposal-panel')) return panel;
      if (selector.includes('construction-proposal-preview')) return preview;
      return null;
    },
  };
};

const evaluateReadiness = (document, outputId, rowCount) => {
  const expression = proposalPreviewReadinessExpression(outputId, rowCount);
  return new Function('document', `return ${expression};`)(document);
};

test('proposal readiness finds the ready preview as a sibling and binds receipt, output, and rows', () => {
  const document = siblingProposalDocument({
    proposalId: 'receipt-1', receiptId: 'receipt-1', outputId: 'target-1', rowCount: 8,
  });

  assert.equal(evaluateReadiness(document, 'target-1', 8), true);
});

test('proposal readiness rejects a preview from a different proposal receipt', () => {
  const document = siblingProposalDocument({
    proposalId: 'receipt-current', receiptId: 'receipt-stale', outputId: 'target-1', rowCount: 8,
  });

  assert.equal(evaluateReadiness(document, 'target-1', 8), false);
});

test('proposal readiness rejects a different output or row count', () => {
  const document = siblingProposalDocument({
    proposalId: 'receipt-1', receiptId: 'receipt-1', outputId: 'target-1', rowCount: 7,
  });

  assert.equal(evaluateReadiness(document, 'target-2', 7), false);
  assert.equal(evaluateReadiness(document, 'target-1', 8), false);
});

test('proposal readiness requires a target output ID and valid expected row count', () => {
  assert.throws(() => proposalPreviewReadinessExpression('', 8), /expected output ID/);
  assert.throws(() => proposalPreviewReadinessExpression('target-1', -1), /non-negative integer/);
});
