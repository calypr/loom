import { test, expect } from '../helpers/cda-fixtures.mjs';
import { CdaUpstreamAppendWitnessUnavailable } from '../helpers/cda-current-draft-upstream-append-oracle.mjs';
import {
  cdaCurrentDraftPatientMembershipHandoffWorkflow,
  cdaCurrentDraftUpstreamAppendWorkflow,
} from '../workflows/cda-current-draft-upstream-append-workflow.mjs';

test.describe('CDA current-draft Group→DERIVE→APPEND', () => {
  test.use({
    cdaScenarioID: 'cda-current-draft-upstream-append',
    cdaCaseName: 'upstream-append',
    cdaRequireSourceFixture: true,
  });

  test('three exact CDA populations Group, edit Patient DERIVE, then Append with full restoration', async ({ page, cda }) => {
    test.setTimeout(300_000);
    try {
      await cdaCurrentDraftUpstreamAppendWorkflow({ page, cda });
    } catch (error) {
      if (!(error instanceof CdaUpstreamAppendWitnessUnavailable)) throw error;
      test.skip(true, error.message);
    }
    await expect(page).toHaveURL(/mode=builder/);
  });
});

test.describe('CDA current-draft Patient starting-collection membership handoff', () => {
  test.use({
    cdaScenarioID: 'cda-current-draft-upstream-append',
    cdaCaseName: 'patient-membership-handoff',
    cdaRequireSourceFixture: true,
  });

  test('native Patient attachment narrows and restores Group→DERIVE→APPEND rows', async ({ page, cda }) => {
    test.setTimeout(300_000);
    try {
      await cdaCurrentDraftPatientMembershipHandoffWorkflow({ page, cda });
    } catch (error) {
      if (!(error instanceof CdaUpstreamAppendWitnessUnavailable)) throw error;
      test.skip(true, error.message);
    }
    await expect(page).toHaveURL(/mode=builder/);
  });
});
