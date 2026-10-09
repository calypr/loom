import { test } from '../helpers/fixtures.mjs';
import { builderCodedSourceColumnWorkflow } from '../workflows/builder-coded-source-column.mjs';

test.describe('Builder coded source column', () => {
  test.use({ scenarioID: 'builder-coded-source-column', caseName: 'coded-source-column',
    fixtureDir: 'testdata/devloop-fixture' });

  test('native coded source column survives edit, reload, removal, and reload', async ({ page, workflow, loomContext }) => {
    test.setTimeout(180_000);
    await builderCodedSourceColumnWorkflow({ ...workflow, page }, loomContext);
  });
});
