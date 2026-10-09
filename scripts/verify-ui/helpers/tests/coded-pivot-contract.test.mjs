import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { chromium } from 'playwright';
import { hasLifecycleContract, registry, scenarioCaseFor } from '../../registry.mjs';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { createCdaInspector } from '../cda-playwright.mjs';
import { classifyExpectedCdaCancellation } from '../cda-fixtures.mjs';
import { DEFAULT_ACTION_TO_RENDER_BUDGET_MS, recordPivotActionToRender } from '../quantity-pivot-budget.mjs';
import {
  CODED_PIVOT_OBSERVATION_ID,
  codedPivotExpectedHeaderValuesFor,
  codedPivotFailureDomSnapshot,
  codedPivotFirstTableReady,
  codedPivotBackToTableControl,
  codedPivotFixtureFor,
  codedPivotRemovalProposalReady,
  codedPivotRenderedValuesFor,
  codedPivotRestoredSourceRowVisible,
  codedPivotSourceControls,
  codedPivotSourceRadioFor,
  codedPivotValuesFor,
} from '../coded-pivot-fixture.mjs';
import {
  codedPivotProposalRequestMatches,
  codedPivotEditorDisposalCancellationEvidenceFor,
  codedPivotPolicyReplacementCancellationEvidenceFor,
  codedPivotFirstFailureEvidenceFor,
  codedPivotRemovalRequestMatches,
  codedPivotPersistedSourceBindingsEqual,
  codedPivotPersistedSourceBindingsFor,
  codedPivotPersistedSourceMatchesOption,
  codedPivotSourceOptionsDiagnosticFor,
  summarizeCodedPivotNativeRequests,
} from '../coded-pivot-native-evidence.mjs';

const scenarioID = 'standalone-reshape-coded-pivot';
const source = (component, overrides = {}) => ({
  id: CODED_PIVOT_OBSERVATION_ID,
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
  resourceType: 'Observation',
  component,
  ...overrides,
});
const codedComponent = (code, value) => ({
  code: { coding: [{ system: 'https://cda.readthedocs.io', code }] },
  ...value,
});

