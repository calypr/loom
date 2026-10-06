import { test, expect } from '../helpers/cda-fixtures.mjs';
import { cdaCurrentDraftMembershipWorkflow } from '../workflows/cda-current-draft-membership-workflow.mjs';

test.describe('CDA current-draft GROUP to GROUP Membership', () => {
  test.use({
    cdaScenarioID: 'cda-current-draft-membership',
    cdaCaseName: 'membership',
    cdaRequireSourceFixture: true,
  });

  test('native INCLUDE and EXCLUDE Membership use two exact grouped Observation ID populations', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await cdaCurrentDraftMembershipWorkflow({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
