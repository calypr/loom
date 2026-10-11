import {
  expect,
  shouldCreateFreshBasicQuantityProject,
  test,
} from '../helpers/cda-fixtures.mjs';
import { hasSameQuantityValueMultiset } from '../helpers/basic-related-quantity-one-all-oracle.mjs';
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
const basicQuantityMode = relatedMode === 'basic' && relatedField === 'quantity';
const relatedCaseName = basicQuantityMode
  ? 'basic-height-quantity-one-all-lifecycle'
  : relatedMode === 'cda' && relatedField === 'specimen-reference'
    ? 'cda-specimen-reference-raw-oracle-one-all-lifecycle'
    : `${relatedMode}-${relatedField}-raw-oracle-one-all-lifecycle`;
const relatedScenarioID = basicQuantityMode
  ? 'basic-related-quantity-one-all'
  : relatedMode === 'cda' && relatedField === 'specimen-reference'
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
    cdaCreateFreshBasicQuantityProject: shouldCreateFreshBasicQuantityProject({
      scenarioID: relatedScenarioID,
      caseName: relatedCaseName,
    }),
  });

  test(`preserves ${relatedMode} ${relatedField} behavior against raw related records`, async ({ page, cda }) => {
    const result = await relatedOneAllWorkflow({ page, cda });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);
    expect(result.cases.length).toBeGreaterThan(0);
    if (relatedScenarioID !== 'basic-related-quantity-one-all') expect(result.status).toBe('passed');
    if (relatedScenarioID === 'basic-related-quantity-one-all') {
      expect(result.project).toBe(cda.project);
      expect(result.generation).toBe(cda.generation);
      expect(result.project).not.toBe(process.env.LOOM_DEV_PROJECT);
      expect(result.project).toMatch(/^loom_dev_verify_/);
      expect(result.quantitySourceContract).toMatchObject({
        path: 'Observation.valueQuantity.value',
        logicalType: 'decimal',
        cardinality: 'optional_one',
        operation: 'RELATED_SOURCE',
        codeFamily: { system: 'https://example.test/codes', code: 'height', display: 'Height' },
        onePolicyExpectedError: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES',
        allValuesPreserveNumericType: true,
      });
      expect(result.oracle.literalFixtureCrossCheck).toMatchObject({
        fixture: 'testdata/devloop-fixture/Patient.ndjson + Observation.ndjson',
        patientId: 'dev-patient-001',
        codeFamily: { system: 'https://example.test/codes', code: 'height', display: 'Height' },
      });
      expect(result.oracle.literalFixtureCrossCheck.observationValues).toEqual([
        { observationID: 'dev-observation-001', code: { system: 'https://example.test/codes', code: 'height', display: 'Height' },
          value: 172.5, quantity: { value: 172.5, unit: 'cm' } },
        { observationID: 'dev-observation-003', code: { system: 'https://example.test/codes', code: 'height', display: 'Height' },
          value: 180, quantity: { value: 180, unit: 'cm' } },
      ]);
      expect(result.oracle).toMatchObject({
        exactMembershipScope: { rootResourceType: 'Patient', patientId: 'dev-patient-001', observationCount: 2 },
        finalScopedRereadMatched: true,
      });
      expect(result.oneRejection).toMatchObject({
        status: 422,
        errorCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES',
        distinctQuantityValues: [172.5, 180],
        manyObservationIds: ['dev-observation-001', 'dev-observation-003'],
      });
      expect(result.oneChooserAfterRejection).toMatchObject({
        open: true, policy: 'ONE', routeChecked: true, formChecked: true, addEnabled: true, savedGroupUnchanged: true,
      });
      expect(result.quantityAllOutputEvidence).toMatchObject({
        expectedValues: [172.5, 180], identitiesMatch: true, logicalType: 'decimal',
      });
      expect(hasSameQuantityValueMultiset(result.quantityAllOutputEvidence.previewValues, [172.5, 180])).toBe(true);
      expect(result.quantityAllCancelEvidence).toEqual({
        workspaceUnchanged: true, constructionUnchanged: true, populationUnchanged: true, renderedRowsMatch: true,
      });
      expect(result.quantityAllSavedEvidence).toMatchObject({
        logicalType: 'decimal', expectedValues: [172.5, 180], sourceMatches: true,
      });
      expect(hasSameQuantityValueMultiset(result.quantityAllSavedEvidence.previewValues, [172.5, 180])).toBe(true);
      expect(hasSameQuantityValueMultiset(result.quantityAllSavedEvidence.reloadValues, [172.5, 180])).toBe(true);
      expect(result.relatedOutputEditEvidence).toMatchObject({
        labelChanged: true, candidateSourceMatches: true, policy: 'ALL',
        expectedValues: [172.5, 180],
      });
      expect(hasSameQuantityValueMultiset(result.relatedOutputEditEvidence.reloadValues, [172.5, 180])).toBe(true);
      expect(result.relatedSourceRestoration).toMatchObject({
        constructionMatchesBaseline: true, populationMatchesBaseline: true, columnsMatchBaseline: true,
        previewRowsMatchBaseline: true, relatedOutputAbsent: true, patientIds: ['dev-patient-001'],
      });
      const timedSteps = [
        'related-observation-one-disagreement',
        'same-chooser-all-repair-preview',
        'cancel-related-all-proposal-preserves-group',
        'reopened-all-preview-before-apply',
        'apply-related-all-to-native-table-render',
        'native-preview-matches-exact-related-field-oracle',
        'reload-related-all-available-cardinality-values',
        'edit-related-field-output-label',
        'reload-edited-related-field-output-label',
        'remove-related-source-step-preview',
        'apply-remove-related-step-restores-native-group-table',
        'reload-restored-available-witness-group-table',
      ];
      const durationByName = new Map(result.cases.map(({ name, durationMs }) => [name, durationMs]));
      for (const name of timedSteps) {
        const durationMs = durationByName.get(name);
        expect(Number.isFinite(durationMs), `${name} must report its action-to-render duration`).toBe(true);
        expect(durationMs, `${name} must settle within five seconds`).toBeLessThanOrEqual(5000);
      }
      expectLifecycleChecks(cda.report.assertions, [
        'Basic Quantity ONE rejects the two exact same-code decimal values and ALL renders the full numeric array',
        'Cancel preserves the Group and Apply/edit retain the exact Quantity source binding',
        'Reload preserves the exact decimal array and removal restores the original Patient Group rows',
        'Basic Quantity ONE-to-ALL lifecycle action-to-render checkpoints finish within five seconds',
      ]);
    }
    if (relatedScenarioID === 'cda-related-one-all-specimen-reference') {
      const requiredSettledCheckpoints = [
        'related-observation-one-disagreement',
        'same-chooser-all-repair-preview',
        'cancel-related-all-proposal-preserves-group',
        'reopened-all-preview-before-apply',
        'apply-related-all-to-native-table-render',
        'native-preview-matches-exact-related-field-oracle',
        'reload-related-all-available-cardinality-values',
        'edit-related-field-output-label',
        'reload-edited-related-field-output-label',
        'remove-related-source-step-preview',
        'apply-remove-related-step-restores-native-group-table',
        'reload-restored-available-witness-group-table',
      ];
      const durationByName = new Map(result.cases.map(({ name, durationMs }) => [name, durationMs]));
      for (const name of requiredSettledCheckpoints) {
        const durationMs = durationByName.get(name);
        expect(Number.isFinite(durationMs), `${name} must have an end-to-end duration`).toBe(true);
        expect(durationMs, `${name} must settle within five seconds`).toBeLessThanOrEqual(5000);
      }
    }
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