test('integer and string coded Pivot fixtures require the exact scoped Observation values', () => {
  const integer = codedPivotFixtureFor('integer');
  assert.deepEqual(integer.map(({ system, code, type, value }) => ({ system, code, type, value })), [{ system: 'https://cda.readthedocs.io', code: 'days_to_collection', type: 'integer', value: '162' }]);
  const rawArtifact = JSON.parse(readFileSync(new URL('./fixtures/coded-pivot-integer-raw-observation.json', import.meta.url), 'utf8'));
  assert.equal(rawArtifact.rowCount, 1);
  const observedInteger = { ...rawArtifact.rows[0], project: rawArtifact.scope.project, generation: rawArtifact.scope.generation };
  assert.deepEqual(codedPivotValuesFor(observedInteger, { mode: 'integer', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' }), [
    { code: 'days_to_collection', type: 'integer', value: '162' },
  ]);

  const strings = codedPivotFixtureFor('string');
  assert.deepEqual(strings.map(({ code, value }) => [code, value]), [
    ['specimen_type', 'analyte'],
    ['primary_disease_type', 'Ductal and lobular neoplasms'],
  ]);
  assert.deepEqual(codedPivotValuesFor(source([
    codedComponent('specimen_type', { valueString: 'analyte' }),
    codedComponent('primary_disease_type', { valueString: 'Ductal and lobular neoplasms' }),
  ]), { mode: 'string', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' }), [
    { code: 'specimen_type', type: 'string', value: 'analyte' },
    { code: 'primary_disease_type', type: 'string', value: 'Ductal and lobular neoplasms' },
  ]);
});

test('coded Pivot raw oracle rejects scope drift, duplicate codes, and wrong scalar types', () => {
  const options = { mode: 'integer', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' };
  const exact = codedComponent('days_to_collection', { valueInteger: 162 });
  assert.throws(() => codedPivotValuesFor(source([exact], { id: 'different-observation' }), options), /exact Observation fixture/);
  assert.throws(() => codedPivotValuesFor(source([exact], { resourceType: 'Patient' }), options), /exact Observation fixture/);
  assert.throws(() => codedPivotValuesFor(source([exact], { project: 'different-project' }), options), /project and generation/);
  assert.throws(() => codedPivotValuesFor(source([exact], { generation: 'other-generation' }), options), /project and generation/);
  assert.throws(() => codedPivotValuesFor(source([exact, exact]), options), /exactly one raw component/);
  assert.throws(() => codedPivotValuesFor(source([]), options), /exactly one raw component/);
  assert.throws(() => codedPivotValuesFor(source([codedComponent('days_to_collection', { valueInteger: '162' })]), options), /integer/);
  const rawArtifact = JSON.parse(readFileSync(new URL('./fixtures/coded-pivot-integer-raw-observation.json', import.meta.url), 'utf8'));
  const rawSource = () => ({ ...rawArtifact.rows[0], project: rawArtifact.scope.project, generation: rawArtifact.scope.generation });
  const wrongValue = rawSource();
  wrongValue.component[0].valueInteger = 163;
  assert.throws(() => codedPivotValuesFor(wrongValue, options), /must equal 162/);
  const missingValue = rawSource();
  delete missingValue.component[0].valueInteger;
  assert.throws(() => codedPivotValuesFor(missingValue, options), /must equal 162/);
  const quantityInsteadOfInteger = rawSource();
  delete quantityInsteadOfInteger.component[0].valueInteger;
  quantityInsteadOfInteger.component[0].valueQuantity = { value: 162 };
  assert.throws(() => codedPivotValuesFor(quantityInsteadOfInteger, options), /must equal 162/);
  assert.throws(() => codedPivotValuesFor(source([
    codedComponent('specimen_type', { valueString: 42 }),
    codedComponent('primary_disease_type', { valueString: 'Ductal and lobular neoplasms' }),
  ]), { ...options, mode: 'string' }), /string/);
  assert.throws(() => codedPivotFixtureFor('decimal'), /Unsupported coded Pivot fixture mode/);
});

test('saved coded Pivot retains the selected native frame as canonical source bindings', () => {
  // Captured selected options and proposal source DTOs from the retained native
  // integer/string runs; the signed choice is sent to the proposal endpoint,
  // which returns the canonical candidate/node identity persisted by Apply.
  const retainedBindings = [
    {
      selectedOption: {
        bindingId: 'd7048a207d827b0bccfc316f91919b6034e5fa834e0e81d13c945f7e48023646',
        resourceType: 'Observation', sourcePath: 'component[]', sourceCanonical: 'Observation.component[]',
        owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'valueInteger', logicalType: 'integer', route: [],
      },
      source: {
        candidateId: 'c_324e141020d913db3a403878', nodeId: 'n_f396c8ca728f4349fa8d2e3c',
        fieldPath: 'component[].valueInteger', route: [],
        family: {
          bindingId: 'd7048a207d827b0bccfc316f91919b6034e5fa834e0e81d13c945f7e48023646',
          resourceType: 'Observation', sourcePath: 'component[]', sourceCanonical: 'Observation.component[]',
          owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'valueInteger',
          choiceArms: ['valueInteger'], logicalType: 'integer', ruleVersion: '4', schemaVersion: 3,
        },
      },
    },
    {
      selectedOption: {
        bindingId: '900b911e87387e947c1e245abaaa1dc129c05c81ee0b0e9248a7be5df929fb09',
        resourceType: 'Observation', sourcePath: 'component[]', sourceCanonical: 'Observation.component[]',
        owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'valueString', logicalType: 'string', route: [],
      },
      source: {
        candidateId: 'c_8a521937841fa94c13f8ca21', nodeId: 'n_f396c8ca728f4349fa8d2e3c',
        fieldPath: 'component[].valueString', route: [],
        family: {
          bindingId: '900b911e87387e947c1e245abaaa1dc129c05c81ee0b0e9248a7be5df929fb09',
          resourceType: 'Observation', sourcePath: 'component[]', sourceCanonical: 'Observation.component[]',
          owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'valueString',
          choiceArms: ['valueString'], logicalType: 'string', ruleVersion: '4', schemaVersion: 3,
        },
      },
    },
  ];

  const stepFor = source => ({ operation: { kind: 'CODED_PIVOT', codedPivot: { source } } });
  for (const { selectedOption, source } of retainedBindings) {
    const proposalStep = stepFor(source);
    const savedStep = stepFor(structuredClone(source));
    assert.deepEqual(codedPivotPersistedSourceBindingsFor(savedStep), source);
    assert.equal(codedPivotPersistedSourceMatchesOption(savedStep, selectedOption), true);
    assert.equal(codedPivotPersistedSourceBindingsEqual(proposalStep, savedStep), true);

    for (const field of ['candidateId', 'nodeId']) {
      const changedSource = { ...source, [field]: `${source[field]}-other` };
      assert.equal(codedPivotPersistedSourceBindingsEqual(proposalStep, stepFor(changedSource)), false,
        `changed ${field} must not survive proposal-to-saved source binding comparison`);
    }
    const changedFamily = { ...source, family: { ...source.family, bindingId: 'unrelated-frame-binding' } };
    assert.equal(codedPivotPersistedSourceMatchesOption(stepFor(changedFamily), selectedOption), false);
    const changedScalar = { ...source, fieldPath: 'component[].valueQuantity.value', family: {
      ...source.family, valuePath: 'valueQuantity.value', choiceArms: ['valueQuantity.value'],
    } };
    assert.equal(codedPivotPersistedSourceMatchesOption(stepFor(changedScalar), selectedOption), false);
    const changedRoute = { ...source, route: [{ stepId: 'unrelated-step' }] };
    assert.equal(codedPivotPersistedSourceMatchesOption(stepFor(changedRoute), selectedOption), false);
  }
});

test('coded Pivot proposal matcher accepts retained integer and string native request shapes', () => {
  // Reduced literal DTOs from bounded reports: integer SHA-256 70f2c13139dd129d5c4e8ddaa82a9e793fdd7dc645e864419ef29ea3bc5c0ea9
  // (nativeRequests[26], [45]); string SHA-256 ab8ba56cb2c54826229acd9a61c292412d031fcf9fe43ec3107af5f1d7e027cb
  // (nativeRequests[26], [45]). Run-scoped signed choice IDs, snapshot tokens, and draft digests use explicit redacted stand-ins;
  // output, step, and column IDs are stable aliases, with all same-request equality links preserved.
  const snapshotToken = 'sha256:<redacted-run-snapshot>';
  const outputId = 'out_<redacted-run-output>';
  const stepId = 'coded-pivot_<redacted-run-step>';
  const integerSourceChoiceId = 'cc2.<redacted-integer-frame-choice>';
  const stringSourceChoiceId = 'cc2.<redacted-string-frame-choice>';
  const integerCategoryChoiceId = 'cc2.<redacted-days-to-collection-category-choice>';
  const specimenCategoryChoiceId = 'cc2.<redacted-specimen-type-category-choice>';
  const diseaseCategoryChoiceId = 'cc2.<redacted-primary-disease-type-category-choice>';
  const system = 'https://cda.readthedocs.io';

  const cases = [
    {
      name: 'integer initial NULL proposal from nativeRequests[26]',
      request: {
        snapshotToken,
        expectedDraftVersion: 3,
        expectedDraftDigest: 'sha256:<redacted-integer-initial-draft-digest>',
        outputId,
        changedStepId: stepId,
        candidateConstruction: {
          version: 1,
          steps: [{
            id: stepId,
            inputs: [{ kind: 'SOURCE_PROJECTION' }],
            operation: { kind: 'CODED_PIVOT', codedPivot: {
              constructionId: stepId,
              sourceChoiceId: integerSourceChoiceId,
              categories: [{ choiceId: integerCategoryChoiceId, outputColumnId: 'coded-column_<days_to_collection>' }],
              duplicatePolicy: 'ERROR', missingCellPolicy: 'NULL',
            } },
            outputs: [{ id: 'coded-column_<days_to_collection>', name: 'days_to_collection', label: 'Days to collection', type: 'INFER' }],
          }],
        },
        limit: 25,
      },
      expected: {
        outputId, snapshotToken, sourceChoiceId: integerSourceChoiceId, missingCellPolicy: 'NULL', stepId,
        categories: [{ system, code: 'days_to_collection', label: 'Days to collection', choiceId: integerCategoryChoiceId }],
      },
    },
    {
      name: 'integer edit ERROR proposal from nativeRequests[45]',
      request: {
        snapshotToken,
        expectedDraftVersion: 4,
        expectedDraftDigest: 'sha256:<redacted-integer-edit-draft-digest>',
        outputId,
        changedStepId: stepId,
        candidateConstruction: {
          version: 1,
          steps: [{
            id: stepId,
            inputs: [{ kind: 'SOURCE_PROJECTION' }],
            operation: { kind: 'CODED_PIVOT', codedPivot: {
              constructionId: stepId,
              sourceChoiceId: integerSourceChoiceId,
              categories: [{ system, code: 'days_to_collection', outputColumnId: 'coded-column_<days_to_collection>' }],
              duplicatePolicy: 'ERROR', missingCellPolicy: 'ERROR',
            } },
            outputs: [{ id: 'coded-column_<days_to_collection>', name: 'days_to_collection', label: 'Days to collection', type: 'INFER' }],
          }],
        },
        limit: 25,
      },
      expected: {
        outputId, snapshotToken, sourceChoiceId: integerSourceChoiceId, missingCellPolicy: 'ERROR', stepId,
        categories: [{ system, code: 'days_to_collection', label: 'Days to collection', outputColumnId: 'coded-column_<days_to_collection>' }],
      },
    },
    {
      name: 'string initial NULL proposal from nativeRequests[26]',
      request: {
        snapshotToken,
        expectedDraftVersion: 3,
        expectedDraftDigest: 'sha256:<redacted-string-initial-draft-digest>',
        outputId,
        changedStepId: stepId,
        candidateConstruction: {
          version: 1,
          steps: [{
            id: stepId,
            inputs: [{ kind: 'SOURCE_PROJECTION' }],
            operation: { kind: 'CODED_PIVOT', codedPivot: {
              constructionId: stepId,
              sourceChoiceId: stringSourceChoiceId,
              categories: [
                { choiceId: specimenCategoryChoiceId, outputColumnId: 'coded-column_<specimen_type>' },
                { choiceId: diseaseCategoryChoiceId, outputColumnId: 'coded-column_<primary_disease_type>' },
              ],
              duplicatePolicy: 'ERROR', missingCellPolicy: 'NULL',
            } },
            outputs: [
              { id: 'coded-column_<specimen_type>', name: 'specimen_type', label: 'Specimen type', type: 'INFER' },
              { id: 'coded-column_<primary_disease_type>', name: 'primary_disease_type', label: 'Primary disease type', type: 'INFER' },
            ],
          }],
        },
        limit: 25,
      },
      expected: {
        outputId, snapshotToken, sourceChoiceId: stringSourceChoiceId, missingCellPolicy: 'NULL', stepId,
        categories: [
          { system, code: 'specimen_type', label: 'Specimen type', choiceId: specimenCategoryChoiceId },
          { system, code: 'primary_disease_type', label: 'Primary disease type', choiceId: diseaseCategoryChoiceId },
        ],
      },
    },
    {
      name: 'string edit ERROR proposal from nativeRequests[45]',
      request: {
        snapshotToken,
        expectedDraftVersion: 4,
        expectedDraftDigest: 'sha256:<redacted-string-edit-draft-digest>',
        outputId,
        changedStepId: stepId,
        candidateConstruction: {
          version: 1,
          steps: [{
            id: stepId,
            inputs: [{ kind: 'SOURCE_PROJECTION' }],
            operation: { kind: 'CODED_PIVOT', codedPivot: {
              constructionId: stepId,
              sourceChoiceId: stringSourceChoiceId,
              categories: [
                { system, code: 'specimen_type', outputColumnId: 'coded-column_<specimen_type>' },
                { system, code: 'primary_disease_type', outputColumnId: 'coded-column_<primary_disease_type>' },
              ],
              duplicatePolicy: 'ERROR', missingCellPolicy: 'ERROR',
            } },
            outputs: [
              { id: 'coded-column_<specimen_type>', name: 'specimen_type', label: 'Specimen type', type: 'INFER' },
              { id: 'coded-column_<primary_disease_type>', name: 'primary_disease_type', label: 'Primary disease type', type: 'INFER' },
            ],
          }],
        },
        limit: 25,
      },
      expected: {
        outputId, snapshotToken, sourceChoiceId: stringSourceChoiceId, missingCellPolicy: 'ERROR', stepId,
        categories: [
          { system, code: 'specimen_type', label: 'Specimen type', outputColumnId: 'coded-column_<specimen_type>' },
          { system, code: 'primary_disease_type', label: 'Primary disease type', outputColumnId: 'coded-column_<primary_disease_type>' },
        ],
      },
    },
  ];

  for (const { name, request, expected } of cases) {
    assert.equal(codedPivotProposalRequestMatches(request, expected), true, name);
    assert.equal(codedPivotProposalRequestMatches({ ...request, outputId: 'out_wrong' }, expected), false, `${name}: output binding`);
    assert.equal(codedPivotProposalRequestMatches({ ...request, snapshotToken: 'sha256:wrong' }, expected), false, `${name}: snapshot binding`);
    assert.equal(codedPivotProposalRequestMatches(request, { ...expected, sourceChoiceId: 'cc2.<wrong-choice>' }), false, `${name}: source choice`);
    assert.equal(codedPivotProposalRequestMatches(request, { ...expected, missingCellPolicy: expected.missingCellPolicy === 'NULL' ? 'ERROR' : 'NULL' }), false,
      `${name}: missing-cell policy`);

    const wrongOutput = structuredClone(request);
    wrongOutput.candidateConstruction.steps[0].outputs[0].label = 'Swapped output';
    assert.equal(codedPivotProposalRequestMatches(wrongOutput, expected), false, `${name}: category-to-output association`);
    const wrongChangedStep = { ...request, changedStepId: 'coded-pivot_other-step' };
    assert.equal(codedPivotProposalRequestMatches(wrongChangedStep, expected), false, `${name}: changed step`);
  }

  const integerInitial = cases[0];
  const extraStep = structuredClone(integerInitial.request);
  extraStep.candidateConstruction.steps.push(structuredClone(extraStep.candidateConstruction.steps[0]));
  assert.equal(codedPivotProposalRequestMatches(extraStep, integerInitial.expected), false, 'A proposal must contain exactly one CODED_PIVOT step.');
  const wrongInputs = structuredClone(integerInitial.request);
  wrongInputs.candidateConstruction.steps[0].inputs = [];
  assert.equal(codedPivotProposalRequestMatches(wrongInputs, integerInitial.expected), false, 'The native source-projection input is part of the proposal contract.');
});

test('coded Pivot policy replacement admits only the retained NULL abort paired with a successful exact ERROR proposal', () => {
  // Reduced literal nativeRequests[59] and [60] from the retained integer report. The run-scoped
  // snapshot/draft/source choice and IDs are redacted aliases; pair equality and policy are preserved.
  // Report SHA-256: 674aa41a68795aebf7dcfaa2b30d1959edb8ca202a1388a8fb94452ba3a17035.
  // The report records action 15 "Set missing value policy" at 56 ms; its absolute action timestamp
  // is not retained, so request times are kept as offsets from the report's old-request start.
  const origin = 'http://127.0.0.1:30008';
  const explorerId = 'qa-reshape-coded-pivot-integer-4dd6c5b0-a8c9-4f91-ab0c-37fde1622';
  const path = `/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}/authoring/v2/construction-proposals`;
  const snapshotToken = 'sha256:<redacted-run-snapshot>';
  const draftDigest = 'sha256:<redacted-run-draft-digest>';
  const outputId = 'out_<retained-run-output>';
  const stepId = 'coded-pivot_<retained-run-step>';
  const sourceChoiceId = 'cc2.<redacted-run-frame-choice>';
  const columnId = 'coded-column_<days_to_collection>';
  const actionLabel = 'Set missing value policy';
  const reason = 'Selecting ERROR superseded the exact in-flight NULL candidate for this coded Pivot edit.';
  const categories = [{
    system: 'https://cda.readthedocs.io',
    code: 'days_to_collection',
    label: 'Days to collection',
    outputColumnId: columnId,
  }];
  const proof = {
    contract: 'coded-pivot-policy-replacement', actionLabel, mode: 'integer',
    project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', explorerId,
    outputId, snapshotToken, draftVersion: 4, draftDigest, stepId, sourceChoiceId,
    categories, fromPolicy: 'NULL', toPolicy: 'ERROR',
  };
  const expected = { origin, path, ...proof, reason };
  const policyAction = { label: actionLabel, startedAt: 100, completedAt: 156 };
  const retainedBody = {
    snapshotToken,
    expectedDraftVersion: 4,
    expectedDraftDigest: draftDigest,
    outputId,
    changedStepId: stepId,
    candidateConstruction: {
      version: 1,
      steps: [{
        id: stepId,
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'CODED_PIVOT', codedPivot: {
          constructionId: stepId,
          sourceChoiceId,
          categories: [{ system: categories[0].system, code: categories[0].code, outputColumnId: columnId }],
          duplicatePolicy: 'ERROR',
          missingCellPolicy: 'NULL',
        } },
        outputs: [{ id: columnId, name: 'days_to_collection', label: 'Days to collection', type: 'INFER' }],
      }],
    },
    limit: 25,
  };
  const canceledRequest = {
    origin, path, method: 'POST', browserRequestId: 'playwright-60',
    requestId: 'construction-proposal-5e7d9cde-78fe-4f52-9334-f1acdfaddafb',
    startedAt: 108, completedAt: 578, failure: 'net::ERR_ABORTED', body: structuredClone(retainedBody),
  };
  const replacementBody = structuredClone(retainedBody);
  replacementBody.candidateConstruction.steps[0].operation.codedPivot.missingCellPolicy = 'ERROR';
  const replacementRequest = {
    origin, path, method: 'POST', browserRequestId: 'playwright-61',
    requestId: 'construction-proposal-4dfe0e7b-7937-4f3d-a593-f2f71376d9a9',
    startedAt: 861, responseReceivedAt: 1321, completedAt: 1349, status: 200,
    body: replacementBody,
    response: {
      proposalId: 'receipt_<retained-replacement-proposal>',
      previewStatus: 'READY', outputId, snapshotToken,
    },
  };
  const cancellation = {
    requestId: canceledRequest.requestId,
    method: 'POST',
    url: `${origin}${path}`,
    reason,
    proof: {
      ...proof,
      scopeAction: actionLabel,
      scopeRequest: { requestId: canceledRequest.requestId, draftVersion: 4, draftDigest, outputId, stageId: null },
    },
  };
  const expectedCancellations = [cancellation];
  const evidence = (requests = [canceledRequest, replacementRequest], replacements = expectedCancellations,
    action = policyAction, expectedProof = expected) => codedPivotPolicyReplacementCancellationEvidenceFor({
      requests, expectedCancellations: replacements, replacementRequest, policyAction: action, expected: expectedProof,
    });

  const matched = evidence();
  assert.equal(matched.status, 'matched-cancellation');
  assert.equal(matched.canceledRequestId, canceledRequest.requestId);
  assert.equal(matched.replacementRequestId, replacementRequest.requestId);
  assert.equal(matched.canceledAt, 578);
  assert.equal(matched.replacementStartedAt, 861);

  const noAbort = structuredClone(canceledRequest);
  delete noAbort.failure;
  noAbort.status = 200;
  noAbort.completedAt = 500;
  assert.equal(evidence([noAbort, replacementRequest], []).status, 'no-cancellation',
    'If the prior NULL proposal completes normally, the exact ERROR proposal remains valid without a waiver.');

  for (const wrongIdentity of [
    { project: 'loom_dev_other' },
    { generation: 'other-generation' },
    { explorerId: 'other-explorer' },
    { outputId: 'out_other' },
    { snapshotToken: 'sha256:other-snapshot' },
    { draftVersion: 5 },
    { draftDigest: 'sha256:other-digest' },
    { stepId: 'coded-pivot_other' },
    { sourceChoiceId: 'cc2.other-choice' },
  ]) {
    assert.equal(evidence([canceledRequest, replacementRequest], expectedCancellations, policyAction,
      { ...expected, ...wrongIdentity }).status, 'invalid', `wrong binding ${Object.keys(wrongIdentity)[0]} is rejected`);
  }

  const wrongPath = structuredClone(replacementRequest);
  wrongPath.path = path.replace(explorerId, 'other-explorer');
  assert.equal(evidence([canceledRequest, wrongPath]).status, 'invalid', 'replacement must use the created Explorer path');
  const failedReplacement = structuredClone(replacementRequest);
  failedReplacement.status = 500;
  assert.equal(evidence([canceledRequest, failedReplacement]).status, 'invalid', 'failed replacement is never waived');
  const wrongPolicyAbort = structuredClone(canceledRequest);
  wrongPolicyAbort.body.candidateConstruction.steps[0].operation.codedPivot.missingCellPolicy = 'ERROR';
  assert.equal(evidence([wrongPolicyAbort, replacementRequest]).status, 'invalid', 'only the NULL candidate can be the superseded request');
  const outsidePolicyWindow = structuredClone(canceledRequest);
  outsidePolicyWindow.completedAt = 50;
  assert.equal(evidence([outsidePolicyWindow, replacementRequest]).status, 'invalid', 'an abort before action 15 is not classified');
  const lateAbort = structuredClone(canceledRequest);
  lateAbort.completedAt = replacementRequest.startedAt;
  assert.equal(evidence([lateAbort, replacementRequest]).status, 'invalid', 'an abort after replacement start is not classified');
  const duplicateAbort = { ...structuredClone(canceledRequest), requestId: 'construction-proposal-duplicate' };
  assert.equal(evidence([canceledRequest, duplicateAbort, replacementRequest]).status, 'invalid', 'duplicate candidate aborts remain fatal');
  assert.equal(evidence([canceledRequest, replacementRequest], [...expectedCancellations, structuredClone(cancellation)]).status, 'invalid',
    'duplicate cancellation ledger entries remain fatal');

  const unmarkedSummary = summarizeCodedPivotNativeRequests([canceledRequest]);
  assert.equal(unmarkedSummary.passed, false, 'An abort is fatal before the pair matcher validates it.');
  const markedRequest = {
    ...canceledRequest,
    expectedCancellation: { contract: 'coded-pivot-policy-replacement', requestId: canceledRequest.requestId },
  };
  assert.equal(summarizeCodedPivotNativeRequests([markedRequest], {
    acceptedExpectedCancellationRequestIds: [canceledRequest.requestId],
  }).passed, true);
  assert.throws(() => summarizeCodedPivotNativeRequests([{ ...canceledRequest, expectedCancellation: undefined }], {
    acceptedExpectedCancellationRequestIds: [canceledRequest.requestId],
  }), /validated policy-replacement marker/);
});

test('coded Pivot policy request window starts before editor open and binds the retained NULL abort', async () => {
  // Retained bounded runs prove the ordering and native cancellation contract. Integer report:
  // /private/tmp/loom-verification-brackets/coded-pivot-integer-d4fe-retry/run-standalone-reshape-coded-pivot-coded-pivot-integer-ggeOwY/playwright-results/standalone-reshape-standal-1295a-integer-coded-pivot-integer/cda-report.json
  // SHA-256 accee4affacc0cb1a73bd0ccee0d6c889a6f487856889365187716661e82f06a (NULL [44], ERROR [45]);
  // string report:
  // /private/tmp/loom-verification-brackets/coded-pivot-string-d4fe-retry/run-standalone-reshape-coded-pivot-coded-pivot-string-0Z8u9F/playwright-results/standalone-reshape-standal-3ee65-t-string-coded-pivot-string/cda-report.json
  // SHA-256 00fbdca5f02c1f49f9cd5cc16ee8f121345d943b113d0020162969db7b05ca86 (NULL [59], ERROR [60]).
  // Persisted values below are reduced literals; run-scoped snapshot/draft/choice/step/request IDs are stand-ins.
  const scenarios = [
    {
      mode: 'integer', candidateIndex: 44, replacementIndex: 45, actionLabel: 'Set missing value policy',
      explorerId: 'qa-reshape-coded-pivot-integer-retained-run',
      outputId: 'out_<retained-integer-output>', stepId: 'coded-pivot_<retained-integer-step>',
      sourceChoiceId: 'cc2.<retained-integer-source-choice>', draftDigest: `sha256:${'1'.repeat(64)}`,
      categories: [{ system: 'https://cda.readthedocs.io', code: 'days_to_collection', label: 'Days to collection',
        outputColumnId: 'coded-column_<days_to_collection>' }],
      nullRequestId: 'construction-proposal-retained-integer-null',
      errorRequestId: 'construction-proposal-retained-integer-error',
    },
    {
      mode: 'string', candidateIndex: 59, replacementIndex: 60, actionLabel: 'Set missing value policy after Cancel',
      explorerId: 'qa-reshape-coded-pivot-string-retained-run',
      outputId: 'out_<retained-string-output>', stepId: 'coded-pivot_<retained-string-step>',
      sourceChoiceId: 'cc2.<retained-string-source-choice>', draftDigest: `sha256:${'2'.repeat(64)}`,
      categories: [
        { system: 'https://cda.readthedocs.io', code: 'specimen_type', label: 'Specimen type',
          outputColumnId: 'coded-column_<specimen_type>' },
        { system: 'https://cda.readthedocs.io', code: 'primary_disease_type', label: 'Primary disease type',
          outputColumnId: 'coded-column_<primary_disease_type>' },
      ],
      nullRequestId: 'construction-proposal-retained-string-null',
      errorRequestId: 'construction-proposal-retained-string-error',
    },
  ];
  const origin = 'http://127.0.0.1:30008';
  const project = 'loom_dev_cda_fhir';
  const generation = 'cda-fhir-v1';
  const snapshotToken = `sha256:${'a'.repeat(64)}`;
  const reason = 'Selecting ERROR superseded the exact in-flight NULL candidate for this coded Pivot edit.';

  const originalDateNow = Date.now;
  let now = 1_000;
  Date.now = () => now;
  try {
    for (const scenario of scenarios) {
      const {
        mode, candidateIndex, replacementIndex, actionLabel, explorerId, outputId, stepId, sourceChoiceId,
        draftDigest, categories, nullRequestId, errorRequestId,
      } = scenario;
      const path = `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/construction-proposals`;
      const page = new EventEmitter();
      const report = { nativeRequests: [], errors: [] };
      const requestFailures = new WeakMap();
      const capture = captureCDARequests(page, {
        apiOrigin: origin,
        ownedPathPrefix: `/api/v1/projects/${project}/explorers/${explorerId}`,
        report,
        responsePaths: /construction-proposals/,
      });
      const trackers = new Set([capture]);

      const requestFor = ({ requestId, requestPath = path, body }) => {
        let observedFailure = null;
        const request = {
          url: () => `${origin}${requestPath}`,
          method: () => 'POST',
          headers: () => ({ 'x-request-id': requestId }),
          postData: () => JSON.stringify(body),
          failure: () => observedFailure,
        };
        page.emit('request', request);
        return {
          request,
          entry: capture.byRequest.get(request),
          respond(status, responseBody) {
            page.emit('response', {
              request: () => request,
              status: () => status,
              headers: () => ({}),
              text: async () => JSON.stringify(responseBody),
            });
          },
          finish() { page.emit('requestfinished', request); },
          abort() {
            observedFailure = { errorText: 'net::ERR_ABORTED' };
            const entry = capture.byRequest.get(request);
            requestFailures.set(request, {
              errorText: 'net::ERR_ABORTED', method: request.method(), url: request.url(),
              requestId: entry.requestId, playwrightRequestId: `cda-request-${entry.browserRequestId}`,
            });
            page.emit('requestfailed', request);
          },
        };
      };
      const bodyFor = missingCellPolicy => ({
        snapshotToken,
        expectedDraftVersion: 4,
        expectedDraftDigest: draftDigest,
        outputId,
        changedStepId: stepId,
        candidateConstruction: {
          version: 1,
          steps: [{
            id: stepId,
            inputs: [{ kind: 'SOURCE_PROJECTION' }],
            operation: { kind: 'CODED_PIVOT', codedPivot: {
              constructionId: stepId,
              sourceChoiceId,
              categories: categories.map(({ system: categorySystem, code, outputColumnId }) => ({
                system: categorySystem, code, outputColumnId,
              })),
              duplicatePolicy: 'ERROR', missingCellPolicy,
            } },
            outputs: categories.map(({ code, label, outputColumnId }) => ({
              id: outputColumnId, name: code, label, type: 'INFER',
            })),
          }],
        },
        limit: 25,
      });

      // An unrelated earlier proposal abort is captured and classified under another action. It
      // must stay outside the current window and must not consume the current action's NULL match.
      const earlierAbort = requestFor({ requestId: 'construction-proposal-prior-action-null', body: bodyFor('NULL') });
      earlierAbort.abort();
      const earlierCancellation = classifyExpectedCdaCancellation({
        request: earlierAbort.request,
        reason: 'The earlier policy action retired its own proposal.',
        proof: { contract: 'coded-pivot-policy-replacement', actionLabel: 'Earlier policy action' },
        report,
        requestFailures,
        trackers,
      });
      assert.equal(earlierCancellation.browserRequestId, 'playwright-1');

      // Prepopulate through the real collector so the retained array indices and browser IDs line up.
      for (let index = 1; index < candidateIndex; index += 1) {
        now += 1;
        const prior = requestFor({
          requestId: `owned-prior-${index}`,
          requestPath: `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/commands`,
          body: {},
        });
        prior.respond(204, {});
        prior.finish();
      }
      await capture.flush({ waitForNativeRequestTerminals: true });
      const policyWindowStart = report.nativeRequests.length; // Capture immediately before opening the editor.
      assert.equal(policyWindowStart, candidateIndex, `${mode}: retained proposal index boundary`);

      const nullCandidate = requestFor({ requestId: nullRequestId, body: bodyFor('NULL') });
      assert.equal(report.nativeRequests.indexOf(nullCandidate.entry), candidateIndex,
        `${mode}: opening the editor starts the retained NULL proposal at its recorded index`);
      assert.equal(nullCandidate.entry.browserRequestId, `playwright-${candidateIndex + 1}`,
        `${mode}: captured browser request ID follows the retained native index`);
      now += 10;
      const policyActionStartedAt = now + 10;
      now += 10;
      assert(nullCandidate.entry.startedAt < policyActionStartedAt,
        `${mode}: the NULL proposal begins before the exact policy action`);

      const proof = {
        contract: 'coded-pivot-policy-replacement', actionLabel, mode, project, generation, explorerId,
        outputId, snapshotToken, draftVersion: 4, draftDigest, stepId, sourceChoiceId, categories,
        fromPolicy: 'NULL', toPolicy: 'ERROR',
        scopeAction: actionLabel,
        scopeRequest: { requestId: nullCandidate.entry.requestId, draftVersion: 4, draftDigest, outputId, stageId: null },
      };
      now += 10;
      nullCandidate.abort();
      const cancellation = classifyExpectedCdaCancellation({
        request: nullCandidate.request,
        reason,
        proof,
        report,
        requestFailures,
        trackers,
      });
      now += 10;
      const replacement = requestFor({ requestId: errorRequestId, body: bodyFor('ERROR') });
      assert.equal(report.nativeRequests.indexOf(replacement.entry), replacementIndex,
        `${mode}: the ERROR replacement follows at its retained native request index`);
      assert.equal(replacement.entry.browserRequestId, `playwright-${replacementIndex + 1}`,
        `${mode}: replacement browser request ID follows the retained native index`);
      assert(replacement.entry.startedAt >= policyActionStartedAt);
      now += 5;
      const policyActionCompletedAt = Date.now();
      now += 5;
      replacement.respond(200, {
        proposalId: `receipt_<retained-${mode}-replacement>`, previewStatus: 'READY', outputId, snapshotToken,
      });
      now += 1;
      replacement.finish();
      now += 10;
      await capture.flush({ waitForNativeRequestTerminals: true });
      const policyAction = { label: actionLabel, startedAt: policyActionStartedAt, completedAt: policyActionCompletedAt };

      const expected = {
        origin, path, project, generation, explorerId, mode, outputId, snapshotToken,
        draftVersion: 4, draftDigest, stepId, sourceChoiceId, categories,
        fromPolicy: 'NULL', toPolicy: 'ERROR', actionLabel, reason,
      };
      const windowRequests = report.nativeRequests.slice(policyWindowStart);
      const evidence = (requests = windowRequests, cancellations = report.expectedCancellations,
        selectedReplacement = replacement.entry, action = policyAction, expectedProof = expected) =>
        codedPivotPolicyReplacementCancellationEvidenceFor({
          requests, expectedCancellations: cancellations ?? [], replacementRequest: selectedReplacement,
          policyAction: action, expected: expectedProof,
        });

      assert.equal(cancellation.requestId, nullCandidate.entry.requestId);
      assert.equal(cancellation.browserRequestId, nullCandidate.entry.browserRequestId);
      assert.equal(cancellation.method, 'POST');
      assert.equal(cancellation.url, `${origin}${path}`);
      assert.equal(cancellation.proof.actionLabel, actionLabel);
      assert.equal(cancellation.proof.scopeAction, actionLabel);
      assert.equal(nullCandidate.entry.failure, 'net::ERR_ABORTED');
      assert.equal(nullCandidate.entry.completedAt > policyAction.startedAt, true);
      const matched = evidence();
      assert.equal(matched.status, 'matched-cancellation', `${mode}: captured pre-action request and exact action proof`);
      assert.equal(matched.canceledRequestId, nullRequestId);
      assert.equal(matched.replacementRequestId, errorRequestId);
      assert.equal(matched.canceledAt, nullCandidate.entry.completedAt);
      assert.equal(matched.canceledAt < matched.replacementStartedAt, true,
        `${mode}: terminal cancellation strictly precedes the replacement request`);
      assert.equal(evidence(windowRequests, report.expectedCancellations).status, 'matched-cancellation',
        `${mode}: the unrelated earlier abort is ignored outside this window/action`);
      assert.equal(evidence(report.nativeRequests, report.expectedCancellations).status, 'invalid',
        `${mode}: a broad ledger match cannot sweep in the unrelated earlier NULL abort`);

      assert.equal(evidence(report.nativeRequests.slice(policyWindowStart + 1)).status, 'invalid',
        `${mode}: the prior late slice omits the editor-open NULL proposal and is invalid`);
      const wrongAction = structuredClone(cancellation);
      wrongAction.proof.actionLabel = 'Set another policy';
      wrongAction.proof.scopeAction = 'Set another policy';
      assert.equal(evidence(windowRequests, [wrongAction]).status, 'invalid', `${mode}: wrong cancellation action is fatal`);
      const wrongRequestIdentity = structuredClone(cancellation);
      wrongRequestIdentity.requestId = 'construction-proposal_<different-request>';
      wrongRequestIdentity.proof.scopeRequest.requestId = wrongRequestIdentity.requestId;
      assert.equal(evidence(windowRequests, [wrongRequestIdentity]).status, 'invalid', `${mode}: wrong canceled request identity is fatal`);

      const noAbortCandidate = {
        ...nullCandidate.entry, failure: undefined, status: 200, completedAt: policyAction.startedAt - 1,
      };
      assert.equal(evidence([noAbortCandidate, replacement.entry], [cancellation]).status, 'invalid',
        `${mode}: a recorded cancellation without the matching native abort is fatal`);
      assert.equal(evidence([noAbortCandidate, replacement.entry], []).status, 'no-cancellation',
        `${mode}: a completed NULL candidate needs no cancellation waiver`);

      const mismatchedReplacement = structuredClone(replacement.entry);
      mismatchedReplacement.body.candidateConstruction.steps[0].operation.codedPivot.missingCellPolicy = 'NULL';
      assert.equal(evidence([nullCandidate.entry, mismatchedReplacement], [cancellation], mismatchedReplacement).status, 'invalid',
        `${mode}: a replacement with the wrong policy cannot complete the pair`);
      assert.equal(summarizeCodedPivotNativeRequests(windowRequests).passed, false,
        `${mode}: the native abort remains fatal until the evidence matcher validates and wraps it`);
      assert.throws(() => summarizeCodedPivotNativeRequests(windowRequests, {
        acceptedExpectedCancellationRequestIds: [nullCandidate.entry.requestId],
      }), /validated policy-replacement marker/,
      `${mode}: the generic classifier marker is insufficient before pair validation`);
      nullCandidate.entry.expectedCancellation = {
        contract: 'coded-pivot-policy-replacement', requestId: nullCandidate.entry.requestId,
      };
      assert.equal(summarizeCodedPivotNativeRequests(windowRequests, {
        acceptedExpectedCancellationRequestIds: [nullCandidate.entry.requestId],
      }).passed, true, `${mode}: validated cancellation marker permits the exact captured pair`);
    }
  } finally {
    Date.now = originalDateNow;
  }
});

test('coded Pivot action-after-Cancel validates its own retained NULL abort and ERROR replacement', () => {
  // Reduced nativeRequests[59]/[60] from both 78d retained reports. IDs, snapshot, digest, and signed
  // choice values are run-scoped aliases; request shape, exact action labels, policy, statuses, and
  // event offsets are retained. The absolute action start is not in cda-report.json, so request
  // offsets are measured from the NULL request start and the recorded action duration is preserved.
  // Integer report SHA-256: 7a5c967019b4cdb1d68f090eabe8f6c36d13c8695d5cdf7718efce8365018685.
  // String report SHA-256: 07d0483c5e05067fa6938799ca07cf3fbd25deaf2382302cbf0daf5e9d094e7e.
  const origin = 'http://127.0.0.1:30008';
  const scenarios = [
    {
      mode: 'integer', actionNumber: 21, actionDurationMs: 60,
      explorerId: 'qa-reshape-coded-pivot-integer-<retained-run>',
      draftDigest: 'sha256:<retained-integer-draft>', outputId: 'out_<retained-integer-output>',
      stepId: 'coded-pivot_<retained-integer-step>', sourceChoiceId: 'cc2.<retained-integer-choice>',
      nullCompletedAt: 476, errorStartedAt: 756, errorResponseReceivedAt: 1222, errorCompletedAt: 1253,
      canceledRequestId: 'construction-proposal-e63b2efb-63d0-474e-9e4f-cf818ebed4d0',
      replacementRequestId: 'construction-proposal-f83e61da-f5d5-4ae0-a7fc-5ffd91e03549',
      categories: [{ system: 'https://cda.readthedocs.io', code: 'days_to_collection',
        label: 'Days to collection', outputColumnId: 'coded-column_<days_to_collection>' }],
    },
    {
      mode: 'string', actionNumber: 22, actionDurationMs: 58,
      explorerId: 'qa-reshape-coded-pivot-string-<retained-run>',
      draftDigest: 'sha256:<retained-string-draft>', outputId: 'out_<retained-string-output>',
      stepId: 'coded-pivot_<retained-string-step>', sourceChoiceId: 'cc2.<retained-string-choice>',
      nullCompletedAt: 507, errorStartedAt: 791, errorResponseReceivedAt: 1320, errorCompletedAt: 1351,
      canceledRequestId: 'construction-proposal-091f516d-15ca-4f4c-82ba-6c6c3f3fe777',
      replacementRequestId: 'construction-proposal-c367e253-874d-470a-9a51-75db8d1ec395',
      categories: [
        { system: 'https://cda.readthedocs.io', code: 'specimen_type', label: 'Specimen type', outputColumnId: 'coded-column_<specimen_type>' },
        { system: 'https://cda.readthedocs.io', code: 'primary_disease_type', label: 'Primary disease type', outputColumnId: 'coded-column_<primary_disease_type>' },
      ],
    },
  ];

  for (const scenario of scenarios) {
    const {
      mode, actionNumber, actionDurationMs, explorerId, draftDigest, outputId, stepId, sourceChoiceId,
      nullCompletedAt, errorStartedAt, errorResponseReceivedAt, errorCompletedAt, canceledRequestId, replacementRequestId, categories,
    } = scenario;
    const snapshotToken = 'sha256:<retained-catalog-snapshot>';
    const path = `/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}/authoring/v2/construction-proposals`;
    const actionLabel = 'Set missing value policy after Cancel';
    const reason = 'Selecting ERROR after Cancel superseded the exact in-flight NULL candidate for this coded Pivot edit.';
    const proof = {
      contract: 'coded-pivot-policy-replacement', actionLabel, mode,
      project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken,
      draftVersion: 4, draftDigest, stepId, sourceChoiceId, categories,
      fromPolicy: 'NULL', toPolicy: 'ERROR',
    };
    const expected = { origin, path, ...proof, reason };
    const bodyFor = missingCellPolicy => ({
      snapshotToken,
      expectedDraftVersion: 4,
      expectedDraftDigest: draftDigest,
      outputId,
      changedStepId: stepId,
      candidateConstruction: {
        version: 1,
        steps: [{
          id: stepId,
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: { kind: 'CODED_PIVOT', codedPivot: {
            constructionId: stepId,
            sourceChoiceId,
            categories: categories.map(({ system, code, outputColumnId }) => ({ system, code, outputColumnId })),
            duplicatePolicy: 'ERROR',
            missingCellPolicy,
          } },
          outputs: categories.map(({ code, label, outputColumnId }) => ({ id: outputColumnId, name: code, label, type: 'INFER' })),
        }],
      },
    });
    const canceledRequest = {
      origin, path, method: 'POST', browserRequestId: 'playwright-60', requestId: canceledRequestId,
      startedAt: 0, completedAt: nullCompletedAt, failure: 'net::ERR_ABORTED', body: bodyFor('NULL'),
    };
    const replacementRequest = {
      origin, path, method: 'POST', browserRequestId: 'playwright-61', requestId: replacementRequestId,
      startedAt: errorStartedAt, responseReceivedAt: errorResponseReceivedAt,
      completedAt: errorCompletedAt, status: 200, body: bodyFor('ERROR'),
      response: { proposalId: 'receipt_<retained-replacement>', previewStatus: 'READY', outputId, snapshotToken },
    };
    const cancellation = {
      requestId: canceledRequestId, method: 'POST', url: `${origin}${path}`, reason,
      proof: {
        ...proof,
        scopeAction: actionLabel,
        scopeRequest: { requestId: canceledRequestId, draftVersion: 4, draftDigest, outputId, stageId: null },
      },
    };
    const expectedCancellations = [cancellation];
    const policyAction = { label: actionLabel, startedAt: 0, completedAt: actionDurationMs };
    const matched = codedPivotPolicyReplacementCancellationEvidenceFor({
      requests: [canceledRequest, replacementRequest], expectedCancellations, replacementRequest, policyAction, expected,
    });
    assert.equal(matched.status, 'matched-cancellation', `${mode} action ${actionNumber}`);
    assert.equal(matched.canceledRequestId, canceledRequestId);
    assert.equal(matched.replacementRequestId, replacementRequestId);
    assert.equal(matched.canceledAt, nullCompletedAt);
    assert.equal(matched.replacementStartedAt, errorStartedAt);

    const firstActionLabel = 'Set missing value policy';
    const firstAction = { label: firstActionLabel, startedAt: 0, completedAt: actionDurationMs };
    const firstActionExpected = {
      ...expected,
      actionLabel: firstActionLabel,
      reason: 'Selecting ERROR superseded the exact in-flight NULL candidate for this coded Pivot edit.',
    };
    const nullCompleted = { ...canceledRequest, startedAt: 1, completedAt: 20, failure: undefined, status: 200 };
    const firstErrorProposal = {
      ...replacementRequest,
      startedAt: actionDurationMs + 1,
      responseReceivedAt: actionDurationMs + 30,
      completedAt: actionDurationMs + 50,
    };
    assert.equal(codedPivotPolicyReplacementCancellationEvidenceFor({
      requests: [nullCompleted, firstErrorProposal],
      expectedCancellations,
      replacementRequest: firstErrorProposal,
      policyAction: firstAction,
      expected: firstActionExpected,
    }).status, 'no-cancellation', 'The later action cancellation ledger must not contaminate the earlier policy action.');

    const wrongActionRecord = structuredClone(cancellation);
    wrongActionRecord.proof.actionLabel = firstActionLabel;
    assert.equal(codedPivotPolicyReplacementCancellationEvidenceFor({
      requests: [canceledRequest, replacementRequest],
      expectedCancellations: [wrongActionRecord], replacementRequest, policyAction, expected,
    }).status, 'invalid', 'An abort marked for another policy action remains fatal.');
  }
});

test('coded Pivot editor disposal consumes only an exact cloned navigation-away proof', () => {
  // Reduced from bounded-coded-pivot-string.json in the 1XP3OL run, request playwright-78;
  // report SHA-256 07d0483c5e05067fa6938799ca07cf3fbd25deaf2382302cbf0daf5e9d094e7e.
  // The original artifact records bodyNotRead; this reduced request/event chronology is used only
  // to validate the cancellation contract, not to claim a current-source lifecycle pass.
  const origin = 'http://127.0.0.1:30008';
  const project = 'loom_dev_cda_fhir';
  const generation = 'cda-fhir-v1';
  const explorerId = 'qa-reshape-coded-pivot-string-73bad3fe-b944-497d-8ad1-c344fb0a87';
  const selectionId = 'selection_c69336fc8f401f4657365e6437dc5ed4eaa6819362f300f0eb8b8c71d0182dbd';
  const outputId = 'out_f775dacbb10ef7b3e5c0395c';
  const snapshotToken = 'sha256:7e9a025e<retained-string-snapshot>';
  const sourceChoiceId = 'cc2.<retained-string-source-choice>';
  const path = `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/semantic-inventory`;
  const action = { label: 'Back to table', startedAt: 1791529413900, completedAt: 1791529413950 };
  const reason = 'The Back to table action retired this exact Coded Pivot semantic-inventory response-body read.';
  const expected = {
    origin, path, project, generation, explorerId, selectionId, mode: 'string', outputId, snapshotToken,
    sourceChoiceId, rowRoot: 'Observation', limit: 50, actionLabel: action.label, reason,
  };
  const proof = {
    contract: 'coded-pivot-editor-disposal', actionLabel: action.label, mode: 'string', project, generation,
    explorerId, selectionId, outputId, snapshotToken, sourceChoiceId, rowRoot: 'Observation', limit: 50,
  };
  const fixtureCancellation = {
    requestId: 'cda-request-633', playwrightRequestId: 'cda-request-633', browserRequestId: 'playwright-78',
    method: 'POST', url: `${origin}${path}`, reason,
    proof: { ...proof, scopeAction: action.label, scopeRequest: {
      requestId: null, draftVersion: null, draftDigest: null, outputId, stageId: null,
    } },
  };
  const entry = {
    requestId: 'playwright-78', browserRequestId: 'playwright-78', ownerPageId: 'playwright-page-1',
    ownerPageUrlAtRequest: `${origin}/?project=${project}&explorer=${explorerId}&mode=builder&selection=${selectionId}`,
    rawURL: `${origin}${path}`, origin, path, method: 'POST', query: {},
    startedAt: 1791529413647,
    body: { snapshotToken, rowRoot: 'Observation', sourceChoiceId, outputId, limit: 50 },
    nativeEventChronology: [
      { event: 'request', browserRequestId: 'playwright-78', observedAt: 1791529413647, objectMatch: true },
      { event: 'response', browserRequestId: 'playwright-78', observedAt: 1791529413918, objectMatch: true },
      { event: 'requestfailed', browserRequestId: 'playwright-78', observedAt: 1791529413929, objectMatch: true },
    ],
    status: 200, responseReceivedAt: 1791529413918, completedAt: 1791529413940,
    failure: 'net::ERR_ABORTED',
    responseReadError: 'response.text: Protocol error (Network.getResponseBody): No data found for resource with given identifier\nResponse body is not available for a response that was navigated away from. Read response.body() before triggering any navigation.',
  };
  const fixtureFailure = {
    errorText: 'net::ERR_ABORTED', expected: true, cancellationAction: action.label,
    triggerAction: action.label, failureAction: { label: action.label }, playwrightRequestId: 'cda-request-633',
    browserRequestId: 'playwright-78',
    requestScope: { expectedProject: project, generation, configuredExplorer: explorerId,
      requestProject: project, requestExplorer: explorerId },
    expectedCancellation: fixtureCancellation,
  };
  const candidate = (overrides = {}) => codedPivotEditorDisposalCancellationEvidenceFor({
    requests: [entry], expectedCancellations: [fixtureCancellation], fixtureNetworkFailures: [fixtureFailure],
    request: entry, action, expected, ...overrides,
  });

  const beforeFinalization = candidate();
  assert.equal(beforeFinalization.status, 'verified-retirement-candidate');
  const finalized = candidate({ finalized: true });
  assert.equal(finalized.status, 'matched-cancellation');
  assert.equal(finalized.requestId, entry.requestId);
  assert.equal(finalized.browserRequestId, fixtureCancellation.browserRequestId);
  assert.equal(finalized.responseReadError, entry.responseReadError);

  const localCancellation = {
    contract: 'coded-pivot-editor-disposal', requestId: entry.requestId,
    browserRequestId: entry.browserRequestId, method: entry.method, url: `${origin}${path}`, reason,
    proof: fixtureCancellation.proof, fixtureCancellation,
  };
  const locallyClassified = { ...entry, expected: true, canceled: true, expectedCancellation: localCancellation };
  const summary = summarizeCodedPivotNativeRequests([locallyClassified], {
    acceptedEditorDisposalRequestIds: [entry.requestId],
  });
  assert.equal(summary.passed, true);
  assert.equal(summary.expectedEditorDisposalCancellations.length, 1);

  const wrongAction = structuredClone(fixtureFailure);
  wrongAction.cancellationAction = 'Apply coded Pivot';
  assert.equal(candidate({ fixtureNetworkFailures: [wrongAction] }).status, 'invalid');
  assert.equal(candidate({ expected: { ...expected, outputId: 'different-output' } }).status, 'invalid');
  assert.equal(candidate({ expected: { ...expected, explorerId: 'different-explorer' } }).status, 'invalid');
  assert.equal(candidate({ expected: { ...expected, sourceChoiceId: 'different-choice' } }).status, 'invalid');
  assert.equal(candidate({ action: { ...action, startedAt: 1791529413930 } }).status, 'invalid',
    'The captured request may start before Back to table, but its terminal abort must be inside that action window.');
  assert.equal(candidate({ requests: [entry, { ...entry, browserRequestId: 'playwright-79' }] }).status, 'invalid',
    'A duplicate aborted semantic-inventory request remains fatal.');
  assert.equal(candidate({ requests: [entry, { ...entry, requestId: 'playwright-79', browserRequestId: 'playwright-79', failure: 'net::ERR_FAILED' }] }).status, 'invalid',
    'An extra non-abort failure remains fatal.');
  const wrongBodyRead = { ...entry, responseReadError: 'response.text: unrelated transport failure' };
  assert.equal(candidate({ requests: [wrongBodyRead], request: wrongBodyRead }).status, 'invalid');
  assert.throws(() => summarizeCodedPivotNativeRequests([{ ...locallyClassified,
    responseReadError: 'response.text: unrelated transport failure' }], {
    acceptedEditorDisposalRequestIds: [entry.requestId],
  }), /exact editor-disposal marker/);

  const normalIntegerRequest = { ...entry, requestId: 'playwright-integer', browserRequestId: 'playwright-integer',
    status: 200, failure: undefined, responseReadError: undefined, completedAt: action.completedAt };
  assert.equal(codedPivotEditorDisposalCancellationEvidenceFor({
    requests: [normalIntegerRequest], expectedCancellations: [], fixtureNetworkFailures: [],
    request: undefined, action, expected: { ...expected, mode: 'integer' },
  }).status, 'no-cancellation', 'A no-abort integer edit must not require a disposal candidate.');
});

test('coded Pivot removal matcher accepts the UI removal-only request and empty source-base construction', () => {
  // BuilderWorkspace.removeConstructionStep filters the current steps, sets removeStepIds:[stepId], and omits changedStepId.
  // The retained prePivotSource.document has no `construction` key, so workflow fallback is the literal empty base below.
  // useConstructionLifecycle.unit.test.tsx already proves removal-only transport; this is not a retained native removal request.
  const prePivotConstruction = { version: 1, steps: [] };
  const request = {
    outputId: 'out_<redacted-run-output>',
    snapshotToken: 'sha256:<redacted-run-snapshot>',
    candidateConstruction: prePivotConstruction,
    removeStepIds: ['coded-pivot_<redacted-run-step>'],
  };
  const expected = {
    outputId: 'out_<redacted-run-output>',
    snapshotToken: 'sha256:<redacted-run-snapshot>',
    removedStepId: 'coded-pivot_<redacted-run-step>',
    candidateConstruction: { version: 1, steps: [] },
  };

  assert.equal(codedPivotRemovalRequestMatches(request, expected), true);
  assert.equal(codedPivotRemovalRequestMatches({ ...request, changedStepId: expected.removedStepId }, expected), false,
    'Removal must not send changedStepId.');
  assert.equal(codedPivotRemovalRequestMatches({ ...request, removeStepIds: ['different-step'] }, expected), false);
  assert.equal(codedPivotRemovalRequestMatches({ ...request, outputId: 'out_wrong' }, expected), false);
  assert.equal(codedPivotRemovalRequestMatches({ ...request, snapshotToken: 'sha256:wrong' }, expected), false);
  assert.equal(codedPivotRemovalRequestMatches({ ...request, candidateConstruction: { version: 1, steps: [{ operation: { kind: 'CODED_PIVOT' } }] } }, expected), false);
});

test('rendered coded values bind each exact Coding to its persisted output header and cell', () => {
  const expected = codedPivotFixtureFor('string');
  const codedStep = {
    operation: { kind: 'CODED_PIVOT', codedPivot: { categories: [
      { system: expected[0].system, code: expected[0].code, outputColumnId: 'coded-specimen' },
      { system: expected[1].system, code: expected[1].code, outputColumnId: 'coded-disease' },
    ] } },
    outputs: [
      { id: 'coded-specimen', label: 'Specimen type' },
      { id: 'coded-disease', label: 'Primary disease type' },
    ],
  };
  const rendered = {
    headers: ['OBSERVATION ID', 'SPECIMEN TYPE', 'PRIMARY DISEASE TYPE'],
    rows: [[CODED_PIVOT_OBSERVATION_ID, 'analyte', 'Ductal and lobular neoplasms']],
  };
  assert.deepEqual(codedPivotExpectedHeaderValuesFor(codedStep, expected), [
    { system: expected[0].system, code: expected[0].code, outputColumnId: 'coded-specimen', label: 'Specimen type', value: 'analyte' },
    { system: expected[1].system, code: expected[1].code, outputColumnId: 'coded-disease', label: 'Primary disease type', value: 'Ductal and lobular neoplasms' },
  ]);
  assert.deepEqual(codedPivotRenderedValuesFor(rendered, codedStep, expected).map(({ system, code, label, value }) => ({ system, code, label, value })), [
    { system: expected[0].system, code: expected[0].code, label: 'Specimen type', value: 'analyte' },
    { system: expected[1].system, code: expected[1].code, label: 'Primary disease type', value: 'Ductal and lobular neoplasms' },
  ]);
  assert.throws(() => codedPivotRenderedValuesFor({ ...rendered, rows: [[CODED_PIVOT_OBSERVATION_ID, 'Ductal and lobular neoplasms', 'analyte']] }, codedStep, expected), /specimen_type value must be "analyte" under "Specimen type"/);
});

test('coded Pivot restoration wait requires one exact source preview row in native Playwright', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  const previewMarkup = ({
    headers = ['Observation ID'],
    rows = [[CODED_PIVOT_OBSERVATION_ID]],
    tableCount = 1,
    loading = false,
    includePreview = true,
  } = {}) => `<!doctype html><html><body>
    ${includePreview ? `<div data-testid="preview-table-scroll">
      ${loading ? '<p>Loading your table…</p>' : ''}
      ${Array.from({ length: tableCount }, () => `<div role="table" aria-rowcount="${rows.length + 1}" aria-colcount="${headers.length}">
        <div role="row">${headers.map(header => `<div role="columnheader">${header}</div>`).join('')}</div>
        ${rows.map(values => `<div role="row"><button type="button" aria-label="Inspect row 1 identity">1</button>
          ${values.map(value => `<div role="cell"><div>${value}</div></div>`).join('')}
        </div>`).join('')}
      </div>`).join('')}
    </div>` : ''}
  </body></html>`;
  const ready = () => page.evaluate(codedPivotRestoredSourceRowVisible, { id: CODED_PIVOT_OBSERVATION_ID });

  await page.setContent(previewMarkup({ includePreview: false }));
  assert.equal(await ready(), false, 'A missing preview is not restoration evidence.');
  await page.setContent(previewMarkup({ rows: [], loading: true }));
  assert.equal(await ready(), false, 'The transitional loading preview is not restoration evidence.');

  await page.setContent(previewMarkup());
  assert.equal(await ready(), true, 'The exact single-row Observation preview is restoration evidence.');
  assert.equal(await page.evaluate(codedPivotRestoredSourceRowVisible, { id: 'different-observation' }), false,
    'A different requested source ID cannot satisfy the wait.');

  await page.setContent(previewMarkup({ rows: [['different-observation']] }));
  assert.equal(await ready(), false, 'A preview row with the wrong ID is rejected.');
  await page.setContent(previewMarkup({ rows: [[CODED_PIVOT_OBSERVATION_ID], ['extra-observation']] }));
  assert.equal(await ready(), false, 'An extra body row is rejected even when the expected ID is present.');
  await page.setContent(previewMarkup({ headers: ['Observation ID', 'Unexpected column'], rows: [[CODED_PIVOT_OBSERVATION_ID, 'extra']] }));
  assert.equal(await ready(), false, 'An extra header is rejected.');
  await page.setContent(previewMarkup({ tableCount: 2 }));
  assert.equal(await ready(), false, 'Multiple preview tables are rejected.');
});

test('serialized removal predicate binds readiness to the exact proposal and preview rows', () => {
  const removalPredicateFor = ({
    panelStatus = 'ready',
    proposalId = 'receipt-1',
    previewStatus = 'ready',
    previewReceiptId = 'receipt-1',
    panelText = 'Removal preview',
    rows = [{ innerText: `Observation ${CODED_PIVOT_OBSERVATION_ID}` }],
  } = {}) => {
    const calls = [];
    const panel = {
      innerText: panelText,
      getAttribute(name) {
        if (name === 'data-proposal-status') return panelStatus;
        if (name === 'data-proposal-id') return proposalId;
        return null;
      },
    };
    const preview = {
      getAttribute(name) {
        if (name === 'data-preview-status') return previewStatus;
        if (name === 'data-preview-receipt-id') return previewReceiptId;
        return null;
      },
      querySelectorAll(selector) {
        calls.push(selector);
        return selector === '[data-testid="construction-proposal-preview-row"]' ? rows : [];
      },
    };
    const predicate = runInNewContext(`(${codedPivotRemovalProposalReady.toString()})`, {
      document: {
        querySelector(selector) {
          calls.push(selector);
          if (selector === '[data-testid="construction-proposal-panel"]') return panel;
          if (selector === '[data-testid="construction-proposal-preview"]') return preview;
          return null;
        },
      },
    });
    return { evaluate: id => predicate({ id }), calls };
  };

  const readyRemoval = removalPredicateFor();
  assert.equal(readyRemoval.evaluate(CODED_PIVOT_OBSERVATION_ID), true);
  assert.equal(readyRemoval.evaluate('different-observation'), false);
  assert.deepEqual(readyRemoval.calls, [
    '[data-testid="construction-proposal-panel"]',
    '[data-testid="construction-proposal-preview"]',
    '[data-testid="construction-proposal-preview-row"]',
    '[data-testid="construction-proposal-panel"]',
    '[data-testid="construction-proposal-preview"]',
    '[data-testid="construction-proposal-preview-row"]',
  ]);
  assert.equal(removalPredicateFor({
    panelText: `Removal preview mentions ${CODED_PIVOT_OBSERVATION_ID}`,
    rows: [{ innerText: 'Different Observation ID' }],
  }).evaluate(CODED_PIVOT_OBSERVATION_ID), false, 'The panel summary cannot stand in for the preview row.');
  assert.equal(removalPredicateFor({ previewReceiptId: 'other-receipt' }).evaluate(CODED_PIVOT_OBSERVATION_ID), false,
    'The rendered preview must carry the exact proposal receipt.');
  assert.equal(removalPredicateFor({ panelStatus: 'previewing' }).evaluate(CODED_PIVOT_OBSERVATION_ID), false);
  assert.equal(removalPredicateFor({ previewStatus: 'previewing' }).evaluate(CODED_PIVOT_OBSERVATION_ID), false);
});

test('coded Pivot removal readiness follows the native sibling preview and exact receipt', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  const fixture = ({
    panelStatus = 'ready',
    proposalId = 'proposal-42',
    previewStatus = 'ready',
    previewReceiptId = 'proposal-42',
    rowId = CODED_PIVOT_OBSERVATION_ID,
    includePreview = true,
  } = {}) => `<!doctype html><html><body>
    <section data-testid="construction-proposal-panel" data-proposal-status="${panelStatus}" data-proposal-id="${proposalId}">
      <div data-testid="construction-proposal-ready"><p>Remove coded Pivot. The preview shows the resulting table.</p></div>
    </section>
    ${includePreview ? `<section aria-label="Table result">
      <div data-testid="construction-proposal-preview" data-preview-status="${previewStatus}" data-preview-receipt-id="${previewReceiptId}" data-preview-output-id="out-coded">
        <table><tbody><tr data-testid="construction-proposal-preview-row"><td>${rowId}</td></tr></tbody></table>
      </div>
    </section>` : ''}
  </body></html>`;

  await page.setContent(fixture());
  assert.equal(await page.evaluate(codedPivotRemovalProposalReady, { id: CODED_PIVOT_OBSERVATION_ID }), true,
    'A ready panel and its linked sibling preview row restore the exact source Observation.');
  assert.equal(await page.evaluate(codedPivotRemovalProposalReady, { id: 'different-observation' }), false,
    'A different Observation must not satisfy the restoration check.');

  await page.setContent(fixture({ previewReceiptId: 'other-proposal' }));
  assert.equal(await page.evaluate(codedPivotRemovalProposalReady, { id: CODED_PIVOT_OBSERVATION_ID }), false,
    'A preview from another receipt cannot satisfy the removal proposal.');

  await page.setContent(fixture({ rowId: 'different-observation' }));
  assert.equal(await page.evaluate(codedPivotRemovalProposalReady, { id: CODED_PIVOT_OBSERVATION_ID }), false,
    'The linked preview must contain the exact Observation row.');

  await page.setContent(fixture({ includePreview: false }));
  assert.equal(await page.evaluate(codedPivotRemovalProposalReady, { id: CODED_PIVOT_OBSERVATION_ID }), false,
    'The summary panel alone is not proof of the resulting table.');

  await page.setContent(fixture({ panelStatus: 'previewing' }));
  assert.equal(await page.evaluate(codedPivotRemovalProposalReady, { id: CODED_PIVOT_OBSERVATION_ID }), false);
  await page.setContent(fixture({ previewStatus: 'previewing' }));
  assert.equal(await page.evaluate(codedPivotRemovalProposalReady, { id: CODED_PIVOT_OBSERVATION_ID }), false);
});

