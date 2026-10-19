import { test } from '../helpers/fixtures.mjs';
import { viewerOutputDatasetZipWorkflow } from '../workflows/viewer-output-dataset-zip.mjs';

test.use({ scenarioID: 'viewer-query', caseName: 'dataset-zip', fixtureDir: 'testdata/devloop-fixture' });

test('Viewer downloads the published Patient dataset as an exact identity-bound ZIP', async ({ page, workflow, loomContext }, testInfo) => {
  await viewerOutputDatasetZipWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
    networkObserver: workflow.networkObserver,
  }, loomContext, testInfo);
});
