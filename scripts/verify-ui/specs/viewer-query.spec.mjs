import { test } from '../helpers/fixtures.mjs';
import { viewerQueryWorkflow } from '../workflows/viewer-query.mjs';

test.use({ scenarioID: 'viewer-query', caseName: 'output', fixtureDir: 'testdata/devloop-fixture' });

test('Viewer query reports an owned read failure and recovers through Retry', async ({ page, workflow, loomContext }, testInfo) => {
  await viewerQueryWorkflow({
    page, report: workflow.report, action: workflow.action, check: workflow.check, fault: workflow.fault,
  }, loomContext, testInfo);
});
