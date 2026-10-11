import { test } from '../helpers/fixtures.mjs';
import { basicRelatedFieldAfterUnpivotWorkflow } from '../workflows/basic-related-field-after-unpivot.mjs';

test.describe('Basic related field after Unpivot', () => {
  test.use({
    scenarioID: 'basic-related-field-after-unpivot',
    caseName: 'gender-all',
    fixtureDir: 'testdata/basic-related-field-after-unpivot-gender',
  });

  test('Basic fixture preserves exact related Patient gender through Unpivot lifecycle', async ({ page, workflow }) => {
    await basicRelatedFieldAfterUnpivotWorkflow({ ...workflow, page });
  });
});
