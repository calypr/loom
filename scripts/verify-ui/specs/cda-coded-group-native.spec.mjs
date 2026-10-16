import { expect, test } from '../helpers/cda-fixtures.mjs';
import { cdaCodedGroupNativeWorkflow } from '../workflows/cda-coded-group-native-workflow.mjs';

test.describe('CDA direct Observation code grouping', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'coded-group-native',
    cdaRequireSourceFixture: true,
  });

  test('selects, previews, cancels, applies, edits, removes, restores, and reloads exact code groups', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await cdaCodedGroupNativeWorkflow({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
