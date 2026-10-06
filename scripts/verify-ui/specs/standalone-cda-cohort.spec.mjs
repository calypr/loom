import { test, expect } from '../helpers/cda-fixtures.mjs';
import { authoredExpandWorkflow } from '../workflows/verify-cda-authored-expand-browser.mjs';
import { cohortMembershipRevisionWorkflow } from '../workflows/verify-cda-cohort-membership-revision-browser.mjs';
import { cohortRowSourcesWorkflow } from '../workflows/verify-cda-cohort-row-sources-browser.mjs';
import { composedRowLineageWorkflow } from '../workflows/verify-cda-composed-row-lineage-browser.mjs';

const fixtureOptions = (caseName, scenarioID = 'standalone-cda-cohort') => ({
  cdaScenarioID: scenarioID,
  cdaCaseName: caseName,
  cdaRequireSourceFixture: true,
});

test.describe('CDA authored EXPAND lifecycle', () => {
  test.use(fixtureOptions('authored-expand-lifecycle'));

  test('preserve RECORDS rows while applying, editing, reloading, and removing EXPAND', async ({ page, cda }) => {
    const result = await authoredExpandWorkflow({ page, cda, expect });
    if (result?.skipReason) test.skip(true, result.skipReason);
    expect(cda.report.status).toBe('passed');
  });
});

for (const changeSourceCollection of [false, true]) {
  const caseName = changeSourceCollection ? 'membership-revision-source-scope-change' : 'membership-revision-source-scope-preserved';
  test.describe(`CDA cohort membership revision: ${caseName}`, () => {
    test.use(fixtureOptions(caseName));

    test('revise immutable membership and restore the source scope through the native Builder', async ({ page, cda }) => {
      await cohortMembershipRevisionWorkflow({ page, cda, changeSourceCollection, expect });
      expect(cda.report.status).toBe('passed');
    });
  });
}

for (const cohortRowValueCase of ['default', 'transformed-category']) {
  test.describe(`CDA cohort row sources: ${cohortRowValueCase}`, () => {
    test.use(fixtureOptions(`cohort-row-sources-${cohortRowValueCase}`));

    test('verify scoped row membership, source lineage, member values, and saved policy edits', async ({ page, cda }) => {
      await cohortRowSourcesWorkflow({ page, cda, cohortRowValueCase, expect });
      expect(cda.report.status).toBe('passed');
    });
  });
}

for (const lineageMode of [
  'COMPOSED_RELATED',
  'DIRECT_PIVOT',
  'DIRECT_PIVOT_SHARED_CONTRIBUTOR',
  'DIRECT_GROUP_COUNT_PIVOT',
]) {
  const caseName = `composed-row-lineage-${lineageMode.toLowerCase()}`;
  test.describe(`CDA row lineage: ${lineageMode}`, () => {
    test.use(fixtureOptions(caseName));

    test('compare native row identities and contributors with the independent scoped source oracle', async ({ page, cda }) => {
      await composedRowLineageWorkflow({ page, cda, lineageMode, expect });
      expect(cda.report.status).toBe('passed');
    });
  });
}

test.describe('CDA upstream Related edit and cascade', () => {
  test.use(fixtureOptions('upstream-edit-cascade', 'cda-upstream-related-edit-cascade'));

  test('edit a saved upstream Related expansion while preserving dependent rows, then cascade-remove it', async ({ page, cda }) => {
    await composedRowLineageWorkflow({ page, cda, lineageMode: 'COMPOSED_RELATED' });
    expect(cda.report.status).toBe('passed');
  });
});
