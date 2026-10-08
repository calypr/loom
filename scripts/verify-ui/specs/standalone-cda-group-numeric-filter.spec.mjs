import { test } from '../helpers/cda-fixtures.mjs';
import { cdaGroupNumericFilterWorkflow } from '../workflows/verify-cda-group-numeric-filter-browser.mjs';

test.describe('CDA numeric COUNT_ROWS Filter after Group', () => {
  test.use({
    cdaScenarioID: 'cda-group-numeric-filter',
    cdaCaseName: 'numeric-filter-after-group',
    cdaRequireSourceFixture: true,
    cdaUiRouting: 'explicit-query',
  });

  test('filter numeric Group summaries through Preview, Cancel, Apply, edit, removal, and reload', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await cdaGroupNumericFilterWorkflow({ page, cda });
  });
});