test('first-table readiness predicate accepts the retained heading and enabled Observation choice in native Playwright', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  const capturedPage = (heading = 'Build your first table', disabled = false) => `
    <main>
      <section>
        <h2>${heading}</h2>
        <p>Choose a populated record type. Loom will add its direct ID column and load a preview. The table name is optional.</p>
        <section aria-label="Choose row type">
          <button type="button" aria-label="Choose Observation rows" ${disabled ? 'disabled' : ''}>
            <span>Observation</span><span>815,261 authorized records</span>
          </button>
        </section>
      </section>
    </main>`;
  await page.route('http://coded-pivot.test/**', route => route.fulfill({
    contentType: 'text/html',
    body: '<!doctype html><html><body></body></html>',
  }));
  const baseURL = 'http://coded-pivot.test/?project=loom_dev_cda_fhir&explorer=owned-explorer&mode=builder';
  const selectedURL = `${baseURL}&selection=owned-selection`;

  for (const url of [baseURL, selectedURL]) {
    await page.goto(url);
    await page.setContent(capturedPage());
    assert.equal(page.url(), url);
    assert.equal(await page.evaluate(codedPivotFirstTableReady), true);
    assert.equal(await page.getByRole('button', { name: 'Choose Observation rows' }).isEnabled(), true);
  }

  await page.setContent(capturedPage('Build another table'));
  assert.equal(await page.evaluate(codedPivotFirstTableReady), false);

  await page.setContent(capturedPage('Build your first table', true));
  assert.equal(await page.evaluate(codedPivotFirstTableReady), false);
  assert.equal(await page.getByRole('button', { name: 'Choose Observation rows' }).isEnabled(), false);
});

