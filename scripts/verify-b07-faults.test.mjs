import test from 'node:test';
import assert from 'node:assert/strict';
import { assertNoPublicationLeak, authoringURL, explorerURL, publicationStateSignature } from './verify-b07-faults.mjs';

const target = { apiUrl: 'http://127.0.0.1:8182', fixtureProject: 'loom_dev_verify_faults', composeProject: 'loom-dev-faults' };

test('fault driver URLs use the isolated fixture project and encoded explorer', () => {
  assert.equal(authoringURL(target, 'explorer/faults', '/publish'), 'http://127.0.0.1:8182/api/v1/projects/loom_dev_verify_faults/explorers/explorer%2Ffaults/authoring/v2/publish');
  assert.equal(explorerURL(target, 'explorer/faults'), 'http://127.0.0.1:8182/api/v1/projects/loom_dev_verify_faults/explorers/explorer%2Ffaults');
});

test('publication state signature captures the active revision and bounded output count', () => {
  assert.deepEqual(publicationStateSignature({
    active: { revisionId: 'revision-1' },
    runtime: {
      publication: { executionId: 'execution-1' },
      qualityReports: [{ receiptId: 'receipt-1' }],
      outputs: [{ outputId: 'patients', rowCount: 2 }],
    },
  }), {
    revisionId: 'revision-1',
    executionId: 'execution-1',
    receiptIds: ['receipt-1'],
    rowCount: 2,
    outputCount: 1,
  });
});

test('fault response redaction rejects candidate identity and publication fields', () => {
  assert.doesNotThrow(() => assertNoPublicationLeak({ value: { error: { code: 'QUALITY_EVIDENCE_INVALID' } } }, 'receipt-candidate', ['restricted-marker']));
  assert.throws(() => assertNoPublicationLeak({ value: { error: { code: 'QUALITY_EVIDENCE_INVALID', details: 'receipt-candidate' } } }, 'receipt-candidate'), /candidate receipt/);
  assert.throws(() => assertNoPublicationLeak({ value: { error: { details: { rowCount: 2 } } } }, 'receipt-candidate'), /publication field rowcount/);
  assert.throws(() => assertNoPublicationLeak({ value: { error: { details: { restricted: 'hidden-value' } } } }, 'receipt-candidate', ['hidden-value']), /restricted marker/);
});
