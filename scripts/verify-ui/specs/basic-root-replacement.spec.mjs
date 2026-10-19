import { test } from '../helpers/fixtures.mjs';
import { basicRootReplacementWorkflow } from '../workflows/basic-root-replacement.mjs';

test.describe('Basic explicit same-table row-root replacement', () => {
  test.use({
    scenarioID: 'builder-root-replacement',
    caseName: 'replace-current-setup',
    fixtureDir: 'testdata/basic-root-replacement',
  });

  test('Explicit row-root replacement previews a reset, cancels safely, applies, and survives reload',
    async ({ page, workflow, loomContext }) => {
      test.setTimeout(180_000);
      await basicRootReplacementWorkflow({ ...workflow, page }, loomContext);
    });
});
