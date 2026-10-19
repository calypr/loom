import { test } from '../helpers/fixtures.mjs';
import { publishedMembershipWorkflow } from '../workflows/builder-combine-draft.mjs';

test.describe('Builder Published Source Membership', () => {
  test.use({ scenarioID: 'builder-published-membership', caseName: 'membership', fixtureDir: 'testdata/verify-combine' });

  test('published MEMBERSHIP uses exact Basic source revisions through INCLUDE, EXCLUDE edit, removal, and reload', async ({ page, workflow, loomContext }) => {
    await publishedMembershipWorkflow({
      page, report: workflow.report, action: workflow.action, check: workflow.check, fault: workflow.fault,
      networkObserver: workflow.networkObserver,
    }, loomContext);
  });
});
