import { test } from '../helpers/fixtures.mjs';
import { nullableJoinWorkflow } from '../workflows/builder-combine-nullable.mjs';

test.use({ scenarioID: 'builder-combine-nullable', caseName: 'lifecycle', fixtureDir: 'testdata/verify-combine-nullable-duplicates' });

test('nullable KEY_JOIN duplicate-key multiplicity and NULL non-equality lifecycle', async ({ page, workflow, loomContext }) => {
  await nullableJoinWorkflow({ page, report: workflow.report, action: workflow.action }, loomContext);
});
