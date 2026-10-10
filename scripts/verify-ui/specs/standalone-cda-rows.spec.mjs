import { expect, test } from '../helpers/cda-fixtures.mjs';
import { nestedRepeatedWorkflow } from '../workflows/verify-cda-nested-repeated-browser.mjs';
import { relatedOneAllWorkflow } from '../workflows/verify-cda-related-one-all-browser.mjs';
import { repeatedEmptyWorkflow } from '../workflows/verify-cda-repeated-empty-browser.mjs';
import { repeatedRowsWorkflow } from '../workflows/verify-cda-repeated-rows-browser.mjs';
import { missingComponentGroupSkipReason } from '../helpers/missing-component-group-oracle.mjs';
import { fixtureUnavailableSkipReason } from '../helpers/cda-fixture-outcomes.mjs';
import {
  statusOneAllLifecycleCheckNames,
  zeroObservationLifecycleCheckNames,
} from '../helpers/related-one-all-lifecycle-contract.mjs';

const expectLifecycleChecks = (assertions, names) => {
  const byName = new Map(assertions.map(assertion => [assertion.name, assertion]));
  for (const name of names) {
    expect(byName.get(name), name).toMatchObject({ status: 'passed' });
  }
};

const relatedMode = process.env.LOOM_RELATED_ONE_ALL_MODE ?? 'cda';
const relatedField = process.env.LOOM_RELATED_ONE_ALL_FIELD ?? 'id';
const relatedCaseName = relatedMode === 'cda' && relatedField === 'specimen-reference'
  ? 'cda-specimen-reference-raw-oracle-one-all-lifecycle'
  : `${relatedMode}-${relatedField}-raw-oracle-one-all-lifecycle`;
const relatedScenarioID = relatedMode === 'cda' && relatedField === 'specimen-reference'
  ? 'cda-related-one-all-specimen-reference'
  : 'cda-related-one-all';
const basicRelatedTarget = relatedMode === 'basic' ? {
  ...(process.env.LOOM_DEV_PROJECT ? { cdaProject: process.env.LOOM_DEV_PROJECT } : {}),
  ...(process.env.LOOM_API_ORIGIN ? { cdaApiOrigin: process.env.LOOM_API_ORIGIN } : {}),
  ...(process.env.LOOM_UI_ORIGIN ? { cdaUiOrigin: process.env.LOOM_UI_ORIGIN } : {}),
  ...(process.env.LOOM_DEV_COMPOSE_PROJECT || process.env.LOOM_CDA_COMPOSE_PROJECT
    ? { cdaComposeProject: process.env.LOOM_DEV_COMPOSE_PROJECT ?? process.env.LOOM_CDA_COMPOSE_PROJECT } : {}),
  ...(process.env.LOOM_CDA_API_CONTAINER ? { cdaApiContainer: process.env.LOOM_CDA_API_CONTAINER } : {}),
  ...(process.env.LOOM_ARANGO_CONTAINER ? { cdaArangoContainer: process.env.LOOM_ARANGO_CONTAINER } : {}),
  ...(process.env.LOOM_DEV_GENERATION ? { cdaGeneration: process.env.LOOM_DEV_GENERATION } : {}),
  ...(process.env.LOOM_DEV_SOURCE_ROOT || process.env.LOOM_CDA_SOURCE_ROOT
    ? { cdaSourceRoot: process.env.LOOM_DEV_SOURCE_ROOT ?? process.env.LOOM_CDA_SOURCE_ROOT } : {}),
  ...(process.env.LOOM_DEV_FIXTURE_DIR || process.env.LOOM_CDA_DATASET_DIR
    ? { cdaDatasetDir: process.env.LOOM_DEV_FIXTURE_DIR ?? process.env.LOOM_CDA_DATASET_DIR } : {}),
  ...(process.env.LOOM_DEV_API_PORT || process.env.LOOM_CDA_API_PORT
    ? { cdaApiPort: process.env.LOOM_DEV_API_PORT ?? process.env.LOOM_CDA_API_PORT } : {}),
  ...(process.env.LOOM_DEV_UI_PORT || process.env.LOOM_CDA_UI_PORT
    ? { cdaUiPort: process.env.LOOM_DEV_UI_PORT ?? process.env.LOOM_CDA_UI_PORT } : {}),
} : {};

test.describe('CDA nested repeated component values', () => {
  test.use({
    cdaScenarioID: 'cda-nested-repeated',
    cdaCaseName: 'nested-coding-items-preserve-base-observation-row-identities',
  });

  test('expands nested component coding items and verifies exact raw source identities', async ({ page, cda }) => {
    const result = await nestedRepeatedWorkflow({ page, cda });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.assertions.some(assertion => assertion.status === 'passed')).toBe(true);
    expect(result.status).toBe('passed');
  });
});

