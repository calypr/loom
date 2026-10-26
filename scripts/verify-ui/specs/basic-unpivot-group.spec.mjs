import { test } from '../helpers/fixtures.mjs';
import { basicUnpivotGroupWorkflow } from '../workflows/basic-unpivot-group.mjs';

test.describe('Basic Unpivot then Group', () => {
  test.use({
    scenarioID: 'basic-unpivot-group',
    caseName: 'unpivot-to-group',
    fixtureDir: 'testdata/basic-unpivot-pivot',
  });

  test('Basic fixture groups exact native Unpivot keys and counts', async ({ page, workflow }) => {
    await basicUnpivotGroupWorkflow({ ...workflow, page });
  });
});
