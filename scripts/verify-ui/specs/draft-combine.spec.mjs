import { test } from '../helpers/fixtures.mjs';
import {
  draftAppendWorkflow,
  draftJoinWorkflow,
  draftMembershipWorkflow,
  groupPivotJoinWorkflow,
} from '../workflows/builder-combine-draft.mjs';

test.describe('Builder Combine Draft Join', () => {
  test.use({ scenarioID: 'builder-combine-draft', caseName: 'join', fixtureDir: 'testdata/verify-combine' });

  test('grouped current-draft Join supports cancel, apply, edit, removal, and reload', async ({ page, workflow, loomContext }) => {
    await draftJoinWorkflow({
      page, report: workflow.report, action: workflow.action, check: workflow.check, fault: workflow.fault,
    }, loomContext);
  });
});

test.describe('Builder Combine Draft Append', () => {
  test.use({ scenarioID: 'builder-combine-draft', caseName: 'append', fixtureDir: 'testdata/verify-combine' });

  test('three-source current-draft APPEND supports cancel, apply, edit, removal, and reload', async ({ page, workflow, loomContext }) => {
    await draftAppendWorkflow({
      page, report: workflow.report, action: workflow.action, check: workflow.check, fault: workflow.fault,
    }, loomContext);
  });
});

test.describe('Builder Combine Draft Group Pivot Join', () => {
  test.use({ scenarioID: 'builder-combine-draft', caseName: 'group-pivot', fixtureDir: 'testdata/verify-combine' });

  test('GROUP-to-PIVOT current-draft Join supports cancel, apply, edit, removal, and reload', async ({ page, workflow, loomContext }) => {
    await groupPivotJoinWorkflow({
      page, report: workflow.report, action: workflow.action, check: workflow.check, fault: workflow.fault,
    }, loomContext);
  });
});

test.describe('Builder Combine Draft Membership', () => {
  test.use({ scenarioID: 'builder-combine-draft', caseName: 'membership', fixtureDir: 'testdata/verify-combine' });

  test('current-draft MEMBERSHIP supports INCLUDE, EXCLUDE edit, removal, restoration, and reload', async ({ page, workflow, loomContext }) => {
    await draftMembershipWorkflow({
      page, report: workflow.report, action: workflow.action, check: workflow.check, fault: workflow.fault,
    }, loomContext);
  });
});