test.describe('CDA related source ONE and ALL values', () => {
  test.use({
    ...(process.env.LOOM_API_ORIGIN ? { cdaApiOrigin: process.env.LOOM_API_ORIGIN } : {}),
    ...(process.env.LOOM_UI_ORIGIN ? { cdaUiOrigin: process.env.LOOM_UI_ORIGIN } : {}),
    ...basicRelatedTarget,
    cdaScenarioID: relatedScenarioID,
    cdaCaseName: relatedCaseName,
  });

  test(`preserves ${relatedMode} ${relatedField} behavior against raw related records`, async ({ page, cda }) => {
    const result = await relatedOneAllWorkflow({ page, cda });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.cases.length).toBeGreaterThan(0);
    expect(result.status).toBe('passed');
  });
});

test.describe('CDA zero Observation related source ONE and ALL', () => {
  test.use({
    ...(process.env.LOOM_API_ORIGIN ? { cdaApiOrigin: process.env.LOOM_API_ORIGIN } : {}),
    ...(process.env.LOOM_UI_ORIGIN ? { cdaUiOrigin: process.env.LOOM_UI_ORIGIN } : {}),
    cdaScenarioID: 'cda-related-one-all-zero-observation',
    cdaCaseName: 'zero-patient-observation-one-all-stable-parent-lifecycle',
  });

  test('keeps an exact zero-match Patient through native ONE/ALL and restoration', async ({ page, cda }) => {
    const result = await relatedOneAllWorkflow({ page, cda, mode: 'cda', fieldMode: 'id', witnessMode: 'zero' });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.cases.length).toBeGreaterThan(0);
    expect(result).toMatchObject({ project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });
    expect(result.oracle.exactMembershipScope).toMatchObject({
      project: 'loom_dev_cda_fhir',
      generation: 'cda-fhir-v1',
      rootResourceType: 'Patient',
      selectedRootCount: 1,
      incomingObservationEdgeCount: 0,
    });
    expect(result.oracle).toMatchObject({
      finalScopedRereadMatched: true,
      witnessSummary: { rootCount: 1, distinctIncomingObservationCount: 0 },
    });
    expect(result.zeroObservationExpansion).toMatchObject({
      savedPolicy: 'PRESERVE_PARENT',
      previewRowCount: 1,
      outputNullable: true,
      previewSchemaNullable: true,
      unmatchedObservationOutputIsNull: true,
    });
    expect(result.zeroObservationOne).toMatchObject({ previewStatus: 'READY', rowCount: 1, value: null });
    expect(result.zeroObservationAll).toMatchObject({ previewStatus: 'READY', rowCount: 1, values: [] });
    expect(result.zeroObservationSavedAll).toMatchObject({
      candidateSourceMatches: true,
      form: 'ALL',
      rowValuePolicy: 'ALL',
      savedParentCount: 1,
      previewValues: [],
      reloadParentCount: 1,
      reloadValues: [],
      reloadSourceMatches: true,
    });
    expect(result.relatedSourceRestoration).toMatchObject({
      constructionMatchesBaseline: true,
      populationMatchesBaseline: true,
      columnsMatchBaseline: true,
      previewRowsMatchBaseline: true,
      relatedOutputAbsent: true,
    });
    expectLifecycleChecks(cda.report.assertions, zeroObservationLifecycleCheckNames);
    expect(result.status).toBe('passed');
  });
});

