import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readViewerRowFileDownloadOracle, VIEWER_ROW_FILE_DOWNLOAD } from '../viewer-row-file-download-oracle.mjs';

const fixtureDir = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../testdata/viewer-row-file-download');
const expectedSource = readFileSync(join(fixtureDir, 'DocumentReference.ndjson'), 'utf8').trim();
const expectedPayload = Buffer.from(VIEWER_ROW_FILE_DOWNLOAD.payload, 'utf8');

const withFixture = (source, payload = expectedPayload, run) => {
  const directory = mkdtempSync(join(tmpdir(), 'loom-filecell-oracle-'));
  try {
    writeFileSync(join(directory, 'DocumentReference.ndjson'), source);
    writeFileSync(join(directory, 'payload.bin'), payload);
    run(directory);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
};

test('Basic Viewer FileCell oracle pins the exact source UUID and literal download payload', () => {
  const oracle = readViewerRowFileDownloadOracle(fixtureDir);
  assert.deepEqual(oracle.fileIDs, ['7f6a8e2d-4c91-4b30-8a72-6e59d03c1f84']);
  assert.equal(oracle.fileID, VIEWER_ROW_FILE_DOWNLOAD.fileID);
  assert.deepEqual(oracle.payloadBytes, expectedPayload);
  assert.equal(oracle.payloadByteLength, 41);
  assert.equal(oracle.payloadSHA256, '7b50a05814262850f1356dd2ff00a8ddf628f916869ff53bcd5cfc6974f49726');
  assert.equal(oracle.downloadName, 'filecell-report.bin');
});

test('FileCell oracle rejects malformed NDJSON', () => {
  withFixture('{not-json}\n', expectedPayload, directory => {
    assert.throws(() => readViewerRowFileDownloadOracle(directory), SyntaxError);
  });
});

test('FileCell oracle rejects a wrong resource shape or identity', () => {
  const wrongResource = JSON.stringify({ resourceType: 'Patient', id: VIEWER_ROW_FILE_DOWNLOAD.fileID });
  withFixture(`${wrongResource}\n`, expectedPayload, directory => {
    assert.throws(() => readViewerRowFileDownloadOracle(directory), /exact independent DocumentReference shape/);
  });
  const wrongIdentity = expectedSource.replace(VIEWER_ROW_FILE_DOWNLOAD.fileID, '7f6a8e2d-4c91-4b30-8a72-6e59d03c1f85');
  withFixture(`${wrongIdentity}\n`, expectedPayload, directory => {
    assert.throws(() => readViewerRowFileDownloadOracle(directory), /exact independent DocumentReference shape/);
  });
});

test('FileCell oracle rejects extra and missing source identities', () => {
  withFixture(`${expectedSource}\n${expectedSource}\n`, expectedPayload, directory => {
    assert.throws(() => readViewerRowFileDownloadOracle(directory), /exactly one DocumentReference identity/);
  });
  withFixture('', expectedPayload, directory => {
    assert.throws(() => readViewerRowFileDownloadOracle(directory), /exactly one DocumentReference identity/);
  });
});

test('FileCell oracle rejects any change to the literal payload bytes', () => {
  withFixture(`${expectedSource}\n`, Buffer.from('altered payload\n'), directory => {
    assert.throws(() => readViewerRowFileDownloadOracle(directory), /literal expected bytes/);
  });
});
