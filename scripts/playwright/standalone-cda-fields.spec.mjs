import { test, expect } from './cda-fixtures.mjs';
import { codedFieldLifecycleWorkflow } from '../verify-ui/coded-field-lifecycle-workflow.mjs';
import { cohortFieldsWorkflow } from '../verify-ui/cohort-fields-workflow.mjs';
import { compoundFieldsWorkflow } from '../verify-ui/compound-fields-workflow.mjs';
import { contributorCodeWorkflow } from '../verify-ui/contributor-code-workflow.mjs';
import { contributorExistsWorkflow } from '../verify-ui/contributor-exists-workflow.mjs';
import { contributorRulesWorkflow } from '../verify-ui/contributor-rules-workflow.mjs';
import { sourceFieldsWorkflow } from '../verify-ui/source-fields-workflow.mjs';
import { repeatedContributorAnyWorkflow } from '../verify-ui/repeated-contributor-any-workflow.mjs';

test.describe('CDA coded field lifecycle', () => {
  test.use({ cdaScenarioID: 'cda-native', cdaCaseName: 'coded-field-lifecycle', cdaRequireSourceFixture: true });
  test('CDA coded field lifecycle', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await codedFieldLifecycleWorkflow({ page, cda, caseOptions: { familyCode: process.env.LOOM_CDA_CODED_CONCEPT_CODE, familyTitle: process.env.LOOM_CDA_CODED_CONCEPT_TITLE } });
    await expect(page).toHaveURL(/mode=builder/);
  });
});

test.describe('CDA cohort fields', () => {
  test.use({ cdaScenarioID: 'cda-native', cdaCaseName: 'cohort-fields', cdaRequireSourceFixture: true });
  test('CDA cohort fields', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await cohortFieldsWorkflow({ page, cda, caseOptions: { memberField: process.env.LOOM_COHORT_FIELD ?? 'resourceType', authoredFilter: process.env.LOOM_COHORT_COMPOSITION === '1', filterOneMember: process.env.LOOM_COHORT_FILTER_ONE === '1', editCohortPolicy: process.env.LOOM_COHORT_POLICY_EDIT === '1', postCohortFilter: process.env.LOOM_COHORT_POST_FILTER === '1', collectionRoundTrip: process.env.LOOM_COHORT_COLLECTION_ROUND_TRIP === '1', removeCohortAnchor: process.env.LOOM_COHORT_REMOVE_ANCHOR === '1', authoredExpand: process.env.LOOM_COHORT_AUTHORED_EXPAND === '1' } });
    await expect(page).toHaveURL(/mode=builder/);
  });
});

test.describe('CDA compound fields', () => {
  test.use({ cdaScenarioID: 'cda-native', cdaCaseName: 'compound-fields', cdaRequireSourceFixture: true });
  test('CDA compound fields', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await compoundFieldsWorkflow({ page, cda, caseOptions: {  } });
    await expect(page).toHaveURL(/mode=builder/);
  });
});

test.describe('CDA contributor code', () => {
  test.use({ cdaScenarioID: 'cda-native', cdaCaseName: 'contributor-code', cdaRequireSourceFixture: true });
  test('CDA contributor code', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await contributorCodeWorkflow({ page, cda, caseOptions: { quantityWitnessReportPath: process.env.LOOM_CDA_QUANTITY_WITNESS_REPORT } });
    await expect(page).toHaveURL(/mode=builder/);
  });
});

test.describe('CDA contributor exists', () => {
  test.use({ cdaScenarioID: 'cda-native', cdaCaseName: 'contributor-exists', cdaRequireSourceFixture: true });
  test('CDA contributor exists', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await contributorExistsWorkflow({ page, cda, caseOptions: {  } });
    await expect(page).toHaveURL(/mode=builder/);
  });
});

test.describe('CDA contributor rules', () => {
  test.use({ cdaScenarioID: 'cda-native', cdaCaseName: 'contributor-rules', cdaRequireSourceFixture: true });
  test('CDA contributor rules', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await contributorRulesWorkflow({ page, cda, caseOptions: {  } });
    await expect(page).toHaveURL(/mode=builder/);
  });
});

test.describe('CDA source fields', () => {
  test.use({ cdaScenarioID: 'cda-native', cdaCaseName: 'source-fields', cdaRequireSourceFixture: true });
  test('CDA source fields', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await sourceFieldsWorkflow({ page, cda, caseOptions: {  } });
    await expect(page).toHaveURL(/mode=builder/);
  });
});

test.describe('CDA repeated contributor ANY', () => {
  test.use({ cdaScenarioID: 'cda-native', cdaCaseName: 'repeated-contributor-any', cdaRequireSourceFixture: true });
  test('CDA repeated contributor ANY', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await repeatedContributorAnyWorkflow({ page, cda, caseOptions: {  } });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
