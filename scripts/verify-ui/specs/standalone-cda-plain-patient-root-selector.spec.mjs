import { test } from '../helpers/cda-fixtures.mjs';
import { plainPatientRootSelectorWorkflow } from '../workflows/verify-cda-plain-patient-root-selector.mjs';

test.describe('CDA plain Patient starting-record selector', () => {
  test.use({
    cdaScenarioID: 'cda-plain-patient-root-selector',
    cdaCaseName: 'select-observation-directly-and-restore-patient',
    cdaUiRouting: 'explicit-query',
  });

  test('select Observation directly from a plain Patient table and restore Patient', async ({ page, cda }) => {
    await plainPatientRootSelectorWorkflow({ page, cda });
  });
});
