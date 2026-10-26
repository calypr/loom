import { test } from '../helpers/fixtures.mjs';
import { basicCodedPivotWorkflow } from '../workflows/basic-coded-pivot.mjs';

test.describe('Basic coded Pivot lifecycle', () => {
  test.use({
    scenarioID: 'basic-coded-pivot',
    caseName: 'integer-lifecycle',
    fixtureDir: 'testdata/basic-coded-pivot',
  });

  test('selects, previews, cancels, applies, edits, removes, and reloads exact coded rows', async ({ page, workflow, loomContext }) => {
    test.setTimeout(180_000);
    await basicCodedPivotWorkflow({ ...workflow, page }, loomContext);
  });
});
