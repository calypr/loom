import { test, expect } from '../helpers/cda-fixtures.mjs';
import { CdaDiagnosticReportAppendWitnessUnavailable } from '../helpers/cda-current-draft-diagnostic-report-append-oracle.mjs';
import { cdaCurrentDraftDiagnosticReportAppendWorkflow } from '../workflows/cda-current-draft-diagnostic-report-append-workflow.mjs';

test.describe('CDA DiagnosticReport current-draft APPEND', () => {
  test.use({
    cdaScenarioID: 'cda-current-draft-diagnostic-report-append',
    cdaCaseName: 'diagnostic-report-append',
    cdaRequireSourceFixture: true,
  });

  test('DiagnosticReport current-draft APPEND previews exact duplicate status counts and restores the rooted target', async ({ page, cda }) => {
    test.setTimeout(300_000);
    try {
      await cdaCurrentDraftDiagnosticReportAppendWorkflow({ page, cda });
    } catch (error) {
      if (!(error instanceof CdaDiagnosticReportAppendWitnessUnavailable)) throw error;
      test.skip(true, error.message);
    }
    await expect(page).toHaveURL(/mode=builder/);
  });
});
