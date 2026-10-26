import { test } from '../helpers/fixtures.mjs';
import { viewerCellExplainWorkflow } from '../workflows/viewer-cell-explain.mjs';

test.use({ scenarioID: 'viewer-query', caseName: 'cell-explain', fixtureDir: 'testdata/devloop-fixture' });

test('Viewer explains the exact Patient Gender cell through source details, close, reopen, and reload', async ({ page, workflow, loomContext }) => {
  await viewerCellExplainWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
    networkObserver: workflow.networkObserver,
  }, loomContext);
});
