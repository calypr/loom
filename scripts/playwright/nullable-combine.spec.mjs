import { test } from './fixtures.mjs';
import { nullableJoinWorkflow } from '../verify-ui/builder-combine-nullable.mjs';

test.use({ scenarioID: 'builder-combine-nullable', caseName: 'lifecycle', fixtureDir: 'testdata/verify-combine' });

test('nullable KEY_JOIN INNER and LEFT lifecycle preserves ordinary NULL equality', async ({ page, workflow, loomContext }) => {
  await nullableJoinWorkflow({ page, report: workflow.report, action: workflow.action }, loomContext);
});
