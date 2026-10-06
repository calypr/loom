import assert from 'node:assert/strict';

// Build the only construction shape a saved RELATED_EXPAND policy edit may
// produce when EXCLUDE changes to PRESERVE_PARENT: the authored policy changes,
// and its stable related-record output becomes nullable at this step and every
// downstream step that carries the same output column.
export function expectedPreserveParentRelatedEdit(construction, stepIndex) {
  const expected = JSON.parse(JSON.stringify(construction));
  assert(Number.isInteger(stepIndex) && stepIndex >= 0 && stepIndex < expected.steps.length,
    'the edited Related step must exist');

  const editedStep = expected.steps[stepIndex];
  assert.equal(editedStep.operation?.kind, 'RELATED_EXPAND', 'the edited step must be RELATED_EXPAND');
  const related = editedStep.operation.relatedExpand;
  assert(related, 'the edited step must carry a Related expansion');
  assert.equal(related.emptyPolicy, 'EXCLUDE', 'the saved Related expansion must start with EXCLUDE');
  assert.equal(typeof related.relatedRecordColumnId, 'string', 'the Related output must have a stable column ID');

  related.emptyPolicy = 'PRESERVE_PARENT';
  const propagatedOutputStepIDs = [];
  for (const step of expected.steps.slice(stepIndex)) {
    const outputs = step.outputs ?? [];
    const matches = outputs.filter(column => column.id === related.relatedRecordColumnId);
    assert.equal(matches.length, 1,
      `step ${step.id} must carry exactly one copy of Related output ${related.relatedRecordColumnId}`);
    assert.notEqual(matches[0].nullable, true,
      `step ${step.id} must derive nullable from the saved EXCLUDE policy before the edit`);
    matches[0].nullable = true;
    propagatedOutputStepIDs.push(step.id);
  }

  return {
    construction: expected,
    relatedRecordColumnId: related.relatedRecordColumnId,
    stepIndex,
    propagatedOutputStepIDs,
    authoredDownstreamOperations: authoredStepOperations(construction.steps.slice(stepIndex + 1)),
  };
}

export function authoredStepOperations(steps) {
  return steps.map(step => ({ id: step.id, operation: step.operation }));
}

export function assertPreserveParentRelatedEdit(candidateConstruction, expectation) {
  assert.deepEqual(candidateConstruction, expectation.construction,
    'only the edited empty policy and its carried related-record nullable flag may differ from the authored construction');
  assert.deepEqual(authoredStepOperations(candidateConstruction.steps.slice(expectation.stepIndex + 1)),
    expectation.authoredDownstreamOperations,
    'downstream step IDs and operations must remain exactly as authored');
}
