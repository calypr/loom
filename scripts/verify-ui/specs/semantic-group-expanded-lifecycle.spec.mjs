import { expect, test } from '../helpers/cda-fixtures.mjs';
import { semanticGroupExpandedLifecycle } from '../workflows/semantic-group-expanded-lifecycle.mjs';

test.describe('CDA expanded Observation Coding Group', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'semantic-group-expanded',
    cdaRequireSourceFixture: true,
  });

  test('selects, previews, cancels, applies, edits, adds contributors, removes, restores, and reloads exact expanded Coding groups', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await semanticGroupExpandedLifecycle({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
