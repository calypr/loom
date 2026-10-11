import { test, expect } from '../helpers/cda-fixtures.mjs';
import { verifyCdaCompoundCodedGroup } from '../workflows/verify-cda-compound-coded-group.mjs';

test.describe('Real CDA compound coded GROUP: Observation.code to valueCodeableConcept display', () => {
  test.use({
    cdaScenarioID: 'cda-workspace-combine',
    cdaCaseName: 'compound-coded-group-native',
    cdaRequireClickhouse: false,
    cdaRequireSourceFixture: true,
    cdaUiRouting: 'explicit-query',
  });

  test('previews, cancels, applies, edits, removes, restores, and reloads the exact three-row witness', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await verifyCdaCompoundCodedGroup({ page, cda });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
