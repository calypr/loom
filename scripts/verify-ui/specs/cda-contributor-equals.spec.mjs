import { test, expect } from '../helpers/cda-fixtures.mjs';
import { contributorRulesWorkflow } from '../workflows/contributor-rules-workflow.mjs';

test.describe('CDA contributor EQUALS', () => {
  test.use({
    cdaScenarioID: 'cda-contributor-equals',
    cdaCaseName: 'contributor-equals',
    cdaRequireSourceFixture: true,
  });

  test('CDA contributor EQUALS', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await contributorRulesWorkflow({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
