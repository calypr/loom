import assert from 'node:assert/strict';
import test from 'node:test';
import {
  cdaSourceGroupCandidateResponseEquivalent,
  proveCdaSourceGroupSupersededChoiceCancellation,
} from '../cda-current-draft-membership-oracle.mjs';
import { constructionCandidateWireEquivalent } from '../builder-combine-draft-helpers.mjs';

const selectedChoice = {
  choiceId: 'rc1.signed-status-choice',
  fieldPath: 'status',
  fhirType: 'string',
  logicalType: 'string',
  label: 'Status',
  occurrenceId: 'base',
  isPopulated: true,
};

const request = () => ({
  groupSources: [{ rowChoiceId: selectedChoice.choiceId, columnId: 'group-source-status' }],
  candidateConstruction: {
    version: 1,
    steps: [{
      id: 'group-status',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'GROUP',
        group: {
          constructionId: 'group-status',
          missingKeyPolicy: 'GROUP',
          keys: [{ inputColumnId: 'group-source-status', outputColumnId: 'group-key-status' }],
          aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'group-count' }],
        },
      },
      outputs: [
        { id: 'group-key-status', name: 'status', label: 'Status', type: 'string' },
        { id: 'group-count', name: 'source_records', label: 'Source records', type: 'integer' },
      ],
    }],
  },
});

const response = () => ({
  candidateConstruction: {
    ...request().candidateConstruction,
    sourceProjections: [{
      columnId: 'group-source-status',
      fhirType: 'string',
      fieldPath: 'status',
      label: 'Status',
      logicalType: 'string',
      occurrenceId: 'base',
      ownerStepId: 'group-status',
    }],
  },
});

test('source GROUP response accepts only the canonical projection backed by the exact selected signed choice', () => {
  assert.equal(constructionCandidateWireEquivalent(request().candidateConstruction, response().candidateConstruction), false,
    'The old full-candidate comparison must reproduce the retained sourceProjections mismatch');
  assert.equal(cdaSourceGroupCandidateResponseEquivalent({
    request: request(), response: response(), selectedChoice,
  }), true);

  for (const field of ['columnId', 'fieldPath', 'occurrenceId', 'fhirType', 'logicalType', 'label', 'ownerStepId']) {
    const invalid = response();
    invalid.candidateConstruction.sourceProjections[0][field] = `wrong-${field}`;
    assert.equal(cdaSourceGroupCandidateResponseEquivalent({
      request: request(), response: invalid, selectedChoice,
    }), false, `wrong canonical ${field} must fail`);
  }

  const extraProjection = response();
  extraProjection.candidateConstruction.sourceProjections.push({ ...extraProjection.candidateConstruction.sourceProjections[0] });
  assert.equal(cdaSourceGroupCandidateResponseEquivalent({ request: request(), response: extraProjection, selectedChoice }), false);

  const wrongChoice = request();
  wrongChoice.groupSources[0].rowChoiceId = 'rc1.different-signed-choice';
  assert.equal(cdaSourceGroupCandidateResponseEquivalent({ request: wrongChoice, response: response(), selectedChoice }), false);

  const changedOutput = response();
  changedOutput.candidateConstruction.steps[0].outputs[0].id = 'different-output';
  assert.equal(cdaSourceGroupCandidateResponseEquivalent({ request: request(), response: changedOutput, selectedChoice }), false);
});

