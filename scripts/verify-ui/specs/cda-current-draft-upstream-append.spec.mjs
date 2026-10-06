import { test, expect } from '../helpers/cda-fixtures.mjs';
import { CdaUpstreamAppendWitnessUnavailable } from '../helpers/cda-current-draft-upstream-append-oracle.mjs';
import { cdaCurrentDraftUpstreamAppendWorkflow } from '../workflows/cda-current-draft-upstream-append-workflow.mjs';

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
