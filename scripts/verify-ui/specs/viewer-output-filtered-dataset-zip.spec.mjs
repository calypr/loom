import { test } from '../helpers/fixtures.mjs';
import { viewerOutputFilteredDatasetZipWorkflow } from '../workflows/viewer-output-filtered-dataset-zip.mjs';

test.use({ scenarioID: 'viewer-query', caseName: 'dataset-zip-filtered-complete-population', fixtureDir: 'testdata/devloop-fixture' });

test('Viewer dataset ZIP exports the complete authorized population while a facet is selected', async ({ page, workflow, loomContext }, testInfo) => {
  await viewerOutputFilteredDatasetZipWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
    networkObserver: workflow.networkObserver,
  }, loomContext, testInfo);
});