test('coded Pivot Back to table control locator follows the operation editor markup in native Playwright', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  // BuilderWorkspace.tsx around line 3102 renders this button with visible text "Back to table"
  // but aria-label="Close operation editor", which takes precedence as its accessible name.
  await page.setContent(`<!doctype html><html><body>
    <button type="button" aria-label="Close operation editor" data-testid="construction-close-operation-editor" class="rounded px-2 py-1 text-sm text-slate-500 hover:bg-slate-100">
      Back to table
    </button>
  </body></html>`);

  const control = codedPivotBackToTableControl(page);
  assert.equal(await control.count(), 1);
  assert.equal(await control.getAttribute('aria-label'), 'Close operation editor');
  assert.equal((await control.innerText()).trim(), 'Back to table');
  assert.equal(await control.isVisible(), true);
  assert.equal(await control.isEnabled(), true);
  assert.equal(await page.getByRole('button', { name: 'Back to table', exact: true }).count(), 0,
    'The visible Back to table text is not the accessible name when aria-label is present.');
  await page.evaluate(() => {
    document.body.addEventListener('click', event => {
      document.body.dataset.clickedTestId = event.target?.getAttribute('data-testid') ?? '';
    });
  });
  await control.click();
  assert.equal(await page.locator('body').getAttribute('data-clicked-test-id'), 'construction-close-operation-editor');
});

