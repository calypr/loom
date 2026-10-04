import { test } from './fixtures.mjs';
import { appendWorkflow } from '../verify-ui/builder-combine.mjs';

test.use({ scenarioID: 'builder-combine', caseName: 'append' });

test('APPEND preview, apply, edit, cancel, removal and reload', async ({ page, workflow, loomContext }) => {
  await appendWorkflow({ page, report: workflow.report, action: workflow.action }, loomContext);
});
