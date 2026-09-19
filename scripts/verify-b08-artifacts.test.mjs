import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';
import { artifactID, publicationIdentity, verifyMemberChecksums } from './verify-b08-artifacts.mjs';

test('artifact ID matches the immutable Go record field order', () => {
  const identity = { project: 'study/project', explorerId: 'patients', revisionId: 'revision-a', outputId: 'patients', receiptId: 'receipt-a', executionId: 'execution-a', idempotencyKey: 'request-a' };
  const encoded = '{"Project":"study/project","ExplorerID":"patients","RevisionID":"revision-a","OutputID":"patients","ReceiptID":"receipt-a","ExecutionID":"execution-a","IdempotencyKey":"request-a"}';
  assert.equal(artifactID(identity), `artifact_${createHash('sha256').update(encoded).digest('hex')}`);
});

test('publication identity reads the immutable active runtime contract', () => {
  assert.deepEqual(publicationIdentity({
    active: { revisionId: 'revision-a' },
    runtime: { publication: { executionId: 'execution-a' }, qualityReports: [{ receiptId: 'receipt-a' }], outputs: [{ outputId: 'patients' }] },
  }), { revisionId: 'revision-a', executionId: 'execution-a', receiptId: 'receipt-a', outputId: 'patients' });
});

test('member checksum verification rejects changed bytes', () => {
  const bytes = Buffer.from('stable');
  const manifest = { members: [{ name: 'data.csv', bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') }] };
  assert.equal(verifyMemberChecksums(new Map([['data.csv', bytes]]), manifest), true);
  assert.equal(verifyMemberChecksums(new Map([['data.csv', Buffer.from('changed')]]), manifest), false);
});
