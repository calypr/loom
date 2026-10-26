import { test } from '../helpers/fixtures.mjs';
import { viewerPaginationWorkflow } from '../workflows/viewer-pagination.mjs';

test.use({ scenarioID: 'viewer-query', caseName: 'pagination', fixtureDir: 'testdata/viewer-pagination' });

test('Viewer paginates exact fixture Patient identities and resets page state', async ({ page, workflow, loomContext }) => {
  await viewerPaginationWorkflow({
    page, report: workflow.report, action: workflow.action, check: workflow.check,
  }, loomContext);
});
