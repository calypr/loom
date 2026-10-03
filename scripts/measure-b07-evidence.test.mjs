import test from 'node:test';
import assert from 'node:assert/strict';
import { median, parseDockerMemoryBytes, parseProcStatusRSSBytes, parseVerifyFastReport, qualityEvidenceJSONBytes, tracePageSummary } from './measure-b07-evidence.mjs';

test('median handles odd and even sample sets', () => {
  assert.equal(median([9, 1, 4]), 4);
  assert.equal(median([9, 1, 4, 8]), 6);
});

test('Docker memory parser accepts binary and decimal units', () => {
  assert.equal(parseDockerMemoryBytes('31.87MiB / 1.943GiB'), Math.round(31.87 * 1024 ** 2));
  assert.equal(parseDockerMemoryBytes('1.5 GB / 4 GB'), 1_500_000_000);
});

test('proc status parser converts VmRSS KiB to bytes', () => {
  assert.equal(parseProcStatusRSSBytes('Name:\tloom-api\nVmRSS:\t2048 kB\n'), 2 * 1024 * 1024);
  assert.throws(() => parseProcStatusRSSBytes('Name:\tloom-api\n'), /VmRSS/);
});

test('quality evidence bytes are measured from stable JSON encoding', () => {
  assert.equal(qualityEvidenceJSONBytes([]), 2);
  assert.equal(qualityEvidenceJSONBytes([{ output: 'patients', completeness: 'COMPLETE' }]), Buffer.byteLength('[{"output":"patients","completeness":"COMPLETE"}]'));
  assert.equal(qualityEvidenceJSONBytes(null), 2);
});

test('trace page summary enforces the requested contribution bound', () => {
  assert.deepEqual(tracePageSummary({ status: 'VALUE', contributions: [{ resourceId: 'one' }], hasMore: true, nextOffset: 1, complete: true }, 2), {
    status: 'VALUE', contributionCount: 1, hasMore: true, nextOffset: 1, complete: true,
  });
  assert.throws(() => tracePageSummary({ contributions: [{}, {}, {}], hasMore: false }, 2), /returned 3 contributions/);
  assert.throws(() => tracePageSummary({ contributions: [{}], hasMore: true, nextOffset: 9 }, 2), /nextOffset/);
});

test('verify-fast report parser rejects canonical and non-isolated targets', () => {
  const report = {
    status: 'passed',
    target: {
      apiUrl: 'http://127.0.0.1:8182', composeProject: 'loom-dev-test', project: 'loom_dev_verify_sample',
      explorerId: 'loom-dev-verification-sample', generation: 'fixture-v1',
      materialization: { id: 'execution:output', name: 'patients', selector: { output: 'patients' } },
    },
  };
  assert.equal(parseVerifyFastReport(report, '/tmp/report.json').outputId, 'patients');
  assert.throws(() => parseVerifyFastReport({ ...report, target: { ...report.target, project: 'NCPI_ACCEPTANCE' } }), /loom_dev_verify/);
  assert.throws(() => parseVerifyFastReport({ ...report, status: 'failed' }), /passed verify-fast/);
});
