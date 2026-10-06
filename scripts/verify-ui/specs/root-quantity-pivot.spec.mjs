import { test as devTest } from '../helpers/fixtures.mjs';
import { test as cdaTest } from '../helpers/cda-fixtures.mjs';
import { rootQuantityPivotWorkflow } from '../workflows/root-quantity-pivot-workflow.mjs';

devTest.describe('Root quantity Pivot fixture lifecycle', () => {
  devTest.use({
    scenarioID: 'root-quantity-pivot',
    caseName: 'fixture-lifecycle',
    fixtureDir: 'testdata/root-quantity-pivot-fixture',
  });

  devTest('discovers typed categories and completes the SUM, edit, removal, and reload lifecycle', async ({ page, workflow, loomContext }) => {
    await rootQuantityPivotWorkflow(page, workflow.report, workflow.action, workflow.check, workflow.fault, {
      ...loomContext,
      caseName: 'fixture-lifecycle',
    });
  });
});

cdaTest.describe('Root quantity Pivot full population discovery', () => {
  cdaTest.use({ cdaScenarioID: 'root-quantity-pivot', cdaCaseName: 'full-population-discovery' });

  cdaTest('matches the complete typed CDA category domain to the independent Arango oracle', async ({ page, cda }) => {
    await rootQuantityPivotWorkflow(page, cda.report, cda.action, cda.check, cda.fault, cda);
  });
});

cdaTest.describe('Root quantity Pivot full population lifecycle', () => {
  cdaTest.use({ cdaScenarioID: 'root-quantity-pivot', cdaCaseName: 'full-population-lifecycle' });

  cdaTest('matches SUM and MAX output and restores the full CDA source after reload', async ({ page, cda }) => {
    await rootQuantityPivotWorkflow(page, cda.report, cda.action, cda.check, cda.fault, cda);
  });
});