test('coded Pivot source options are inspected and selected through the native radio label', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  await page.setContent(`<!doctype html><html><head><style>.block{display:block}.text-xs{font-size:.75rem}</style></head><body>
    <section aria-label="Coded values as columns">
      <div><h3>Coded values as columns</h3><p>Keep one row per Observation record. Each selected code becomes a column filled by its paired value.</p></div>
      <label>Find a coded source<input type="search" placeholder="Search sources"></label>
      <fieldset>
        <legend>Source of coded values</legend>
        <label class="flex gap-2"><input type="radio" name="coded-pivot-source"><span><strong class="block">Observation component values (string)</strong><span class="text-xs">Codes and their paired values on Observation records.</span></span></label>
        <label class="flex gap-2"><input type="radio" name="coded-pivot-source"><span><strong class="block">Observation component values (integer)</strong><span class="text-xs">Codes and their paired values on Observation records.</span></span></label>
      </fieldset>
    </section>
  </body></html>`);

  // This is the same inspector installed as cda.inspect in cda-fixtures.mjs.
  const cda = { inspect: createCdaInspector(page) };
  const previousFailureCapture = () => {
    const sourceControls = [];
    const modeLabel = {};
    sourceControls[sourceControls.length] = modeLabel;
    return sourceControls;
  };
  await assert.rejects(cda.inspect(previousFailureCapture), /Browser inspection callbacks may inspect results only/,
    'The cda.inspect guard reproduces the mutation failure recorded in the retained bracket.');
  const sourceLabels = [
    { mode: 'integer', title: 'Observation component values (integer)' },
    { mode: 'string', title: 'Observation component values (string)' },
  ];
  const description = 'Codes and their paired values on Observation records.';

  for (const { mode, title } of sourceLabels) {
    const sources = await cda.inspect(codedPivotSourceControls);
    const matchingSource = sources.find(source => source.text?.includes(title));
    assert.ok(matchingSource, `The native chooser must render its direct ${mode} source.`);
    assert.equal(matchingSource.text, `${title}\n${description}`);
    assert.equal(matchingSource.disabled, false);

    const previousLocator = page.locator('section[aria-label="Coded values as columns"] label')
      .filter({ hasText: matchingSource.text });
    assert.equal(await previousLocator.count(), 0, 'Captured innerText line breaks do not make the old hasText locator reliable.');

    const sourceRadio = codedPivotSourceRadioFor(page, matchingSource.text);
    assert.equal(await sourceRadio.count(), 1);
    await sourceRadio.click();
    const selectedSources = await cda.inspect(codedPivotSourceControls);
    assert.equal(selectedSources.find(source => source.text === matchingSource.text)?.checked, true,
      `Selecting the ${mode} label must use native radio behavior.`);

    const domSnapshot = await cda.inspect(codedPivotFailureDomSnapshot, { mode });
    assert.equal(domSnapshot.mode, mode);
    assert.ok(domSnapshot.sourceControls.some(control => control.labelText === matchingSource.text));
  }
});

