import { test } from '../helpers/fixtures.mjs';
import { viewerRowDetailsWorkflow } from '../workflows/viewer-row-details.mjs';

test.use({ scenarioID: 'viewer-query', caseName: 'row-details', fixtureDir: 'testdata/devloop-fixture' });

test('Viewer row details show the exact Patient ID through close, reopen, reload, and reopen', async ({ page, workflow, loomContext }) => {
  await viewerRowDetailsWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
  }, loomContext);
});
