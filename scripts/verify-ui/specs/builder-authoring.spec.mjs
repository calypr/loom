import { test } from '../helpers/fixtures.mjs';
import { builderAuthoringWorkflow } from '../helpers/playwright-authoring.mjs';
import { builderAuthoringSuggestionsWorkflow } from '../helpers/playwright-authoring-suggestions.mjs';
import { cohortRecodeWorkflow } from '../workflows/builder-authoring.mjs';
import { cohortExpandWorkflow } from '../workflows/builder-cohort-expand.mjs';
import { repeatedEmptyWorkflow } from '../workflows/builder-repeated.mjs';
import { groupEntryWorkflow } from '../workflows/builder-group-entry.mjs';
import { populationCoverageWorkflow } from '../workflows/population-coverage.mjs';
import { builderExplorerSelectionWorkflow } from '../workflows/builder-explorer-selection.mjs';
import { builderDataframeContractWorkflow } from '../workflows/builder-dataframe-contract.mjs';
import { builderTableReorderWorkflow } from '../workflows/builder-table-reorder.mjs';

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
  defineCase('authoring', 'Builder authoring previews, changes an existing table root, and reloads',
    builderAuthoringWorkflow, 'testdata/builder-coded-source-column');
  defineCase('cohort-recode', 'named cohort recoding preserves raw ALL bindings across edit and reload',
    cohortRecodeWorkflow);
  defineCase('cohort-expand', 'named cohort expansion preserves exact Patient IDs through its lifecycle',
    cohortExpandWorkflow);
  defineCase('repeated-empty', 'repeated empty and missing values preserve source row identities',
    repeatedEmptyWorkflow, 'testdata/verify-repeated-empty');
  defineCase('group-entry', 'standard Group entry proposes an empty-key COUNT_ROWS preview',
    groupEntryWorkflow);
  defineCase('selected-resource-coverage',
    'selected Patient coverage preserves exact membership through detach, reattach, and reload',
    populationCoverageWorkflow);
  defineCase('explorer-selection',
    'Explorer switching shows distinct tables and restores the selected Explorer after reload',
    builderExplorerSelectionWorkflow);
  defineCase('dataframe-contract-panel',
    'Dataframe contract panel shows the exact Patient output and lineage',
    builderDataframeContractWorkflow);
  defineCase('table-reorder', 'native table reorder preserves exact saved order and rows after reload',
    builderTableReorderWorkflow, 'testdata/builder-coded-source-column');
});