test('coded Pivot failure evidence retains the first chooser DOM and exact matching source across cleanup', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  const chooser = `
    <section aria-label="Coded values as columns">
      <label><input type="radio" name="coded-pivot-source" value="choice-integer" checked>
        <span><strong>Observation component values (integer)</strong>
        <span>Codes and their paired values on Observation records.</span></span>
      </label>
    </section>`;
  const origin = 'http://127.0.0.1:30008';
  const project = 'loom_dev_cda_fhir';
  const explorerId = 'qa-coded-pivot-integer';
  const ownedPath = `/api/v1/projects/${project}/explorers/${explorerId}`;
  const request = {
    snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7', outputId: 'out_coded_pivot_source',
    resourceType: 'Observation', limit: 50,
  };
  const endpointPath = `${ownedPath}/authoring/v2/frame-source-options`;
  const sourceOption = {
    choiceId: 'choice-integer', title: 'Observation component values (integer)',
    description: 'Codes and their paired values on Observation records.', resourceType: 'Observation',
    sourcePath: 'Observation.component', bindingId: 'component-value', owningScope: 'resource',
    keyPath: 'Observation.component.code.coding.code', valuePath: 'Observation.component.valueInteger',
    logicalType: 'integer', exampleConcept: 'Days to collection', observedOccurrences: 1, route: [],
    forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'DEFAULT' }], defaultForm: 'VALUE',
  };
  const response = {
    snapshotToken: request.snapshotToken, outputId: request.outputId, complete: true, truncated: false,
    sources: [sourceOption],
  };
  await page.route(`${origin}/**`, async route => {
    if (new URL(route.request().url()).pathname === endpointPath) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(response) });
      return;
    }
    await route.fulfill({ status: 200, contentType: 'text/html', body: `<!doctype html><html><body><main>${chooser}</main></body></html>` });
  });
  await page.goto(origin);
  const captureReport = { nativeRequests: [], errors: [] };
  const requestCapture = captureCDARequests(page, {
    apiOrigin: origin, ownedPathPrefix: ownedPath, report: captureReport, responsePaths: /frame-source-options/,
  });
  const fetchResponse = page.evaluate(async ({ path, body }) => {
    const result = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    return result.json();
  }, { path: endpointPath, body: request });
  const entry = await requestCapture.waitFor(candidate => candidate.path === endpointPath && candidate.method === 'POST' &&
    Array.isArray(requestCapture.rawResponseBody(candidate)?.sources), { timeoutMs: 5000 });
  assert.deepEqual(await fetchResponse, response);
  const capturedRequest = requestCapture.rawRequestBody(entry);
  const capturedResponse = requestCapture.rawResponseBody(entry);
  assert.deepEqual(capturedRequest, request);
  assert.deepEqual(capturedResponse, response);
  const captureOrder = [];
  const evidence = await codedPivotFirstFailureEvidenceFor({
    mode: 'integer', action: { label: 'Select integer coded source', startedAt: 10 },
    captureDom: async () => {
      captureOrder.push('dom');
      const cda = { inspect: createCdaInspector(page) };
      return cda.inspect(codedPivotFailureDomSnapshot, { mode: 'integer' });
    },
    captureSourceOptions: domSnapshot => {
      captureOrder.push('source-options');
      return codedPivotSourceOptionsDiagnosticFor(entry, capturedRequest, capturedResponse, {
        origin, project, generation: 'cda-fhir-v1', explorerId, outputId: request.outputId,
        snapshotToken: request.snapshotToken, mode: 'integer', domSnapshot,
      });
    },
  });

  assert.deepEqual(captureOrder, ['dom', 'source-options']);
  assert.ok(evidence.sourceOptions, evidence.sourceOptionsCaptureError);
  assert.equal(evidence.dom.codedPivotSectionPresent, true);
  assert.equal(evidence.dom.sourceControls[0].checked, true);
  assert.equal(evidence.dom.sourceControlCount, 1);
  assert.equal(evidence.dom.sourceControlsTruncated, false);
  assert.deepEqual(evidence.sourceOptions.candidateChoices, [sourceOption]);
  assert.deepEqual(evidence.sourceOptions.matchedChoiceIndexes, [0]);
  assert.equal(evidence.sourceOptions.target.generation, 'cda-fhir-v1');
  assert.match(evidence.sourceOptions.target.identitySource, /BuilderV2 catalog generation\/snapshot/);
  assert.match(evidence.sourceOptions.frameOwnership, /empty route, Observation resourceType/);
  assert.equal(evidence.sourceOptions.envelope.sourceCount, 1);

  await page.setContent('<!doctype html><html><body><h1>Build your first table</h1></body></html>');
  assert.equal(await page.evaluate(() => document.querySelector('section[aria-label="Coded values as columns"]') === null), true);
  assert.equal(evidence.dom.codedPivotSectionPresent, true, 'The captured action-boundary DOM must survive later cleanup navigation.');
  assert.deepEqual(evidence.sourceOptions.candidateChoices, [sourceOption]);
});

