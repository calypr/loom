import { test } from '../helpers/fixtures.mjs';
import { appendWorkflow } from '../workflows/builder-combine.mjs';

test.use({ scenarioID: 'builder-combine', caseName: 'append', fixtureDir: 'testdata/verify-combine' });

test('APPEND preview, apply, edit, cancel, removal and reload', async ({ page, workflow, loomContext }) => {
  await appendWorkflow({
    page,
    report: workflow.report,
    action: workflow.action,
    nativeRequestLedger: workflow.nativeRequestLedger,
  }, loomContext);
});
