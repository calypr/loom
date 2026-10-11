import { test, expect } from '../helpers/cda-fixtures.mjs';
import { cdaPublishedMembershipWorkflow } from '../workflows/cda-published-membership-workflow.mjs';

test.describe('CDA published-source GROUP to GROUP Membership', () => {
  test.use({
    cdaScenarioID: 'cda-published-source-membership',
    cdaCaseName: 'published-membership',
    cdaUiRouting: 'explicit-query',
    cdaRequireClickhouse: true,
    cdaRequireSourceFixture: true,
  });

  test('native published-source INCLUDE and EXCLUDE Membership pin exact grouped Observation IDs through reload', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await cdaPublishedMembershipWorkflow({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
