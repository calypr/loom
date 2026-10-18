import { expect, test } from '../helpers/cda-fixtures.mjs';
import { semanticGroupConnectedLifecycle } from '../workflows/semantic-group-connected-lifecycle.mjs';

test.describe('CDA connected Specimen Coding Group', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'semantic-group-connected',
    cdaRequireSourceFixture: true,
  });

  test('groups current Patient rows by exact inbound Specimen Coding and restores related owners', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await semanticGroupConnectedLifecycle({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
