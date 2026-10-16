import { test } from '../helpers/fixtures.mjs';
import { basicRuntimeScopeIsolationWorkflow } from '../workflows/basic-runtime-scope-isolation.mjs';

test.use({
  scenarioID: 'viewer-query',
  caseName: 'runtime-scope-isolation',
  fixtureDir: 'testdata/devloop-fixture',
  fixtureGeneration: 'scope-001-primary',
});

test('Basic Viewer output excludes rows from another project and generation', async ({ page, workflow, loomContext }) => {
  await basicRuntimeScopeIsolationWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    check: workflow.check,
  }, loomContext);
});
