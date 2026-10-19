import { test } from '../helpers/fixtures.mjs';
import { createPositiveMedicationCdaContext } from '../helpers/positive-medication-cda-context.mjs';
import { zeroColumnRelatedMedicationWorkflow } from '../workflows/verify-cda-zero-column-related-medication.mjs';

test.describe('positive fixture zero-column five-hop RelatedExpand', () => {
  test.use({
    scenarioID: 'cda-five-hop-related-expansion',
    caseName: 'medication-positive-fixture',
    fixtureDir: 'testdata/cda-zero-column-related-medication-positive',
    fixtureGeneration: 'cda-fhir-v1',
  });

  test('preserve and exclude exact positive Medication matches through native edit and reload', async ({ page, workflow }, testInfo) => {
    test.setTimeout(300_000);
    const cda = createPositiveMedicationCdaContext({
      page,
      workflow,
      testInfo,
      playwrightTest: test,
    });
    await zeroColumnRelatedMedicationWorkflow({ page, cda });
  });
});
