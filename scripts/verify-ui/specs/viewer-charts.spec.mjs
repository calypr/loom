import { test } from '../helpers/fixtures.mjs';
import { viewerChartsWorkflow } from '../workflows/viewer-charts.mjs';

test.use({ scenarioID: 'viewer-query', caseName: 'charts', fixtureDir: 'testdata/devloop-fixture' });

test('Viewer charts expose the exact Gender category count and missing count before and after reload', async ({ page, workflow, loomContext }) => {
  await viewerChartsWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
  }, loomContext);
});
