import { test, expect } from '../helpers/cda-fixtures.mjs';
import { semanticSpecimenFieldsWorkflow } from '../workflows/semantic-specimen-fields-workflow.mjs';

test.describe('CDA semantic Specimen fields', () => {
  test.use({
    cdaScenarioID: 'cda-semantic-specimen-fields',
    cdaCaseName: 'specimen-semantic-fields',
    cdaRequireSourceFixture: true,
  });

  test('CDA root Specimen semantic field lifecycle', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await semanticSpecimenFieldsWorkflow({
      page,
      cda,
      caseOptions: {
        witnessReportPath: process.env.LOOM_CDA_SPECIMEN_SEMANTIC_WITNESS_REPORT,
      },
    });
    await expect(page).toHaveURL(/mode=builder/);
  });
});
