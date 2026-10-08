import { test, expect } from '../helpers/cda-fixtures.mjs';
import { assertFilterBrowserDefaultMode, filterBrowserWorkflow } from '../workflows/verify-cda-filter-browser.mjs';
import { rootSettingsWorkflow } from '../workflows/verify-cda-root-settings.mjs';
import { filterLifecycleWorkflow } from '../workflows/verify-cda-filter-lifecycle.mjs';
import { rootRebaseWorkflow } from '../workflows/verify-cda-root-rebase.mjs';
import { legacyCollectionWorkflow } from '../workflows/verify-cda-legacy-collection.mjs';
import { addColumnDialogWorkflow } from '../workflows/verify-cda-add-column-dialog.mjs';
import { cdaPublicationWorkflow } from '../workflows/verify-cda-publication-browser.mjs';
import { relatedEligibilityWorkflow } from '../workflows/verify-cda-related-eligibility.mjs';
import { framingWorkflow } from '../workflows/verify-cda-framing.mjs';
import { identifierMultiplicityWorkflow } from '../workflows/verify-cda-identifier-multiplicity.mjs';
import { lastTableWorkflow } from '../workflows/verify-cda-last-table.mjs';
import { collectionRepairWorkflow } from '../workflows/verify-cda-collection-repair.mjs';
import { startingCollectionHandoffWorkflow } from '../workflows/verify-cda-starting-collection-handoff.mjs';
import { zeroColumnRelatedMedicationWorkflow } from '../workflows/verify-cda-zero-column-related-medication.mjs';
import { rowSourcesWorkflow } from '../workflows/verify-cda-row-sources-browser.mjs';
import { tableManagementWorkflow } from '../workflows/verify-cda-table-management-browser.mjs';
import { mixedSiblingCountWorkflow } from '../workflows/verify-cda-mixed-sibling-count-browser.mjs';
import { sourceAggregateMixedSiblingCountWorkflow } from '../workflows/verify-cda-sourceaggregate-mixed-sibling-count-browser.mjs';
import { expandedPublicationWorkflow } from '../workflows/verify-cda-expanded-publication-browser.mjs';
import { indirectSpecimenPatientWorkflow } from '../workflows/verify-cda-indirect-specimen-patient.mjs';
import { activeRelatedContributorAnyWorkflow } from '../workflows/verify-cda-active-related-contributor-any-browser.mjs';
import { namedCohortRelatedCountWorkflow } from '../workflows/verify-cda-named-cohort-related-count-browser.mjs';

test.describe('CDA Add columns dialog cancellation', () => {
  test.use({
    cdaExplorer: process.env.LOOM_QA_EXPLORER,
    cdaScenarioID: 'cda-add-column-dialog',
    cdaCaseName: 'suggestion-cancel-preserves-draft',
  });

  test('cancel each coded suggestion dialog without changing the draft', async ({ page, cda }) => {
    await addColumnDialogWorkflow({ page, cda, expect });
  });
});

test.describe('CDA publication', () => {
  test.use({
    cdaScenarioID: 'cda-publication',
    cdaCaseName: 'bounded-specimen-publication-viewer-reload',
    cdaRequireClickhouse: true,
  });

  test('publish a bounded Specimen output and verify its viewer rows', async ({ page, cda }) => {
    await cdaPublicationWorkflow({ page, cda });
  });
});

test.describe('CDA related eligibility', () => {
  test.use({
    cdaExplorer: process.env.LOOM_QA_EXPLORER,
    cdaScenarioID: 'cda-related-eligibility',
    cdaCaseName: 'exists-absent-count-and-removal',
  });

  test('change related-record eligibility and restore the source rows', async ({ page, cda }) => {
    await relatedEligibilityWorkflow({ page, cda, expect });
  });
});

test.describe('CDA framing', () => {
  test.use({
    cdaExplorer: process.env.LOOM_CDA_EXPLORER_ID,
    cdaScenarioID: 'cda-framing',
    cdaCaseName: 'related-specimen-code-values',
  });

  test('save and remove a related framed column', async ({ page, cda }) => {
    await framingWorkflow({ page, cda });
  });
});

test.describe('CDA identifier multiplicity', () => {
  test.use({
    ...(process.env.LOOM_CDA_EXPLORER_SEED ? { cdaExplorer: process.env.LOOM_CDA_EXPLORER_SEED } : {}),
    cdaScenarioID: 'cda-identifier-multiplicity',
    cdaCaseName: 'diagnosis-identifier-preserves-multiplicity',
  });

  test('preserve Condition diagnosis identifier multiplicity through add and remove', async ({ page, cda }) => {
    await identifierMultiplicityWorkflow({ page, cda, expect });
  });
});


test.describe('CDA filter lifecycle', () => {
  test.use({
    cdaScenarioID: 'cda-filter-browser',
    cdaCaseName: 'filter-lifecycle',
    cdaUiRouting: 'explicit-query',
  });

  test('create, edit, cancel, apply, and remove a typed CDA filter', async ({ page, cda }) => {
    assertFilterBrowserDefaultMode();
    await filterBrowserWorkflow({ page, cda });
  });
});

