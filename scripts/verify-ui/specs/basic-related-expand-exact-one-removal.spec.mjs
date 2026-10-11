import { test } from '../helpers/fixtures.mjs';
import { savedRelatedExpandRouteEditWorkflow } from '../workflows/saved-related-expand-route-edit.mjs';

test.describe('Basic exact-one RelatedExpand removal and recreation', () => {
  test.use({
    scenarioID: 'builder-saved-route-edit',
    caseName: 'relatedexpand-exact-one-removal',
    fixtureDir: 'testdata/devloop-fixture',
  });

  test('removing and recreating RelatedExpand preserves exact one-match Patient rows', async ({ page, workflow, loomContext }) => {
    test.setTimeout(300_000);
    await savedRelatedExpandRouteEditWorkflow(
      { ...workflow, page },
      loomContext,
      { includeRemovalLifecycle: true },
    );
  });
});