test('coded Pivot source-options diagnostics bind the owned frame and expose bounded-page truncation', () => {
  const origin = 'http://127.0.0.1:30008';
  const project = 'loom_dev_cda_fhir';
  const explorerId = 'qa-coded-pivot-integer';
  const outputId = 'out_coded_pivot_source';
  const snapshotToken = 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7';
  const path = `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/frame-source-options`;
  const request = { snapshotToken, outputId, resourceType: 'Observation', limit: 50 };
  const entry = { origin, path, method: 'POST', status: 200, completedAt: 10, body: request };
  const domSnapshot = { mode: 'integer', sourceControls: [{
    name: 'coded-pivot-source', labelText: 'Observation component values (integer) Codes and their paired values on Observation records.',
  }] };
  const option = (index, overrides = {}) => ({
    choiceId: `choice-${index}`, title: `Other numeric frame ${index}`, description: 'bounded diagnostic fixture',
    resourceType: 'Observation', sourcePath: 'Observation.component', bindingId: `binding-${index}`,
    owningScope: 'resource', keyPath: 'Observation.component.code.coding.code',
    valuePath: 'Observation.component.valueDecimal', logicalType: 'decimal', exampleConcept: 'other',
    observedOccurrences: 1, route: [],
    forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'DEFAULT' }], defaultForm: 'VALUE',
    ...overrides,
  });
  const matchingOption = option(50, {
    choiceId: 'choice-integer', title: 'Observation component values (integer)',
    description: 'Codes and their paired values on Observation records.',
    valuePath: 'Observation.component.valueInteger', logicalType: 'integer',
  });
  const response = {
    snapshotToken, outputId, complete: false, truncated: false, nextCursor: 'next-page-token',
    sources: [...Array.from({ length: 50 }, (_, index) => option(index, { description: 'x'.repeat(12_000) })), matchingOption],
  };
  const diagnostic = codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  });
  assert.equal(diagnostic.envelope.sourceCount, 51);
  assert.equal(diagnostic.envelope.sourceCountExceedsLimit, true);
  assert.equal(diagnostic.envelope.sourceCountExceedsDiagnosticEntryCap, true);
  assert.equal(diagnostic.envelope.complete, false);
  assert.equal(diagnostic.envelope.nextCursor, 'next-page-token');
  assert.equal(diagnostic.diagnosticTruncated, true);
  assert.deepEqual(diagnostic.candidateChoices, [matchingOption], 'A matching native choice remains available outside the preview cap.');
  assert.deepEqual(diagnostic.matchedChoiceIndexes, [0]);
  assert.ok(diagnostic.sourcePreview.length < 50);
  assert.ok(diagnostic.diagnosticBytes <= diagnostic.diagnosticByteCap);
  assert.ok(diagnostic.envelope.responseBytes > diagnostic.diagnosticByteCap);

  assert.throws(() => codedPivotSourceOptionsDiagnosticFor({ ...entry, path: `${path}/other` }, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  }), /exact owned endpoint/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin: 'http://127.0.0.1:8188', project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  }), /exact owned endpoint/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project: 'wrong-project', generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  }), /exact owned endpoint/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: '', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  }), /exact project, generation/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId: 'out_other', snapshotToken, mode: 'integer', domSnapshot,
  }), /exact owned output/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken: 'sha256:other', mode: 'integer', domSnapshot,
  }), /exact owned output/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer',
    domSnapshot: { ...domSnapshot, mode: 'string' },
  }), /failure DOM must retain its mode/);

  for (const wrongFrame of [
    { ...matchingOption, route: [{ fromResourceType: 'Observation', toResourceType: 'Patient' }] },
    { ...matchingOption, resourceType: 'Patient' },
    { ...matchingOption, valuePath: 'Observation.component.valueString' },
  ]) {
    const wrongFrameDiagnostic = codedPivotSourceOptionsDiagnosticFor(entry, request, {
      ...response, sources: [wrongFrame], complete: true, nextCursor: undefined,
    }, { origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot });
    assert.equal(wrongFrameDiagnostic.candidateChoiceCount, 0, 'Only the exact direct Observation mode frame belongs to this chooser contract.');
  }
});

