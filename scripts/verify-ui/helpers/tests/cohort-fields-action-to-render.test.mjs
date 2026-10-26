import assert from 'node:assert/strict';
import test from 'node:test';
import { summarizeLifecycleEvidence } from '../coverage-status.mjs';
import {
  CDA_ACTION_TO_RENDER_BUDGET_MS,
  summarizeCdaActionToRenderTimings,
} from '../cda-action-to-render-budget.mjs';
import { assertVisibleCohortMemberIds } from '../../workflows/cohort-fields-workflow.mjs';

const performanceCheckName = 'native cohort Apply and member-field Apply render exact scoped rows within five seconds';
const expectedCheckpointNames = [
  'Apply named cohort through exact scoped Specimen member rows',
  'Apply Resource Type member field through exact scoped grid values',
];

const performanceAssertion = (timings) => {
  const timingSummary = summarizeCdaActionToRenderTimings(timings, CDA_ACTION_TO_RENDER_BUDGET_MS);
  return {
    report: {
      dimensions: { performance: timingSummary.withinBudget ? 'passed' : 'failed' },
      assertions: [{
        name: performanceCheckName,
        dimension: 'performance',
        status: timingSummary.withinBudget ? 'passed' : 'failed',
        evidence: {
          budgetMs: timingSummary.budgetMs,
          checkpointCount: timingSummary.checkpointCount,
          maximumDurationMs: timingSummary.maximumDurationMs,
          withinBudget: timingSummary.withinBudget,
          measuredTransitionCount: timingSummary.checkpointCount,
          actionCount: 4,
          maxActionMs: 300,
          timingCheckpoints: timingSummary.checkpoints,
        },
      }],
    },
    timingSummary,
  };
};

const contract = {
  requiredChecks: [performanceCheckName],
  performanceCheckName,
  lifecycleEvidence: { performance: { check: performanceCheckName, checkpointBudgetMs: 5_000 } },
};

test('cohort Apply and member-field Apply checkpoints reach the bracket summary', () => {
  const { report, timingSummary } = performanceAssertion([
    { name: expectedCheckpointNames[0], durationMs: 1_644 },
    { name: expectedCheckpointNames[1], durationMs: 1_201 },
  ]);
  const bracket = summarizeLifecycleEvidence(report, contract);

  assert.equal(report.assertions[0].status, 'passed');
  assert.equal(timingSummary.budgetMs, 5_000);
  assert.equal(timingSummary.checkpointCount, 2);
  assert.deepEqual(timingSummary.checkpoints.map(({ name }) => name), expectedCheckpointNames);
  assert.equal(bracket.status, 'passed');
  assert.equal(bracket.dimensions.performance, 'passed');
  assert.equal(bracket.renderCheckpoints.status, 'present');
  assert.equal(bracket.renderCheckpoints.count, 2);
  assert.equal(bracket.renderCheckpoints.maximumDurationMs, 1_644);
  assert.deepEqual(bracket.renderCheckpoints.checkpoints.map(({ checkName, name, durationMs }) => ({ checkName, name, durationMs })), [
    { checkName: performanceCheckName, name: expectedCheckpointNames[0], durationMs: 1_644 },
    { checkName: performanceCheckName, name: expectedCheckpointNames[1], durationMs: 1_201 },
  ]);
});

test('one cohort Apply-through-grid interval over five seconds fails', () => {
  const { report, timingSummary } = performanceAssertion([
    { name: expectedCheckpointNames[0], durationMs: 5_001 },
    { name: expectedCheckpointNames[1], durationMs: 1_201 },
  ]);

  const bracket = summarizeLifecycleEvidence(report, contract);
  assert.equal(report.assertions[0].status, 'failed');
  assert.equal(timingSummary.withinBudget, false);
  assert.equal(timingSummary.maximumDurationMs, 5_001);
  assert.equal(timingSummary.checkpoints[0].durationMs, 5_001,
    'Apply and settled-grid wait remain one continuous measured interval');
  assert.equal(bracket.dimensions.performance, 'failed');
  assert.equal(bracket.renderCheckpoints.maximumDurationMs, 5_001);
});

