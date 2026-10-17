import { expect, test } from '../helpers/cda-fixtures.mjs';
import { semanticGroupLifecycle } from '../workflows/semantic-group-lifecycle.mjs';

test.describe('CDA semantic category Group', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'semantic-group',
    cdaRequireSourceFixture: true,
  });

  test('selects, previews, cancels, applies, edits, removes, restores, and reloads exact semantic groups', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await semanticGroupLifecycle({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
