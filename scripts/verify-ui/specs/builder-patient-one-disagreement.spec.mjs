import { test } from '../helpers/fixtures.mjs';
import { patientOneDisagreementWorkflow } from '../workflows/builder-patient-one-disagreement.mjs';

test.describe('Builder Patient ID ONE disagreement', () => {
  test.use({ scenarioID: 'builder-authoring', caseName: 'patient-one-disagreement', fixtureDir: 'testdata/devloop-fixture' });

  test('raw Patient.id ONE refusal leaves the saved Group intact for same-editor ALL repair', async ({ page, workflow, loomContext }) => {
    await patientOneDisagreementWorkflow({ ...workflow, page }, loomContext);
  });
});
