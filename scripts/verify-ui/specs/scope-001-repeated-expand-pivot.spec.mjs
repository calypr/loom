import { test } from '../helpers/fixtures.mjs';
import { scope001RepeatedExpandPivotWorkflow } from '../workflows/scope-001-repeated-expand-pivot.mjs';

test.describe('SCOPE-001 populated repeated component EXPAND to Pivot', () => {
  test.use({
    scenarioID: 'builder-repeated',
    caseName: 'repeated-expand-pivot',
    fixtureDir: 'testdata/scope-001-repeated-pivot',
  });

  test('native component expansion and distinct Pivot survive preview, edit, removal, and reload',
    async ({ page, workflow, loomContext }) => {
      test.setTimeout(180_000);
      await scope001RepeatedExpandPivotWorkflow({ ...workflow, page }, loomContext);
    });
});