test.describe('CDA composite Group filter lifecycle', () => {
  test.use({
    cdaScenarioID: 'cda-filter-browser',
    cdaCaseName: 'composite-group-filter',
    cdaUiRouting: 'explicit-query',
  });

  test('filter two-key Observation Group output by subject reference through Cancel, Apply, edit, removal, and reload', async ({ page, cda }) => {
    assertFilterBrowserDefaultMode();
    const previousShape = process.env.LOOM_FILTER_GROUP_SHAPE;
    process.env.LOOM_FILTER_GROUP_SHAPE = 'COMPOSITE_ID_SUBJECT';
    try {
      await filterBrowserWorkflow({ page, cda });
    } finally {
      if (previousShape === undefined) delete process.env.LOOM_FILTER_GROUP_SHAPE;
      else process.env.LOOM_FILTER_GROUP_SHAPE = previousShape;
    }
  });
});


test.describe('CDA root row settings', () => {
  test.use({
    cdaScenarioID: 'cda-root-settings',
    cdaCaseName: 'change-root-and-restore-population-route',
  });

  test('preserve selected CDA membership while changing and restoring the row root', async ({ page, cda }) => {
    await rootSettingsWorkflow({ page, cda });
  });
});


test.describe('CDA filter lifecycle', () => {
  test.use({
    cdaScenarioID: 'cda-filter-lifecycle',
    cdaCaseName: 'create-edit-apply-and-remove-patient-filter',
  });

  test('create, edit, apply, and remove a CDA Patient filter with raw-source checks', async ({ page, cda }) => {
    await filterLifecycleWorkflow({ page, cda });
  });
});


test.describe('CDA root rebase lifecycle', () => {
  test.use({
    cdaScenarioID: 'cda-root-rebase',
    cdaCaseName: 'preserve-patient-values-through-observation-and-restore',
    cdaUiRouting: 'explicit-query',
  });

  test('rebase Patient rows through Observation and restore the original route and values', async ({ page, cda }) => {
    await rootRebaseWorkflow({ page, cda });
  });
});


test.describe('CDA legacy collection', () => {
  test.use({
    cdaScenarioID: 'cda-legacy-collection',
    cdaCaseName: 'read-legacy-parent-direction-without-rewriting-draft',
  });

  test('read legacy selection coverage and prove the saved draft remains unchanged', async ({ page, cda }) => {
    await legacyCollectionWorkflow({ page, cda });
  });
});

test.describe('CDA last-table deletion and Undo', () => {
  test.use({
    cdaScenarioID: 'cda-last-table',
    cdaCaseName: 'delete-last-table-undo-and-reload',
    cdaRequireSourceFixture: true,
  });

  test('delete the only CDA table, restore it with Undo, and verify reload state', async ({ page, cda }) => {
    await lastTableWorkflow({ page, cda });
  });
});

test.describe('CDA collection repair', () => {
  const partialLongRoute = process.env.LOOM_COLLECTION_PARTIAL_LONG_ROUTE === '1';
  const longRoute = process.env.LOOM_COLLECTION_LONG_ROUTE === '1' || partialLongRoute;
  test.use({
    cdaScenarioID: partialLongRoute ? 'cda-collection-repair-partial' : 'cda-collection-repair',
    cdaCaseName: partialLongRoute ? 'partial-long-route-repair-and-reload' : longRoute ? 'long-route-repair-and-reload' : 'unmapped-parent-repair-and-reload',
    cdaRequireSourceFixture: true,
    cdaUiRouting: 'explicit-query',
  });

  test('remove an unmapped selected resource, verify the saved route, and reload', async ({ page, cda }) => {
    await collectionRepairWorkflow({ page, cda });
  });
});

test.describe('CDA starting collection handoff', () => {
  test.use({
    cdaScenarioID: 'cda-starting-collection-handoff',
    cdaCaseName: 'initial-selection-handoff-route-preview-apply-reload',
    cdaRequireSourceFixture: true,
  });

  test('hand off a starting collection, attach its route, preview, and reload', async ({ page, cda }) => {
    await startingCollectionHandoffWorkflow({ page, cda });
  });
});

test.describe('CDA zero-column five-hop RelatedExpand', () => {
  test.use({
    cdaScenarioID: 'cda-five-hop-related-expansion',
    cdaCaseName: 'medication-preserve-parent',
    cdaRequireSourceFixture: true,
  });

  test('Make a row for each Medication from a zero-column Specimen through five relationship hops', async ({ page, cda }) => {
    test.setTimeout(300_000);
    await zeroColumnRelatedMedicationWorkflow({ page, cda });
  });
});

