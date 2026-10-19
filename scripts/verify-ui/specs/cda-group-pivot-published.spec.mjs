import { test, expect } from '../helpers/cda-fixtures.mjs';
import { cdaGroupPivotPublishedWorkflow } from '../workflows/cda-group-pivot-published-workflow.mjs';

test.describe('CDA Group→Pivot publication and Viewer reload', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'group-pivot-published',
    cdaRequireSourceFixture: true,
    cdaRequireClickhouse: true,
  });

  test('native real-CDA Group→Pivot publishes and reloads exact Viewer rows', async ({ page, cda }) => {
    test.setTimeout(300_000);
    try {
      await cdaGroupPivotPublishedWorkflow({ page, cda });
    } catch (error) {
      if (error?.name !== 'RawOracleUnavailableError' || error.rawOracleFailure !== true) throw error;
      test.skip(true, error.message);
    }
    await expect(page).toHaveURL(/mode=viewer/);
  });
});
