import { test, expect } from '../helpers/cda-fixtures.mjs';
import { cdaCurrentDraftGroupJoinWorkflow } from '../workflows/cda-current-draft-group-join-workflow.mjs';

test.describe('CDA current-draft Group and Join', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'group-join',
    cdaRequireSourceFixture: true,
  });

  test('two independent native Observation Groups join through the current draft lifecycle', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await cdaCurrentDraftGroupJoinWorkflow({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
