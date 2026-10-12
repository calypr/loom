import { test } from '../helpers/fixtures.mjs';
import { verifySemanticConstructionChoiceRoute } from '../workflows/verify-semantic-construction-choice-route.mjs';

const caseName = 'semantic-construction-choice-route-fallback';

test.describe('ConceptCatalog routed semantic binding', () => {
  test.use({
    scenarioID: 'standalone-misc',
    caseName,
    fixtureDir: 'testdata/semantic-construction-choice-route',
    fixtureGeneration: 'semantic-choice-v1',
  });

  test('previews, applies, reloads, and removes the exact optional routed semantic value', async ({ page, workflow, loomContext }) => {
    test.setTimeout(300_000);
    await verifySemanticConstructionChoiceRoute({ page, workflow, context: loomContext });
  });
});
