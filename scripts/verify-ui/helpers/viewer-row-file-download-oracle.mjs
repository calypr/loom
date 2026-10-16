import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

export const VIEWER_ROW_FILE_DOWNLOAD = Object.freeze({
  fileID: '7f6a8e2d-4c91-4b30-8a72-6e59d03c1f84',
  downloadName: 'filecell-report.bin',
  payload: 'Loom FileCell native-download fixture v1\n',
  payloadBytes: 41,
  payloadSHA256: '7b50a05814262850f1356dd2ff00a8ddf628f916869ff53bcd5cfc6974f49726',
});

const expectedDocumentReference = Object.freeze({
  resourceType: 'DocumentReference',
  id: VIEWER_ROW_FILE_DOWNLOAD.fileID,
  status: 'current',
  description: 'FileCell native download fixture',
  content: [{
    attachment: {
      contentType: 'application/octet-stream',
      url: 'urn:loom:fixture:filecell-report-v1',
      title: VIEWER_ROW_FILE_DOWNLOAD.downloadName,
    },
  }],
});
const expectedPayload = Buffer.from(VIEWER_ROW_FILE_DOWNLOAD.payload, 'utf8');

export const readViewerRowFileDownloadOracle = fixtureDir => {
  const documentReferencePath = join(fixtureDir, 'DocumentReference.ndjson');
  const documentReferenceBytes = readFileSync(documentReferencePath);
  const lines = documentReferenceBytes.toString('utf8').split(/\r?\n/).filter(line => line.length > 0);
  assert.equal(lines.length, 1, 'FileCell source fixture must contain exactly one DocumentReference identity');
  const records = lines.map(line => JSON.parse(line));
  assert.deepEqual(records, [expectedDocumentReference],
    'FileCell source fixture must retain the exact independent DocumentReference shape and UUID');

  const payloadPath = join(fixtureDir, 'payload.bin');
  const payloadBytes = readFileSync(payloadPath);
  assert.deepEqual(payloadBytes, expectedPayload, 'FileCell fixture payload must match the literal expected bytes');
  const payloadSHA256 = createHash('sha256').update(payloadBytes).digest('hex');
  assert.equal(payloadBytes.byteLength, VIEWER_ROW_FILE_DOWNLOAD.payloadBytes,
    'FileCell fixture payload must retain the literal expected byte length');
  assert.equal(payloadSHA256, VIEWER_ROW_FILE_DOWNLOAD.payloadSHA256,
    'FileCell fixture payload must retain the literal expected SHA-256');

  return {
    documentReferencePath,
    documentReferenceSHA256: createHash('sha256').update(documentReferenceBytes).digest('hex'),
    fileID: VIEWER_ROW_FILE_DOWNLOAD.fileID,
    fileIDs: [VIEWER_ROW_FILE_DOWNLOAD.fileID],
    documentReferences: records,
    payloadPath,
    payloadBytes,
    payloadByteLength: payloadBytes.byteLength,
    payloadSHA256,
    downloadName: VIEWER_ROW_FILE_DOWNLOAD.downloadName,
  };
};
