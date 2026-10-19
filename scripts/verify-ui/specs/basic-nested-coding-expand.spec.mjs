import { test } from '../helpers/fixtures.mjs';
import { basicNestedCodingExpandWorkflow } from '../workflows/basic-nested-coding-expand.mjs';

test.describe('Basic nested-coding EXPAND', () => {
  test.use({
    scenarioID: 'basic-nested-coding-expand',
    caseName: 'nested-coding',
    fixtureDir: 'testdata/basic-nested-coding-expand',
  });

  test('Basic fixture expands nested coding with owner, multiplicity, Cancel, Apply, edit, removal and reload', async ({ page, workflow, loomContext }) => {
    test.setTimeout(300_000);
    await basicNestedCodingExpandWorkflow({ ...workflow, page }, loomContext);
  });
});
