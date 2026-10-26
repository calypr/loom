import { test } from '../helpers/fixtures.mjs';
import { firstTableWorkflow, recompileWorkflow, tablesWorkflow } from '../workflows/builder-controls.mjs';

test.describe('Builder Controls recompile', () => {
  test.use({ scenarioID: 'builder-controls', caseName: 'recompile', fixtureDir: 'testdata/devloop-fixture' });

  test('Recompile recovers from automatic compilation failure', async ({ page, workflow, loomContext }) => {
    await recompileWorkflow({
      page, report: workflow.report, action: workflow.action, check: workflow.check, fault: workflow.fault,
    }, loomContext);
  });
});

test.describe('Builder Controls first table', () => {
  test.use({ scenarioID: 'builder-controls', caseName: 'first-table', fixtureDir: 'testdata/devloop-fixture' });

  test('Add columns waits for a current-draft preview', async ({ page, workflow, loomContext }) => {
    await firstTableWorkflow({
      page, report: workflow.report, action: workflow.action, check: workflow.check,
    }, loomContext);
  });
});

test.describe('Builder Controls tables', () => {
  test.use({ scenarioID: 'builder-controls', caseName: 'tables', fixtureDir: 'testdata/devloop-fixture' });

  test('Duplicate, rename, select, delete, copy, and reload tables', async ({ page, workflow, loomContext }) => {
    await tablesWorkflow({
      page, report: workflow.report, action: workflow.action, check: workflow.check,
    }, loomContext);
  });
});
