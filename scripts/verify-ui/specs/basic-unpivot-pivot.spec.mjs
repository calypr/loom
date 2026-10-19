import { test } from '../helpers/fixtures.mjs';
import { basicUnpivotPivotWorkflow } from '../workflows/basic-unpivot-pivot.mjs';

test.describe('Basic Unpivot then Pivot', () => {
  test.use({
    scenarioID: 'basic-unpivot-pivot-roundtrip',
    caseName: 'unpivot-to-pivot',
    fixtureDir: 'testdata/basic-unpivot-pivot',
  });

  test('Basic fixture preserves exact Patient rows through native Unpivot→Pivot, edit, reload, and restoration', async ({ page, workflow }) => {
    await basicUnpivotPivotWorkflow({ ...workflow, page });
  });
});
