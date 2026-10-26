import { test } from '../helpers/fixtures.mjs';
import { builderExplorerDeletionWorkflow } from '../workflows/builder-explorer-deletion.mjs';

test.describe('Builder Explorer deletion', () => {
  test.use({
    scenarioID: 'builder-controls',
    caseName: 'explorer-deletion',
    fixtureDir: 'testdata/devloop-fixture',
  });

  test('Explorer deletion removes the disposable Explorer, preserves the exact fallback URL, and blocks invalid deletes',
    async ({ page, workflow, loomContext }) => {
      await builderExplorerDeletionWorkflow({ ...workflow, page }, loomContext);
    });
});