test('source GROUP cancellation requires the exact owned abort and later same-draft Status READY proposal', () => {
  const initialChoice = {
    choiceId: 'rc1.initial-value-string.payload',
    fieldPath: 'valueString',
    fhirType: 'string',
    logicalType: 'string',
    label: 'ValueString',
    occurrenceId: 'base',
    isPopulated: true,
  };
  const identity = {
    project: 'loom_dev_cda_fhir',
    explorer: 'cda-membership-owned',
    generation: 'cda-fhir-v1',
    uiOrigin: 'http://127.0.0.1:30008',
    outputId: 'out-membership',
    snapshotToken: 'sha256:snapshot',
    draftVersion: 13,
    draftDigest: 'sha256:draft-13',
    actionLabel: 'Group the restored target by its populated raw Observation status field',
    actionStartedAt: 1_200,
  };
  const proposalPath = `/api/v1/projects/${identity.project}/explorers/${identity.explorer}/authoring/v2/construction-proposals`;
  const bodyFor = choice => ({
    outputId: identity.outputId,
    snapshotToken: identity.snapshotToken,
    expectedDraftVersion: identity.draftVersion,
    expectedDraftDigest: identity.draftDigest,
    changedStepId: 'group-status',
    groupSources: [{ rowChoiceId: choice.choiceId, columnId: 'group-source-status' }],
    candidateConstruction: {
      version: 1,
      steps: [{
        id: 'group-status',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'GROUP', group: {
          constructionId: 'group-status',
          missingKeyPolicy: 'GROUP',
          keys: [{ inputColumnId: 'group-source-status', outputColumnId: 'group-key-status' }],
          aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'group-count' }],
        } },
        outputs: [
          { id: 'group-key-status', name: choice.fieldPath, label: choice.label, type: choice.logicalType },
          { id: 'group-count', name: 'source_records', label: 'Source records', type: 'integer' },
        ],
      }],
    },
  });
  const replacementBody = bodyFor(selectedChoice);
  const supersededEvent = {
    origin: identity.uiOrigin,
    path: proposalPath,
    method: 'POST',
    requestId: 'construction-proposal-superseded',
    browserRequestId: 'playwright-154',
    startedAt: 1_100,
    completedAt: 1_300,
    failure: 'net::ERR_ABORTED',
  };
  const replacementEvent = {
    origin: identity.uiOrigin,
    path: proposalPath,
    method: 'POST',
    requestId: 'construction-proposal-status',
    browserRequestId: 'playwright-155',
    startedAt: 1_350,
    completedAt: 1_800,
    status: 200,
    triggerAction: identity.actionLabel,
  };
  const failureDiagnostic = {
    errorText: 'net::ERR_ABORTED',
    method: 'POST',
    url: `${identity.uiOrigin}${proposalPath}`,
    requestId: supersededEvent.requestId,
    browserRequestId: supersededEvent.browserRequestId,
    failureAction: { label: identity.actionLabel },
    requestScope: {
      expectedProject: identity.project,
      generation: identity.generation,
      configuredExplorer: identity.explorer,
      requestProject: identity.project,
      requestExplorer: identity.explorer,
    },
  };
  const replacementResponse = {
    snapshotToken: identity.snapshotToken,
    draftVersion: identity.draftVersion,
    draftDigest: identity.draftDigest,
    outputId: identity.outputId,
    proposalId: 'receipt-status',
    previewStatus: 'READY',
    preview: { receiptId: 'receipt-status' },
    candidateConstruction: {
      ...replacementBody.candidateConstruction,
      sourceProjections: [{
        columnId: 'group-source-status',
        fhirType: selectedChoice.fhirType,
        fieldPath: selectedChoice.fieldPath,
        label: selectedChoice.label,
        logicalType: selectedChoice.logicalType,
        occurrenceId: selectedChoice.occurrenceId,
        ownerStepId: 'group-status',
      }],
    },
  };
  const input = {
    ...identity,
    supersededEvent,
    supersededBody: bodyFor(initialChoice),
    failureDiagnostic,
    replacementEvent,
    replacementBody,
    replacementResponse,
    supersededChoice: initialChoice,
    replacementChoice: selectedChoice,
  };

  const evidence = proveCdaSourceGroupSupersededChoiceCancellation(input);
  assert.equal(evidence.ok, true, evidence.reason);
  assert.equal(evidence.proof.supersededRequest.requestId, supersededEvent.requestId);
  assert.equal(evidence.proof.replacement.status, 200);
  assert.equal(evidence.proof.replacement.previewStatus, 'READY');
  assert.equal(evidence.proof.replacement.responseExact, true);

  assert.equal(proveCdaSourceGroupSupersededChoiceCancellation({
    ...input,
    failureDiagnostic: { ...failureDiagnostic, failureAction: { label: 'different action' } },
  }).ok, false, 'an abort attributed to a different action must remain unexpected');
  assert.equal(proveCdaSourceGroupSupersededChoiceCancellation({
    ...input,
    supersededEvent: { ...supersededEvent, response: { previewStatus: 'READY' } },
  }).ok, false, 'an aborted request with any response must not be classified as a cancellation');
  assert.equal(proveCdaSourceGroupSupersededChoiceCancellation({
    ...input,
    replacementResponse: { ...replacementResponse, snapshotToken: 'sha256:other-snapshot' },
  }).ok, false, 'a replacement from a different snapshot must not justify the abort');
  assert.equal(proveCdaSourceGroupSupersededChoiceCancellation({
    ...input,
    replacementResponse: { ...replacementResponse, previewStatus: 'INVALID' },
  }).ok, false, 'a non-READY replacement must not justify the abort');
});
