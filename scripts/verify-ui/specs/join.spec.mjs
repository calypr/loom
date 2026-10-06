import { test } from '../helpers/fixtures.mjs';
import { joinWorkflow } from '../workflows/builder-combine.mjs';

test.use({ scenarioID: 'builder-combine', caseName: 'join', fixtureDir: 'testdata/verify-combine' });

test('KEY_JOIN INNER/LEFT preview, apply, cancel, removal and reload', async ({ page, workflow, loomContext }) => {
  await joinWorkflow({ page, report: workflow.report, action: workflow.action }, loomContext);
});
