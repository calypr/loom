import { test } from '../helpers/fixtures.mjs';
import { basicTemporalFilterWorkflow } from '../workflows/basic-temporal-filter.mjs';

test.describe('Basic native DATE and DATE_TIME filter lifecycle', () => {
  for (const caseName of ['date', 'date-time']) {
    test.describe(caseName, () => {
      test.use({
        scenarioID: 'basic-temporal-filter',
        caseName,
        fixtureDir: 'testdata/basic-temporal-filter',
      });

      test(`${caseName} filter keeps exact typed values and source rows through Cancel, Apply, edit, removal, and reload`, async ({ page, workflow, loomContext }) => {
        test.setTimeout(180_000);
        await basicTemporalFilterWorkflow({ ...workflow, page, caseName }, loomContext);
      });
    });
  }
});