test('visible cohort cells must contain exactly the scoped source_identity IDs', () => {
  const expectedIds = [
    '00001c68-2c20-5003-a144-b2442469d8de',
    '0000ae4e-5498-51e8-a091-32b0552093ac',
  ];
  // Literal visibleMemberCell captured in the retained UsOiky domain report.
  const visibleMemberCell = `payload: collection: {"bodySite":{"reference":{"reference":"BodyStructure/4e5ae09f-f81e-5126-a6d9-97ac10405700"}}} · extension: {"url":"http://fhir-aggregator.org/fhir/StructureDefinition/part-of-study","valueReference":{"reference":"ResearchStudy/f421997b-c463-5a61-8b5c-75a252b125b2"}} · id: 00001c68-2c20-5003-a144-b2442469d8de · identifier: MP2PRT-ALL.MP2PRT-PATIVJ.MP2PRT-PATIVJ-NB1-A-1-0-D-A82L-36 · parent: Specimen/8307c3a6-eaae-53b6-ab12-b4d8afcefb1d · project_id: loom_dev_cda_fhir · resourceType: Specimen · subject: Patient/235d274f-c6a2-533f-8378-eb8311757b47 · type: Normal · source_identity: generation: cda-fhir-v1 · id: 00001c68-2c20-5003-a144-b2442469d8de · project: loom_dev_cda_fhir · resource_type: Specimen; payload: extension: {"url":"http://fhir-aggregator.org/fhir/StructureDefinition/part-of-study","valueReference":{"reference":"ResearchStudy/e7cf89a0-51d9-5244-a2af-d8875bf649f1"}} · id: 0000ae4e-5498-51e8-a091-32b0552093ac · identifier: TARGET-OS.TARGET-40-PAPXWB.41709e15-fb79-5731-b55e-87f3284e9752 · parent: Specimen/2d7d6c59-dc0f-5e2a-a428-f3031d9f4ed0 · project_id: loom_dev_cda_fhir · resourceType: Specimen · subject: Patient/0cacfe56-bd44-53db-abe6-d1e7e6748e4a · type: Tumor · source_identity: generation: cda-fhir-v1 · id: 0000ae4e-5498-51e8-a091-32b0552093ac · project: loom_dev_cda_fhir · resource_type: Specimen`;
  const [firstRecord, secondRecord] = visibleMemberCell.split('; ');

  assert.deepEqual(assertVisibleCohortMemberIds(visibleMemberCell, expectedIds).sort(), [...expectedIds].sort());
  assert.throws(() => assertVisibleCohortMemberIds(firstRecord, expectedIds), /exactly match/,
    'A missing visible member ID must fail');
  const extraRecord = `${secondRecord.replace(`id: ${expectedIds[1]} · project:`, 'id: 0000d5d5-39e2-4af9-8bcb-e672a557c6d5 · project:')}`;
  assert.throws(() => assertVisibleCohortMemberIds(`${visibleMemberCell}; ${extraRecord}`, expectedIds), /exactly match/,
    'An extra visible member ID must fail');
  const duplicateRecord = secondRecord.replace(`id: ${expectedIds[1]} · project:`, `id: ${expectedIds[0]} · project:`);
  assert.throws(() => assertVisibleCohortMemberIds(`${firstRecord}; ${duplicateRecord}`, expectedIds), /exactly match/,
    'A duplicate visible member ID must fail');
  assert.throws(() => assertVisibleCohortMemberIds(`payload id: ${expectedIds[0]}; id: ${expectedIds[1]}`, expectedIds), /exactly match/,
    'Arbitrary id fields must not be treated as source identities');
  const wrongScopeIdentity = 'source_identity: generation: cda-fhir-v9 · id: 0000d5d5-39e2-4af9-8bcb-e672a557c6d5 · project: loom_dev_cda_fhir · resource_type: Specimen';
  assert.throws(() => assertVisibleCohortMemberIds(`${visibleMemberCell}; ${wrongScopeIdentity}`, expectedIds), /Every visible source_identity segment/,
    'A source_identity with the wrong generation must not be ignored');
});