test.describe('CDA related Observation.status grouped-row ONE to ALL', () => {
  test.use({
    ...(process.env.LOOM_API_ORIGIN ? { cdaApiOrigin: process.env.LOOM_API_ORIGIN } : {}),
    ...(process.env.LOOM_UI_ORIGIN ? { cdaUiOrigin: process.env.LOOM_UI_ORIGIN } : {}),
    cdaScenarioID: 'cda-related-one-all-observation-status',
    cdaCaseName: 'observation-status-one-to-all-same-chooser-lifecycle',
  });

  test('repairs a distinct-status ONE rejection through the same chooser and restores the exact Group', async ({ page, cda }) => {
    const result = await relatedOneAllWorkflow({ page, cda, mode: 'cda', fieldMode: 'status' });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.cases.length).toBeGreaterThan(0);
    expect(result).toMatchObject({
      project: 'loom_dev_cda_fhir',
      generation: 'cda-fhir-v1',
      statusSourceContract: {
        path: 'Observation.status',
        logicalType: 'string',
        cardinality: 'optional_one',
        relatedSourceForm: 'ALL',
      },
      oneRejection: { status: 422, errorCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' },
      oneChooserAfterRejection: {
        open: true,
        policy: 'ONE',
        routeChecked: true,
        formChecked: true,
        addEnabled: true,
        savedGroupUnchanged: true,
      },
      relatedAllOutputEvidence: { policy: 'ALL', form: 'ALL', contributorPolicy: 'ALL_MATCHES', identitiesMatch: true },
      relatedAllCancelEvidence: {
        workspaceUnchanged: true,
        constructionUnchanged: true,
        populationUnchanged: true,
        renderedRowsMatch: true,
      },
      relatedAllSavedEvidence: { candidateSourceMatches: true, form: 'ALL', rowValuePolicy: 'ALL', reloadSourceMatches: true },
      relatedOutputEditEvidence: { labelChanged: true, candidateSourceMatches: true, policy: 'ALL' },
      relatedSourceRestoration: {
        constructionMatchesBaseline: true,
        populationMatchesBaseline: true,
        columnsMatchBaseline: true,
        previewRowsMatchBaseline: true,
        relatedOutputAbsent: true,
      },
    });
    expect(result.oneRejection.distinctStatusValues.length).toBeGreaterThan(1);
    expect(result.relatedAllOutputEvidence.previewValues).toEqual(result.relatedAllOutputEvidence.expectedValues);
    expect(result.relatedAllSavedEvidence.reloadValues).toEqual(result.relatedAllSavedEvidence.expectedValues);
    expect(result.relatedOutputEditEvidence.reloadValues).toEqual(result.relatedOutputEditEvidence.expectedValues);
    expect(result.oracle.exactMembershipScope).toMatchObject({
      project: 'loom_dev_cda_fhir',
      generation: 'cda-fhir-v1',
      rootResourceType: 'Specimen',
    });
    expect(result.oracle).toMatchObject({ finalScopedRereadMatched: true });
    const manyWitness = result.oracle.witnesses.find(witness => witness.category === 'many-distinct-statuses');
    expect(result.oracle.finalStatusMembership.observationPairs).toEqual(
      manyWitness.observationKeys.map((key, index) => ({
        id: manyWitness.observationIds[index],
        _id: key,
        status: manyWitness.observationStatuses[index],
      })).sort((left, right) => left._id.localeCompare(right._id)),
    );
    expectLifecycleChecks(cda.report.assertions, statusOneAllLifecycleCheckNames);
    expect(result.status).toBe('passed');
  });
});

test.describe('CDA zero Observation saved RelatedExpand empty-policy edit', () => {
  test.use({
    ...(process.env.LOOM_API_ORIGIN ? { cdaApiOrigin: process.env.LOOM_API_ORIGIN } : {}),
    ...(process.env.LOOM_UI_ORIGIN ? { cdaUiOrigin: process.env.LOOM_UI_ORIGIN } : {}),
    cdaScenarioID: 'cda-related-one-all-zero-observation',
    cdaCaseName: 'zero-patient-observation-exclude-policy-edit-cancel-apply-restoration',
  });

  test('edits a saved zero-match RelatedExpand from PRESERVE_PARENT to EXCLUDE', async ({ page, cda }) => {
    const result = await relatedOneAllWorkflow({ page, cda, mode: 'cda', fieldMode: 'id', witnessMode: 'zero',
      savedEmptyPolicyEdit: true });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.cases.length).toBeGreaterThan(0);
    expect(result.status).toBe('passed');
  });
});

test.describe('CDA repeated component empty-list policies', () => {
  test.use({
    cdaScenarioID: 'cda-repeated-empty',
    cdaCaseName: 'preserve-parent-exclude-error-group-count-and-restoration',
  });

  test('preserves empty and populated component owners through row policy and GROUP lifecycle', async ({ page, cda }) => {
    const result = await repeatedEmptyWorkflow({ page, cda });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.assertions.some(assertion => assertion.status === 'passed')).toBe(true);
    expect(result.status).toBe('passed');
  });
});

test.describe('CDA missing-component EXPANDED and GROUP lifecycle', () => {
  test.use({
    cdaScenarioID: 'cda-repeated-missing-component-group',
    cdaCaseName: 'missing-component-expanded-group-cancel-restore',
  });

  test('preserves missing-component owners through EXPANDED and GROUP removal Cancel', async ({ page, cda }) => {
    const result = await repeatedEmptyWorkflow({ page, cda, mode: 'missing-component-group' });
    const skipReason = missingComponentGroupSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.assertions.some(assertion => assertion.status === 'passed')).toBe(true);
    expect(result.status).toBe('passed');
  });
});

test.describe('CDA repeated component rows', () => {
  test.use({
    cdaScenarioID: 'cda-repeated-rows',
    cdaCaseName: 'component-array-source-expand-field-remove-and-row-restoration',
  });

  test('applies source expansion, verifies item values, and restores original Observation rows', async ({ page, cda }) => {
    const result = await repeatedRowsWorkflow({ page, cda });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.assertions.some(assertion => assertion.status === 'passed')).toBe(true);
    expect(result.status).toBe('passed');
  });
});
