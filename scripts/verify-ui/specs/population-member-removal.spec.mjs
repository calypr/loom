import { test } from '../helpers/cda-fixtures.mjs';
import { populationMemberRemovalWorkflow } from '../workflows/population-member-removal-workflow.mjs';

test.describe('Builder population member removal', () => {
  test.use({
    cdaScenarioID: 'builder-population-member-removal',
    cdaCaseName: 'mapped-plus-orphan-to-empty',
  });

  test('removes the mapped source member and restores the full population after undo', async ({ page, cda }) => {
    await populationMemberRemovalWorkflow(page, cda.report, cda.action, cda.check, cda.fault, cda);
  });
});

test.describe('Builder population member removal with a nonempty GROUP', () => {
  test.use({
    cdaScenarioID: 'builder-population-member-removal',
    cdaCaseName: 'mapped-contributor-removal-preserves-group-counts',
  });

  test('removes one mapped contributor, preserves the lower nonempty GROUP, and restores the exact baseline', async ({ page, cda }) => {
    await populationMemberRemovalWorkflow(page, cda.report, cda.action, cda.check, cda.fault, cda);
  });
});
