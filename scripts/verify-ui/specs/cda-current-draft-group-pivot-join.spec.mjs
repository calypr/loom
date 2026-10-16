import { test, expect } from '../helpers/cda-fixtures.mjs';
import { isCdaGroupPivotJoinOracleUnavailable } from '../workflows/cda-current-draft-group-pivot-join-workflow.mjs';
import { cdaCurrentDraftGroupPivotJoinWorkflow } from '../workflows/cda-current-draft-group-pivot-join-workflow.mjs';

test.describe('CDA current-draft Group→Pivot→Join', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'group-pivot-join',
    cdaRequireSourceFixture: true,
  });

  test('exact Observation Groups feed a native Pivot and editable current-draft Join', async ({ page, cda }) => {
    test.setTimeout(300_000);
    try {
      await cdaCurrentDraftGroupPivotJoinWorkflow({ page, cda });
    } catch (error) {
      if (!isCdaGroupPivotJoinOracleUnavailable(error)) throw error;
      test.skip(true, error.message);
    }
    await expect(page).toHaveURL(/mode=builder/);
  });
});
