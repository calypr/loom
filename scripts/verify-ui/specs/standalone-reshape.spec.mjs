import { randomUUID, randomInt } from 'node:crypto';
import { test } from '../helpers/cda-fixtures.mjs';
import { runCodedPivotWorkflow } from '../workflows/verify-cda-coded-pivot.mjs';
import { runImplicitPivotWorkflow } from '../workflows/verify-cda-implicit-pivot.mjs';
import { runPivotCategoryCycleBrowserWorkflow } from '../workflows/verify-cda-pivot-category-cycle-browser.mjs';
import { runPivotCategoryEditBrowserWorkflow } from '../workflows/verify-cda-pivot-category-edit-browser.mjs';
import { runPivotFieldChangeBrowserWorkflow } from '../workflows/verify-cda-pivot-field-change-browser.mjs';
import { runPivotReloadBrowserWorkflow } from '../workflows/verify-cda-pivot-reload-browser.mjs';
import { runQuantityPivotBrowserWorkflow } from '../workflows/verify-cda-quantity-pivot-browser.mjs';
import { runQuantityPivotNativeDragBrowserWorkflow } from '../workflows/verify-cda-quantity-pivot-native-drag-browser.mjs';
import { runRelatedFieldAfterUnpivotBrowserWorkflow } from '../workflows/verify-cda-related-field-after-unpivot-browser.mjs';
import { runRelatedPivotBrowserWorkflow } from '../workflows/verify-cda-related-pivot-browser.mjs';
import { runRelatedUnpivotBrowserWorkflow } from '../workflows/verify-cda-related-unpivot-browser.mjs';
import { runUnpivotWorkflow } from '../workflows/verify-cda-unpivot.mjs';
import { runGroupAddFieldsBrowserWorkflow } from '../workflows/verify-cda-group-add-fields-browser.mjs';
import { runGroupOneConflictBrowserWorkflow } from '../workflows/verify-cda-group-one-conflict-browser.mjs';
import { runGroupRelatedValuesBrowserWorkflow } from '../workflows/verify-cda-group-related-values-browser.mjs';
import { runRelatedGroupBrowserWorkflow } from '../workflows/verify-cda-related-group-browser.mjs';
import { runGroupRelatedSummaryBrowserWorkflow } from '../workflows/verify-cda-group-related-summary-browser.mjs';
import { runGroupEditBeforeRelatedColumnBrowserWorkflow } from '../workflows/verify-cda-group-edit-before-related-column-browser.mjs';
import { isPostPivotRawOracleUnavailable, runRelatedSourceAfterPivotBrowserWorkflow } from '../workflows/verify-cda-related-source-after-pivot-browser.mjs';

test.describe('standalone CDA reshape workflows', () => {

  const register = (name, workflow, args = {}, options = {}) => {
    const createsExplorer = !name.startsWith('implicit-pivot-') && !['pivot-reload', 'unpivot'].includes(name);
    const explorer = name === 'pivot-category-edit'
      ? `pivot-category-edit-browser-${Date.now()}${randomInt(100000, 1000000)}`
      : `qa-reshape-${name}-${randomUUID()}`;
    test.describe(name, () => {
      test.use({
        cdaScenarioID: `standalone-reshape-${name}`,
        cdaCaseName: name,
        ...(createsExplorer ? { cdaExplorer: explorer } : {}),
        ...options,
      });
      test(name, async ({ page, cda }) => {
        try {
          await workflow({ page, cda }, args);
        } catch (error) {
          if (name === 'related-source-after-pivot' && isPostPivotRawOracleUnavailable(error)) {
            test.skip(true, error.message);
          }
          throw error;
        }
      });
    });
  };

  for (const mode of ['integer', 'string']) {
    register(`coded-pivot-${mode}`, runCodedPivotWorkflow, { mode });
  }

  for (const mode of ['root', 'related', 'related-source-key']) {
    register(`implicit-pivot-${mode}`, runImplicitPivotWorkflow, { mode });
  }

  register('group-add-fields', runGroupAddFieldsBrowserWorkflow);
  register('group-one-conflict', runGroupOneConflictBrowserWorkflow);
  register('group-related-values', runGroupRelatedValuesBrowserWorkflow);
  register('related-group', runRelatedGroupBrowserWorkflow);
  register('group-related-summary', runGroupRelatedSummaryBrowserWorkflow);
  register('group-edit-before-related-column', runGroupEditBeforeRelatedColumnBrowserWorkflow);

  register('pivot-category-cycle', runPivotCategoryCycleBrowserWorkflow);
  register('pivot-related-apply-only', runPivotCategoryCycleBrowserWorkflow, { relatedApplyOnly: true });
  register('pivot-category-edit', runPivotCategoryEditBrowserWorkflow);
  register('pivot-field-change', runPivotFieldChangeBrowserWorkflow);
  register('pivot-reload', runPivotReloadBrowserWorkflow, {
    seedPath: process.env.LOOM_PIVOT_RELOAD_SEED,
  });

  register('quantity-pivot', runQuantityPivotBrowserWorkflow);
  register('quantity-pivot-full-population', runQuantityPivotBrowserWorkflow, { fullPopulation: true });
  register('quantity-pivot-native-drag', runQuantityPivotNativeDragBrowserWorkflow);
  register('quantity-pivot-native-drag-full-population', runQuantityPivotNativeDragBrowserWorkflow, { fullPopulation: true });

  for (const caseName of ['gender-all', 'gender-null-all', 'resource-type-all', 'id-count']) {
    register(`related-field-after-unpivot-${caseName}`, runRelatedFieldAfterUnpivotBrowserWorkflow, { caseName });
  }

  register('related-pivot', runRelatedPivotBrowserWorkflow);
  register('related-source-after-pivot', runRelatedSourceAfterPivotBrowserWorkflow, {}, { cdaScenarioID: 'standalone-reshape-related-source-after-pivot', cdaExplorer: `qa-post-pivot-${randomUUID()}` });
  register('related-unpivot', runRelatedUnpivotBrowserWorkflow, {}, { cdaScenarioID: 'standalone-reshape-related-unpivot' });
  register('unpivot', runUnpivotWorkflow);
});
