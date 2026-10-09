import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { createReport, recordCheck, reportDimensions } from '../report.mjs';

const workflowSource = readFileSync(new URL('../../workflows/builder-authoring.mjs', import.meta.url), 'utf8');

test('cohort recode uses only supported report dimensions for every workflow check', () => {
  const checks = [...workflowSource.matchAll(/\bcheck\(\s*'([^']+)'\s*,\s*'([^']+)'/g)]
    .map(([, dimension, name]) => ({ dimension, name }));
  assert.ok(checks.length >= 11, 'the registered cohort lifecycle checks must remain present');

  const report = createReport({
    scenario: 'builder-authoring',
    caseName: 'cohort-recode',
    target: {},
    evidenceDirectory: '/private/tmp/cohort-recode-report-dimension-test',
  });
  for (const { dimension, name } of checks) {
    assert.ok(reportDimensions.includes(dimension), `${name} uses supported report dimension ${dimension}`);
    assert.doesNotThrow(() => recordCheck(report, dimension, name, true), `${name} is accepted by recordCheck`);
  }

  const cancelCheck = checks.find(({ name }) => name ===
    'Cancel preserves the saved ALL recoding, exact Builder draft, and visible category values');
  assert.equal(cancelCheck?.dimension, 'correctness', 'Cancel verifies saved-state correctness');
});
