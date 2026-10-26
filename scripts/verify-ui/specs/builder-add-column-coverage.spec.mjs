import { test } from '../helpers/fixtures.mjs';
import { builderAddColumnCoverageWorkflow } from '../workflows/builder-add-column-coverage.mjs';

test.describe('Builder Add-column value coverage', () => {
  test.use({
    scenarioID: 'builder-add-column-coverage',
    caseName: 'basic-field-coverage',
    fixtureDir: 'testdata/basic-add-column-coverage',
  });

  test('native direct-field coverage preview cancels unchanged, applies exact rows, and survives reload', async ({ page, workflow, loomContext }) => {
    test.setTimeout(180_000);
    await builderAddColumnCoverageWorkflow({ ...workflow, page }, loomContext);
  });
});
