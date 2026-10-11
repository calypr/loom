import { expect, test } from '../helpers/cda-fixtures.mjs';
import { cdaSavedRelatedRouteEditEpoch126Workflow } from '../workflows/cda-saved-related-route-edit-epoch126.mjs';

test.describe('Saved real-CDA RelatedExpand route editing', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'saved-cda-related-route-edit-epoch126',
    cdaUiRouting: 'explicit-query',
    cdaRequireSourceFixture: true,
  });

  test('Saved real-CDA routes edit A-to-B, Cancel unchanged, Apply, restore B-to-A, and reload exact rows', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await cdaSavedRelatedRouteEditEpoch126Workflow({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
