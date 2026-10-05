import { test, expect } from './cda-fixtures.mjs';
import { filterBrowserWorkflow } from '../verify-cda-filter-browser.mjs';
import { rootSettingsWorkflow } from '../verify-cda-root-settings.mjs';
import { filterLifecycleWorkflow } from '../verify-cda-filter-lifecycle.mjs';
import { rootRebaseWorkflow } from '../verify-cda-root-rebase.mjs';
import { legacyCollectionWorkflow } from '../verify-cda-legacy-collection.mjs';
import { addColumnDialogWorkflow } from '../verify-cda-add-column-dialog.mjs';
import { cdaPublicationWorkflow } from '../verify-cda-publication-browser.mjs';
import { relatedEligibilityWorkflow } from '../verify-cda-related-eligibility.mjs';
import { framingWorkflow } from '../verify-cda-framing.mjs';
import { identifierMultiplicityWorkflow } from '../verify-cda-identifier-multiplicity.mjs';

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
    cdaExplorer: process.env.LOOM_CDA_EXPLORER_SEED,
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
    cdaCaseName: `operator-${process.env.LOOM_SAVED_FILTER_OPERATOR ?? 'default'}-value-${process.env.LOOM_FILTER_VALUE_TYPE ?? 'string'}`,
  });

  test('create, edit, cancel, apply, and remove a typed CDA filter', async ({ page, cda }) => {
    await filterBrowserWorkflow({ page, cda });
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
