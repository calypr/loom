import { test } from '../helpers/fixtures.mjs';
import { basicPivotUnpivotWorkflow } from '../workflows/basic-pivot-unpivot.mjs';

test.use({
  scenarioID: 'basic-pivot-unpivot',
  caseName: 'lifecycle',
  fixtureDir: 'testdata/basic-pivot-unpivot',
});

test('Basic fixture preserves exact row identities through native Pivot→Unpivot lifecycle', async ({ page, workflow }) => {
  await basicPivotUnpivotWorkflow({ ...workflow, page });
});
