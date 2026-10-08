import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { createReport, recordCheck, reportDimensions } from '../report.mjs';
import {
  readSelectOptionsInPage,
} from '../../workflows/cda-current-draft-upstream-append-workflow.mjs';

test('serialized select-option reader returns raw DOM text without Node normalize closure', async () => {
  const workflow = await readFile(new URL('../../workflows/cda-current-draft-upstream-append-workflow.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /page\.locator\(selector\)\.evaluate\(readSelectOptionsInPage\)/);
  assert.doesNotMatch(workflow, /normalize\(option\.textContent\)/);
  const browserReader = runInNewContext('(' + readSelectOptionsInPage.toString() + ')');
  const options = browserReader({ options: [
    { value: 'table-1', textContent: '  Patient   FHIR ID  ', disabled: false },
    { value: '', textContent: null, disabled: true },
  ] });
  assert.deepEqual(JSON.parse(JSON.stringify(options)), [
    { value: 'table-1', text: '  Patient   FHIR ID  ', disabled: false },
    { value: '', text: null, disabled: true },
  ]);
});

test('Patient membership checks use dimensions accepted by the native report writer', async () => {
  const workflow = await readFile(new URL('../../workflows/cda-current-draft-upstream-append-workflow.mjs', import.meta.url), 'utf8');
  const start = workflow.indexOf('const runPatientMembershipHandoff = async () => {');
  const end = workflow.indexOf('\n    };\n\n    if (!membershipOnly)', start);
  assert.ok(start >= 0 && end > start, 'Patient handoff check block must remain identifiable');
  const dimensions = [...workflow.slice(start, end).matchAll(/requireCheck\(\s*'([^']+)'/g)]
    .map(([, dimension]) => dimension);
  assert.equal(dimensions.length, 9, 'All eight lifecycle checks and the performance check must be reported');

  const report = createReport({ scenario: 'cda-current-draft-upstream-append', caseName: 'patient-membership-handoff', target: null,
    evidenceDirectory: '/tmp/patient-membership-report-dimensions' });
  dimensions.forEach((dimension, index) => {
    assert.ok(reportDimensions.includes(dimension), `Patient handoff check ${index + 1} uses unsupported report dimension '${dimension}'`);
    assert.doesNotThrow(() => recordCheck(report, dimension, `Patient handoff check ${index + 1}`, true));
  });
});
