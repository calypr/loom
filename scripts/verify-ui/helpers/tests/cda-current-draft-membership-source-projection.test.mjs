import assert from 'node:assert/strict';
import test from 'node:test';
import {
  cdaSourceGroupCandidateResponseEquivalent,
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
