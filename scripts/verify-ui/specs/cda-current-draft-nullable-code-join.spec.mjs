import { test, expect } from '../helpers/cda-fixtures.mjs';
import { cdaCurrentDraftNullableCodeJoinWorkflow } from '../workflows/cda-current-draft-nullable-code-join-workflow.mjs';

test.describe('CDA current-draft nullable valueQuantity.code Join', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'nullable-code-join',
    cdaUiRouting: 'explicit-query',
    cdaRequireSourceFixture: true,
  });

  test('raw Observation selections preserve duplicate-key and missing-code Join semantics', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await cdaCurrentDraftNullableCodeJoinWorkflow({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
