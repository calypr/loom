import { expect, test } from '../helpers/cda-fixtures.mjs';
import { nestedAuthoredExpandWorkflow } from '../workflows/verify-cda-nested-authored-expand-browser.mjs';

const persistenceChecks = [
  'Reload preserves native EXPAND output and unchanged RECORDS row definition',
  'Edited EXPAND output and unchanged RECORDS definition persist after reload',
  'Reload after removal preserves source-record restoration',
];

const runNestedExpandLifecycle = async ({ page, cda }) => {
  test.setTimeout(300_000);
  await nestedAuthoredExpandWorkflow({ page, cda });
  const expectedWitnessMode = cda.caseName === 'nested-coding-expand-lifecycle-single-per-component'
    ? 'single-coding-per-component' : 'multi-coding-component';
  expect(cda.report.oracle.witness.mode).toBe(expectedWitnessMode);
  const performance = cda.report.assertions.find(assertion =>
    assertion.name === 'All native action and action-to-render checkpoints complete within five seconds');
  expect(performance).toMatchObject({ dimension: 'performance', status: 'passed' });
  expect(performance.evidence.workflowCheckpoints.length).toBeGreaterThan(0);
  expect(performance.evidence.workflowCheckpoints.every(checkpoint =>
    typeof checkpoint.name === 'string' && Number.isFinite(checkpoint.durationMs) &&
    checkpoint.durationMs >= 0 && checkpoint.durationMs <= 5000)).toBe(true);
  expect(performance.evidence.nativeActionCount).toBeGreaterThan(0);
  for (const name of persistenceChecks) {
    const assertion = cda.report.assertions.find(candidate => candidate.name === name);
    expect(assertion, name).toMatchObject({ dimension: 'persistence', status: 'passed' });
    expect(assertion.evidence.unchangedSavedState).toBe(true);
    expect(assertion.evidence.before).toEqual(assertion.evidence.after);
    expect(assertion.evidence.before.snapshotToken).toBeTruthy();
    expect(assertion.evidence.before.draftVersion).toBeGreaterThan(0);
    expect(assertion.evidence.before.draftDigest).toBeTruthy();
    expect(assertion.evidence.before.outputId).toBeTruthy();
    expect(assertion.evidence.before.document.rows.kind).toBe('RECORDS');
    expect(assertion.evidence.before.document.columns.length).toBeGreaterThan(0);
    expect(assertion.evidence.before.preview.rowCount).toBeGreaterThan(0);
  }
  if (cda.report.status === 'partial') {
    expect(cda.report.failures).toEqual([]);
    expect(cda.report.gaps.map(gap => gap.assertion)).toEqual([
      'PRESERVE_PARENT emits an explicit row when the nested coding list is empty',
    ]);
    expect(cda.report.assertions.filter(assertion => assertion.status === 'untested').map(assertion => assertion.name)).toEqual(
      cda.report.gaps.map(gap => gap.assertion),
    );
    expect(cda.report.assertions.filter(assertion => assertion.status === 'failed')).toEqual([]);
    return;
  }
  expect(cda.report.status).toBe('passed');
};

const lifecycleVariants = [
  {
    caseName: 'nested-coding-expand-lifecycle',
    title: 'expand nested component coding values with exact source identity, cancel, apply, edit, reload, and restore',
  },
  {
    caseName: 'nested-coding-expand-lifecycle-single-per-component',
    title: 'expand nested component coding values with one coding per component and exact source identity, cancel, apply, edit, reload, and restore',
  },
];

test.describe('CDA nested authored EXPAND lifecycle', () => {
  for (const variant of lifecycleVariants) {
    test.describe(variant.caseName, () => {
      test.use({
        cdaScenarioID: 'cda-authored-nested-expand',
        cdaCaseName: variant.caseName,
        cdaUiRouting: 'explicit-query',
        cdaRequireSourceFixture: true,
      });

      test(variant.title, runNestedExpandLifecycle);
    });
  }
});
