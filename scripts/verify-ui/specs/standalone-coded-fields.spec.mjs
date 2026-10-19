import { test } from '../helpers/fixtures.mjs';
import { standaloneCodedFieldsWorkflow } from '../workflows/standalone-coded-fields.mjs';

const caseName = 'coded-fields';
const title = 'Basic fixture adds standalone coded fields with exact Coding version and display correlation';

test.describe('Basic standalone coded fields', () => {
  test.use({
    scenarioID: 'basic-standalone-coded-fields',
    caseName,
    fixtureDir: 'testdata/basic-standalone-coded-fields',
  });

  test(title, async ({ page, workflow, loomContext }) => {
    test.setTimeout(300_000);
    await standaloneCodedFieldsWorkflow({ ...workflow, page }, loomContext);
  });
});
