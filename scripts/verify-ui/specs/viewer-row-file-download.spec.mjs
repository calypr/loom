import { test } from '../helpers/fixtures.mjs';
import { viewerRowFileDownloadWorkflow } from '../workflows/viewer-row-file-download.mjs';

test.use({
  scenarioID: 'viewer-query',
  caseName: 'per-row-file-download',
  fixtureDir: 'testdata/viewer-row-file-download',
});

test('Viewer downloads the exact source DocumentReference from a per-row FileCell and repeats after reload', async ({ page, workflow, loomContext }, testInfo) => {
  await viewerRowFileDownloadWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
    networkObserver: workflow.networkObserver,
  }, loomContext, testInfo);
});
