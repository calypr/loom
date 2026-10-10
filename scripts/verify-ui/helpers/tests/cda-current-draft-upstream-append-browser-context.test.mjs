import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { createReport, recordCheck, reportDimensions } from '../report.mjs';
import { classifyEvidence } from '../coverage-status.mjs';
import { scenarioCaseFor } from '../../registry.mjs';
import { CDA_ACTION_TO_RENDER_BUDGET_MS, summarizeCdaActionToRenderTimings } from '../cda-action-to-render-budget.mjs';
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
  assert.equal(dimensions.length, 10, 'All lifecycle, action-budget, and attachment-to-grid checks must be reported');

  const report = createReport({ scenario: 'cda-current-draft-upstream-append', caseName: 'patient-membership-handoff', target: null,
    evidenceDirectory: '/tmp/patient-membership-report-dimensions' });
  dimensions.forEach((dimension, index) => {
    assert.ok(reportDimensions.includes(dimension), `Patient handoff check ${index + 1} uses unsupported report dimension '${dimension}'`);
    assert.doesNotThrow(() => recordCheck(report, dimension, `Patient handoff check ${index + 1}`, true));
  });
});


test('Patient Replace-to-grid timing uses one continuous production budget across the exact APPEND render', async () => {
  const workflow = await readFile(new URL('../../workflows/cda-current-draft-upstream-append-workflow.mjs', import.meta.url), 'utf8');
  const start = workflow.indexOf('const attachSelection = async (');
  const end = workflow.indexOf('\n      };\n\n      const narrowed =', start);
  assert.ok(start >= 0 && end > start, 'Patient replacement helper must remain identifiable');
  const attachSelection = workflow.slice(start, end);
  const timerStart = attachSelection.indexOf('startedAtMonotonicMs: performance.now()');
  const replaceClick = attachSelection.indexOf("await click('Replace the saved Patient collection");
  const exactGrid = attachSelection.indexOf('const autoPreview = await waitForAutoAppendPreview');
  const timerEnd = attachSelection.indexOf('transitionTiming.completedAtMonotonicMs = performance.now()');
  assert.ok(timerStart >= 0 && timerStart < replaceClick, 'The one monotonic clock starts immediately before Replace');
  assert.ok(replaceClick < exactGrid && exactGrid < timerEnd, 'The same clock ends after exact APPEND rows and receipt are read');
  assert.match(workflow, /summarizeCdaActionToRenderTimings\(attachmentToGridTimings\.map/);
  assert.match(workflow, /both Patient collection replacement click-to-grid transitions complete within five seconds/);

  const replaceClickMs = 2_501;
  const navigationAndRenderMs = 2_500;
  assert.ok(replaceClickMs <= CDA_ACTION_TO_RENDER_BUDGET_MS);
  assert.ok(navigationAndRenderMs <= CDA_ACTION_TO_RENDER_BUDGET_MS);
  const summary = summarizeCdaActionToRenderTimings([
    { name: 'Replace click through exact APPEND grid', durationMs: replaceClickMs + navigationAndRenderMs },
  ]);
  assert.equal(summary.maximumDurationMs, 5_001);
  assert.equal(summary.withinBudget, false, 'A reset per-action clock would pass both parts; the continuous interval must fail');
});


test('pre-instrumentation Patient lifecycle remains partial until attachment-to-grid evidence is present', () => {
  const contract = scenarioCaseFor('cda-current-draft-upstream-append', 'patient-membership-handoff');
  const newTransitionCheck = 'both Patient collection replacement click-to-grid transitions complete within five seconds';
  assert.equal(contract.requiredChecks.at(-1), newTransitionCheck);
  const priorChecks = contract.requiredChecks.filter(name => name !== newTransitionCheck);
  const priorReport = {
    schemaVersion: 2,
    status: 'passed',
    dimensions: Object.fromEntries(reportDimensions.map(dimension => [dimension, { status: 'passed' }])),
    assertions: priorChecks.map(name => ({ name, status: 'passed' })),
  };
  assert.equal(priorChecks.length, 9, 'Retain the pre-instrumentation behavioral evidence set');
  assert.equal(classifyEvidence(priorReport, contract.requiredChecks, contract, { status: 'passed' }), 'partial');
});