test.describe('CDA row source lineage', () => {
  const lineageMode = process.env.LOOM_LINEAGE_MODE ?? 'GROUP';
  const withFilter = process.env.LOOM_LINEAGE_FILTER === '1';
  test.use({
    cdaScenarioID: 'cda-row-sources',
    cdaCaseName: `${lineageMode.toLowerCase()}-${withFilter ? 'with-filter' : 'source-inspection'}`,
    cdaRequireSourceFixture: true,
  });

  test('inspect grouped, coded, or related row sources across save, edit, removal, and reload', async ({ page, cda }) => {
    await rowSourcesWorkflow({ page, cda });
  });
});

test.describe('CDA table management', () => {
  test.use({
    cdaScenarioID: 'cda-table-management',
    cdaCaseName: 'create-rename-duplicate-reorder-delete-undo-and-reload',
    cdaRequireSourceFixture: true,
  });

  test('create, rename, duplicate, reorder, delete, undo, and reload CDA tables', async ({ page, cda }) => {
    await tableManagementWorkflow({ page, cda });
  });
});

test.describe('CDA mixed sibling related COUNT', () => {
  test.use({
    cdaScenarioID: 'cda-mixed-sibling-count',
    cdaCaseName: 'condition-observation-specimen-count-lifecycle',
    cdaRequireSourceFixture: true,
  });

  test('cancel, save, reload, and remove three related COUNT siblings', async ({ page, cda }) => {
    const report = await mixedSiblingCountWorkflow({ page, cda });
    expect(report.status).toBe('passed');
  });
});

test.describe('CDA mixed sibling SourceAggregate COUNT', () => {
  test.use({
    cdaScenarioID: 'cda-sourceaggregate-mixed-sibling-count',
    cdaCaseName: 'condition-observation-specimen-sourceaggregate-lifecycle',
    cdaRequireSourceFixture: true,
  });

  test('save, reload, and remove three sibling SourceAggregate COUNT columns', async ({ page, cda }) => {
    const report = await sourceAggregateMixedSiblingCountWorkflow({ page, cda });
    expect(report.status).toBe('passed');
  });
});

test.describe('CDA expanded publication', () => {
  test.use({
    cdaScenarioID: 'cda-expanded-publication',
    cdaCaseName: 'component-row-expand-publish-clickhouse-viewer-reload',
    cdaRequireSourceFixture: true,
    cdaRequireClickhouse: true,
  });

  test('expand exact Observation component tuples, publish, and verify ClickHouse Viewer rows', async ({ page, cda }) => {
    const report = await expandedPublicationWorkflow({ page, cda });
    expect(report.status).toBe('passed');
  });
});

test.describe('CDA indirect Specimen to Patient values', () => {
  test.use({
    cdaScenarioID: 'cda-indirect-specimen-patient',
    cdaCaseName: 'two-hop-route-value-lifecycle',
    cdaRequireSourceFixture: true,
  });

  test('verify Patient.id values through the Specimen to Observation to Patient route', async ({ page, cda }) => {
    await indirectSpecimenPatientWorkflow({ page, cda, mode: 'values' });
  });
});

test.describe('CDA indirect Specimen to Patient count', () => {
  test.use({
    cdaScenarioID: 'cda-indirect-specimen-patient',
    cdaCaseName: 'two-hop-route-count-lifecycle',
    cdaRequireSourceFixture: true,
  });

  test('verify related Patient.id counts through the Specimen to Observation to Patient route', async ({ page, cda }) => {
    await indirectSpecimenPatientWorkflow({ page, cda, mode: 'count' });
  });
});

test.describe('CDA active-related Contributor ANY', () => {
  test.use({
    cdaScenarioID: 'cda-active-related-contributor-any',
    cdaCaseName: 'active-related-repeated-nested-code-any-lifecycle',
    cdaRequireSourceFixture: true,
  });

  test('author and restore an Observation Contributor ANY rule from the active Patient anchor', async ({ page, cda }) => {
    const report = await activeRelatedContributorAnyWorkflow({ page, cda });
    expect(report.status).toBe('passed');
  });
});

for (const { relatedForm, includeEmptyGroup, caseName } of [
  { relatedForm: 'COUNT', includeEmptyGroup: false, caseName: 'related-count-nonempty-groups' },
  { relatedForm: 'COUNT', includeEmptyGroup: true, caseName: 'related-count-including-empty-group' },
  { relatedForm: 'ALL', includeEmptyGroup: false, caseName: 'related-all-nonempty-groups' },
  { relatedForm: 'ALL', includeEmptyGroup: true, caseName: 'related-all-including-empty-group' },
]) {
  test.describe(`CDA named-cohort related ${relatedForm} ${includeEmptyGroup ? 'with' : 'without'} empty group`, () => {
    test.use({
      cdaScenarioID: 'cda-named-cohort-related-count',
      cdaCaseName: caseName,
      cdaRequireSourceFixture: true,
    });

    test(`author and restore the exact named-cohort ${relatedForm} contributor lifecycle`, async ({ page, cda }) => {
      const report = await namedCohortRelatedCountWorkflow({ page, cda, relatedForm, includeEmptyGroup });
      expect(report.status).toBe('passed');
    });
  });
}
