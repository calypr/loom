import { test } from '../helpers/cda-fixtures.mjs';
import { cdaSameTableRootReplacementWorkflow } from '../workflows/cda-same-table-root-replacement.mjs';

test.describe('CDA same-table root replacement', () => {
  test.use({
    cdaScenarioID: 'cda-same-table-root-replacement',
    cdaCaseName: 'same-table-root-replacement',
    cdaUiRouting: 'explicit-query',
    cdaRequireSourceFixture: true,
  });

  test('replace the Patient setup with the exact default Observation page on the same table', async ({ page, cda }) => {
    await cdaSameTableRootReplacementWorkflow({ page, cda });
  });
});
