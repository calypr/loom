import { test } from '../helpers/fixtures.mjs';
import { conditionSpecimenRouteAmbiguityWorkflow } from '../workflows/condition-specimen-route-ambiguity.mjs';

test.describe('Condition-to-Specimen root-choice ambiguity', () => {
  test.use({
    scenarioID: 'builder-authoring',
    caseName: 'condition-specimen-route-ambiguity',
    fixtureDir: 'testdata/basic-root-picker-ambiguity',
  });

  test('native Record type picker resolves Condition-to-Specimen alternatives with the authored Patient anchor',
    async ({ page, workflow, loomContext }) => {
      test.setTimeout(180_000);
      await conditionSpecimenRouteAmbiguityWorkflow({ ...workflow, page }, loomContext);
    });
});
