import { test } from '../helpers/fixtures.mjs';
import { viewerOutputActionCSVWorkflow } from '../workflows/viewer-output-action.mjs';

test.use({ scenarioID: 'viewer-query', caseName: 'configured-output-action-csv', fixtureDir: 'testdata/devloop-fixture' });

test('Viewer downloads the configured output-action CSV with the exact Patient fixture rows', async ({ page, workflow, loomContext }, testInfo) => {
  await viewerOutputActionCSVWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
    networkObserver: workflow.networkObserver,
  }, loomContext, testInfo);
});
