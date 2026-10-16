import { test } from '../helpers/fixtures.mjs';
import { relatedExpandFieldLifecycleWorkflow } from '../workflows/related-expand-field-lifecycle.mjs';

test.describe('Basic RelatedExpand active-record field lifecycle', () => {
  test.use({
    scenarioID: 'basic-related-expand-field-lifecycle',
    caseName: 'observation-root-medication-administration',
    fixtureDir: 'testdata/basic-related-expand-field-lifecycle',
    fixtureGeneration: 'cda-fhir-v1',
  });

  test('Basic fixture preserves related source and active-record fields through Group lifecycle', async ({ page, workflow }) => {
    await relatedExpandFieldLifecycleWorkflow({ ...workflow, page });
  });
});
