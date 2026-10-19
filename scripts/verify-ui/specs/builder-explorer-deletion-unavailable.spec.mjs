import { test } from '../helpers/fixtures.mjs';
import { builderExplorerDeletionUnavailableWorkflow } from '../workflows/builder-explorer-deletion-unavailable.mjs';

test.describe('Builder Explorer deletion unavailable', () => {
  test.use({
    scenarioID: 'builder-controls',
    caseName: 'explorer-deletion-unavailable',
    fixtureDir: 'testdata/devloop-fixture',
  });

  test('Explorer deletion stays hidden and preserves the exact Explorer list and URL across reload',
    async ({ page, workflow, loomContext }) => {
      await builderExplorerDeletionUnavailableWorkflow({ ...workflow, page }, loomContext);
    });
});
