import { test } from '../helpers/fixtures.mjs';
import { basicRelatedFieldAfterUnpivotWorkflow } from '../workflows/basic-related-field-after-unpivot.mjs';

test.describe('Basic nullable related field after Unpivot', () => {
  test.use({
    scenarioID: 'basic-related-field-after-unpivot',
    caseName: 'gender-missing-all',
    fixtureDir: 'testdata/basic-related-field-after-unpivot-nullable',
  });

  test('Basic fixture preserves missing Patient.gender as an ALL null member through Unpivot lifecycle', async ({ page, workflow }) => {
    await basicRelatedFieldAfterUnpivotWorkflow({ ...workflow, page, genderMode: 'missing' });
  });
});
