import { test } from '../helpers/fixtures.mjs';
import { legacyV9WorkspaceWorkflow } from '../workflows/legacy-v9-workspace.mjs';

test.describe('Legacy Builder workspace digest', () => {
  test.use({ scenarioID: 'builder-authoring', caseName: 'legacy-v9-rows', fixtureDir: 'testdata/devloop-fixture' });

  test('legacy semantics v9 preserves the saved Patient digest through native row actions and reload', async ({ page, workflow, loomContext }) => {
    await legacyV9WorkspaceWorkflow({ ...workflow, page }, loomContext);
  });
});
