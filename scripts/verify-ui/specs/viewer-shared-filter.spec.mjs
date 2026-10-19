import { test } from '../helpers/fixtures.mjs';
import { viewerSharedFilterWorkflow } from '../workflows/viewer-shared-filter.mjs';

test.use({
  scenarioID: 'viewer-query',
  caseName: 'shared-filter-linked-outputs',
  fixtureDir: 'testdata/devloop-fixture',
});

test('Viewer shared filters apply to linked outputs, cancel cleanly, and reset on reload', async ({ page, workflow, loomContext }) => {
  await viewerSharedFilterWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
    networkObserver: workflow.networkObserver,
  }, loomContext);
});
