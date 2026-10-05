import { test } from './fixtures.mjs';
import { builderAuthoringWorkflow } from '../lib/playwright-authoring.mjs';
import { builderAuthoringSuggestionsWorkflow } from '../lib/playwright-authoring-suggestions.mjs';
import { cohortRecodeWorkflow } from '../verify-ui/builder-authoring.mjs';
import { cohortExpandWorkflow } from '../verify-ui/builder-cohort-expand.mjs';
import { repeatedEmptyWorkflow } from '../verify-ui/builder-repeated.mjs';
import { groupEntryWorkflow } from '../verify-ui/builder-group-entry.mjs';

test.describe('Builder authoring', () => {
  test.use({ scenarioID: 'builder-authoring', fixtureDir: 'testdata/devloop-fixture' });

  const defineCase = (caseName, title, runWorkflow, fixtureDir = 'testdata/devloop-fixture') => {
    test.describe(caseName, () => {
      test.use({ caseName, fixtureDir });
      test(title, async ({ page, workflow, loomContext }) => {
        await runWorkflow({ ...workflow, page }, loomContext);
      });
    });
  };

  defineCase('suggestions', 'catalog-backed suggestions expose actionable Patient candidates',
    builderAuthoringSuggestionsWorkflow);
  defineCase('authoring', 'Builder authoring previews, publishes, and reloads configured fields',
    builderAuthoringWorkflow);
  defineCase('cohort-recode', 'named cohort recoding preserves raw ALL bindings across edit and reload',
    cohortRecodeWorkflow);
  defineCase('cohort-expand', 'named cohort expansion preserves exact Patient IDs through its lifecycle',
    cohortExpandWorkflow);
  defineCase('repeated-empty', 'repeated empty and missing values preserve source row identities',
    repeatedEmptyWorkflow, 'testdata/verify-repeated-empty');
  defineCase('group-entry', 'standard Group entry proposes an empty-key COUNT_ROWS preview',
    groupEntryWorkflow);
});
