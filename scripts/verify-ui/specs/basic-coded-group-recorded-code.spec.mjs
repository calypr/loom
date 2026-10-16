import { test } from '../helpers/fixtures.mjs';
import { basicCodedGroupRecordedCodeWorkflow } from '../workflows/basic-coded-group-recorded-code.mjs';

const caseName = 'coded-group-recorded-code-basic';
const title = 'Basic fixture groups literal Observation codes with exact counts through Preview, Cancel, Apply, edit, removal and reload';

test.describe('Basic native coded-group recorded-code lifecycle', () => {
  test.use({
    scenarioID: 'standalone-misc',
    caseName,
    fixtureDir: 'testdata/basic-coded-group-recorded-code',
  });

  test(title, async ({ page, workflow, loomContext }) => {
    await basicCodedGroupRecordedCodeWorkflow(workflow, loomContext);
  });
});
