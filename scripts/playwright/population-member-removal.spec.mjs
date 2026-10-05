import { test } from './cda-fixtures.mjs';
import { populationMemberRemovalWorkflow } from '../verify-ui/population-member-removal-workflow.mjs';

test.describe('Builder population member removal', () => {
  test.use({
    cdaScenarioID: 'builder-population-member-removal',
    cdaCaseName: 'mapped-plus-orphan-to-empty',
  });

  test('removes the mapped source member and restores the full population after undo', async ({ page, cda }) => {
    await populationMemberRemovalWorkflow(page, cda.report, cda.action, cda.check, cda.fault, cda);
  });
});
