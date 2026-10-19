import { test, expect } from '../helpers/cda-fixtures.mjs';
import { CdaPublishedAppendWitnessUnavailable } from '../helpers/cda-published-upstream-append-oracle.mjs';
import { cdaPublishedUpstreamAppendWorkflow } from '../workflows/cda-published-upstream-append-workflow.mjs';

test.describe('published real-CDA APPEND', () => {
  test.use({
    cdaScenarioID: 'cda-published-upstream-append',
    cdaCaseName: 'published-append',
    cdaRequireClickhouse: true,
    cdaRequireSourceFixture: true,
  });

  test('publish exact raw CDA sources, Append with null padding, and restore the rooted target', async ({ page, cda }) => {
    test.setTimeout(300_000);
    try {
      await cdaPublishedUpstreamAppendWorkflow({ page, cda });
    } catch (error) {
      if (!(error instanceof CdaPublishedAppendWitnessUnavailable)) throw error;
      test.skip(true, error.message);
    }
    await expect(page).toHaveURL(/mode=builder/);
  });
});
