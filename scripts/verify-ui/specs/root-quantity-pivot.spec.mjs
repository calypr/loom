import { randomUUID } from 'node:crypto';
import { test as devTest } from '../helpers/fixtures.mjs';
import { test as cdaTest } from '../helpers/cda-fixtures.mjs';
import { rootQuantityPivotWorkflow } from '../workflows/root-quantity-pivot-workflow.mjs';
import { runQuantityPivotNativeDragBrowserWorkflow } from '../workflows/verify-cda-quantity-pivot-native-drag-browser.mjs';

devTest.describe('Root quantity Pivot fixture lifecycle', () => {
  devTest.use({
    scenarioID: 'root-quantity-pivot',
    caseName: 'fixture-lifecycle',
    fixtureDir: 'testdata/root-quantity-pivot-fixture',
  });

  devTest('discovers typed categories and completes the SUM, edit, removal, and reload lifecycle', async ({ page, workflow }) => {
    await rootQuantityPivotWorkflow(page, workflow.report, workflow.action, workflow.check, workflow.fault, {
      ...workflow,
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

cdaTest.describe('Related text-only quantity Pivot full population lifecycle', () => {
  cdaTest.use({ cdaScenarioID: 'root-quantity-pivot', cdaCaseName: 'related-text-only-full-population-lifecycle',
    cdaExplorer: `qa-related-text-pivot-${randomUUID()}`, cdaUiRouting: 'explicit-query' });

  cdaTest('related quantity Pivot groups by text and applies SUM/MAX across the full route', async ({ page, cda }) => {
    await runQuantityPivotNativeDragBrowserWorkflow({ page, cda }, { fullPopulation: true, textOnly: true });
  });
});
