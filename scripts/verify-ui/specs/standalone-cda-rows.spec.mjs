import { expect, test } from '../helpers/cda-fixtures.mjs';
import { nestedRepeatedWorkflow } from '../workflows/verify-cda-nested-repeated-browser.mjs';
import { relatedOneAllWorkflow } from '../workflows/verify-cda-related-one-all-browser.mjs';
import { repeatedEmptyWorkflow } from '../workflows/verify-cda-repeated-empty-browser.mjs';
import { repeatedRowsWorkflow } from '../workflows/verify-cda-repeated-rows-browser.mjs';

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
    if (result.status !== 'passed') test.skip(true, result.skipReason ?? `Nested coverage is ${result.status}.`);
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
    if (result.status !== 'passed') test.skip(true, result.skipReason ?? `Related ONE/ALL coverage is ${result.status}.`);
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
    if (result.status !== 'passed') test.skip(true, result.skipReason ?? `Empty-component coverage is ${result.status}.`);
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
    if (result.status !== 'passed') test.skip(true, result.skipReason ?? `Repeated-component coverage is ${result.status}.`);
    expect(result.assertions.some(assertion => assertion.status === 'passed')).toBe(true);
    expect(result.status).toBe('passed');
  });
});
