import { test } from '../helpers/fixtures.mjs';
import { savedRelatedExpandRouteEditWorkflow } from '../workflows/saved-related-expand-route-edit.mjs';

test.describe('Builder saved RelatedExpand route editing', () => {
  test.use({
    scenarioID: 'builder-saved-route-edit',
    caseName: 'saved-related-expand-route-edit',
    fixtureDir: 'testdata/devloop-fixture',
  });

  test('Saved RelatedExpand edits Cancel, Apply, restore, and reload exact route rows', async ({ page, workflow, loomContext }) => {
    test.setTimeout(300_000);
    await savedRelatedExpandRouteEditWorkflow({ ...workflow, page }, loomContext);
  });
});