test('native request evidence rejects pending, failed, and statusless requests', () => {
  assert.equal(summarizeCodedPivotNativeRequests([{ browserRequestId: '1', status: 200, completedAt: 10 }]).passed, true);

  const pending = summarizeCodedPivotNativeRequests([{ browserRequestId: '2', status: 200, responseReceivedAt: 5 }]);
  assert.equal(pending.pending.length, 1);
  assert.equal(pending.passed, false);

  const failed = summarizeCodedPivotNativeRequests([{ browserRequestId: '3', failure: 'net::ERR_ABORTED', completedAt: 12 }]);
  assert.equal(failed.pending.length, 0, 'An explicit request failure is terminal evidence.');
  assert.equal(failed.terminalFailures.length, 1, 'A terminal failure remains visible and blocks a clean pass.');
  assert.equal(failed.passed, false);

  assert.equal(summarizeCodedPivotNativeRequests([{ browserRequestId: '4', completedAt: 14 }]).passed, false);
});

test('integer and string native cases have separate registered four-dimension lifecycle contracts', () => {
  const scenario = registry.find(entry => entry.id === scenarioID);
  assert(scenario, 'The native coded Pivot scenario is registered.');
  assert.equal(scenario.script, 'verify-cda-coded-pivot.mjs');
  assert.ok(scenario.endpoints.includes('POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/construction-proposals'));
  assert.ok(scenario.endpoints.includes('POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/commands'));

  for (const mode of ['integer', 'string']) {
    const caseName = `coded-pivot-${mode}`;
    const contract = scenarioCaseFor(scenario, caseName);
    assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-reshape.spec.mjs');
    assert.match(contract.playwrightGrep, new RegExp(`${caseName}\\$`));
    assert.deepEqual(contract.expectedIdentity, { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });
    assert.equal(contract.lifecycleEvidence.performance.check, contract.performanceCheckName);
    assert.equal(contract.lifecycleEvidence.performance.checkpointBudgetMs, DEFAULT_ACTION_TO_RENDER_BUDGET_MS);
    assert.equal(contract.requiredChecks.length, 9);
    assert.equal(contract.requiredChecks[0], 'Independent raw Observation oracle proves the exact project, generation, ID, Coding.system/code, and mode-specific scalar values');
    assert.match(contract.requiredChecks[3], /Cancel preserves the exact saved CODED_PIVOT and values/);
    assert.match(contract.requiredChecks[7], /five seconds/);
    assert.equal(contract.requiredChecks[8], 'No unexpected native requests or browser errors occurred');
  }

  const sourceText = readFileSync(new URL('../../workflows/verify-cda-coded-pivot.mjs', import.meta.url), 'utf8');
  const nativeEvidenceText = readFileSync(new URL('../coded-pivot-native-evidence.mjs', import.meta.url), 'utf8');
  for (const [index, dimension] of [
    [0, 'correctness'], [1, 'usability'], [2, 'correctness'], [3, 'persistence'],
    [4, 'persistence'], [5, 'persistence'], [6, 'persistence'], [7, 'performance'], [8, 'correctness'],
  ]) assert.match(sourceText, new RegExp(`recordCheck\\(${index}, '${dimension}'`));
  assert.match(sourceText, /scenarioCaseFor\('standalone-reshape-coded-pivot', `coded-pivot-\$\{mode\}`\)/);
  assert.match(sourceText, /construction-cancel-proposal/);
  assert.match(sourceText, /frame-source-options/);
  assert.match(sourceText, /semantic-inventory/);
  assert.match(sourceText, /codedPivotProposalRequestMatches\(requestCapture\.rawRequestBody\(entry\)/);
  assert.match(nativeEvidenceText, /export const codedPivotProposalRequestMatches/);
  assert.match(nativeEvidenceText, /export const codedPivotPolicyReplacementCancellationEvidenceFor/);
  assert.match(sourceText, /report\.proposedSourceBindings = proposedSourceBindings/);
  assert.match(sourceText, /codedPivotPersistedSourceMatchesOption\(proposedCodedStep, selectedSourceOption\)/);
  assert.match(sourceText, /codedPivotPersistedSourceMatchesOption\(codedStep, selectedSourceOption\)/);
  assert.match(sourceText, /recordPivotActionToRender\(/);
  assert.match(sourceText, /startedAt:\s*started/);
  assert.match(sourceText, /name:\s*`\$\{name\}-to-render`/);
  assert.match(sourceText, /codedPivotRenderedValuesFor\(/);
  assert.match(sourceText, /waitNative\(codedPivotRemovalProposalReady, \{ id: observationId \}/);
  assert.match(sourceText, /summarizeCodedPivotNativeRequests\(/);
  assert.match(sourceText, /report\.nativeRequestEvidence\.passed/);
  const firstFailureCapture = sourceText.indexOf('const firstFailureEvidence = await codedPivotFirstFailureEvidenceFor');
  assert(firstFailureCapture >= 0 && firstFailureCapture < sourceText.indexOf('} finally {', firstFailureCapture),
    'Failure-time DOM and source options must be captured before the workflow cleanup finally block.');
  assert.match(nativeEvidenceText, /Number\.isFinite\(request\.completedAt\)/);
  assert.match(nativeEvidenceText, /terminalFailures/);
  assert.match(nativeEvidenceText, /invalidStatuses/);
  assert.match(sourceText, /CODED_PIVOT/);
  assert.ok(sourceText.indexOf('const editStarted = Date.now();') < sourceText.indexOf('await select(policyAction.label'));
  assert.ok(sourceText.indexOf('const editRequestStart = report.nativeRequests.length;') < sourceText.indexOf('await select(policyAction.label'));
  assert.ok(sourceText.indexOf('const reapplyStarted = Date.now();') < sourceText.indexOf('await select(reapplyPolicyAction.label'));
  assert.ok(sourceText.indexOf('const reapplyRequestStart = report.nativeRequests.length;') < sourceText.indexOf('await select(reapplyPolicyAction.label'));
  assert.match(sourceText, /editProposal = await requestCapture\.waitFor\([\s\S]*codedPivotProposalRequestMatches\(requestCapture\.rawRequestBody\(entry\)/);
  assert.match(sourceText, /reapplyProposal = await requestCapture\.waitFor\([\s\S]*codedPivotProposalRequestMatches\(requestCapture\.rawRequestBody\(entry\)/);
  assert.match(sourceText, /codedPivotRemovalRequestMatches\(requestCapture\.rawRequestBody\(entry\)/);
  assert.match(sourceText, /cda\.withExpectedCancellations\(/);
  assert.match(sourceText, /codedPivotPolicyReplacementCancellationEvidenceFor\(/);
  assert.match(sourceText, /codedPivotBackToTableControl\(page\)/);
  assert.ok(sourceText.indexOf('const editorDisposalRequestStart = report.nativeRequests.length;') < sourceText.indexOf("await action('Reopen coded pivot editor'"),
    'The semantic-inventory request window must include requests created while opening the editor.');
  assert.match(sourceText, /await cda\.withExpectedCancellations\(\{[\s\S]*contract: 'coded-pivot-editor-disposal'[\s\S]*actionLabel: editorDisposalAction\.label/);
  assert.match(nativeEvidenceText, /export const codedPivotRemovalRequestMatches/);
  assert.match(sourceText, /codedPivotBindingsFor\(editedCodedStep\), report\.initialStepBindings/);
  assert.match(sourceText, /report\.cancelReloadAssociation, report\.outputAssociation/);

  const checkpoints = [];
  assert.deepEqual(recordPivotActionToRender({ cases: checkpoints, name: 'apply-to-render', startedAt: 100, finishedAt: 137,
    budgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS }), { name: 'apply-to-render', durationMs: 37, budgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS });
  assert.deepEqual(checkpoints, [{ name: 'apply-to-render', durationMs: 37, budgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS }]);
});

test('coverage maps only the exact integer and string coded forms and leaves other coded forms open', () => {
  const owner = registry.find(entry => entry.id === 'builder-authoring');
  const broadGap = owner.coverage.find(entry => entry.feature === 'coded Pivot');
  assert.equal(broadGap.acceptance.kind, 'unmapped');
  assert.match(broadGap.acceptance.unmappedReason, /Other source resource types/);

  for (const [feature, caseName] of [
    ['integer coded Pivot columns', 'coded-pivot-integer'],
    ['string coded Pivot columns', 'coded-pivot-string'],
  ]) {
    const coverage = registry.find(entry => entry.id === scenarioID).coverage.find(entry => entry.feature === `native ${feature === 'integer coded Pivot columns' ? 'integer' : 'string'} coded Pivot values on one exact CDA Observation`);
    assert.equal(coverage.status, 'untested');
    assert.equal(coverage.acceptance.kind, 'lifecycle');
    assert.equal(coverage.acceptance.case, caseName);
    assert.equal(hasLifecycleContract(coverage, registry.find(entry => entry.id === scenarioID)), true);
  }
});

test('both native coded forms join the exact scenario while retaining distinct Playwright case names', () => {
  const spec = readFileSync(new URL('../../specs/standalone-reshape.spec.mjs', import.meta.url), 'utf8');
  const codedCaseNames = [...spec.matchAll(/register\('coded-pivot-([^']+)'/g)].map(([, mode]) => `coded-pivot-${mode}`);
  assert.deepEqual(codedCaseNames, ['coded-pivot-integer', 'coded-pivot-string']);

  const codedRegistrations = [...spec.matchAll(/register\('coded-pivot-(integer|string)',\s*runCodedPivotWorkflow,\s*\{\s*mode:\s*'(integer|string)'\s*\},\s*\{\s*cdaScenarioID:\s*'standalone-reshape-coded-pivot'\s*,?\s*\}\s*\);/g)]
    .map(([, caseMode, workflowMode]) => [caseMode, workflowMode]);
  assert.deepEqual(codedRegistrations, [['integer', 'integer'], ['string', 'string']],
    'Each explicit native case must bind its matching fixture mode to the shared coded Pivot scenario.');
});
