import assert from 'node:assert/strict';

export function relatedChoiceStageContext(document, expectedStage) {
  assert(document && typeof document === 'object', 'related choice requires the current Builder document');
  if (expectedStage === 'source_projection') {
    const construction = document.construction;
    const steps = construction?.steps;
    assert(construction == null || (Array.isArray(steps) && steps.length === 0),
      'source projection must not contain authored construction steps');
    return { stageId: 'source_projection', anchorColumnId: '_key' };
  }

  assert.equal(expectedStage, 'authored_construction');
  const steps = document.construction?.steps;
  assert(Array.isArray(steps) && steps.length > 0,
    'authored construction must include a saved step');
  const previousStep = steps.at(-1);
  assert(previousStep && typeof previousStep.id === 'string' && previousStep.id.length > 0,
    'saved construction step must have an ID');
  const operation = previousStep.operation;
  assert(operation && typeof operation.kind === 'string',
    'saved construction step must have an operation kind');
  const anchorColumnId = operation.kind === 'RELATED_EXPAND'
    ? operation.relatedExpand?.relatedRecordColumnId
    : '_key';
  assert(anchorColumnId, 'saved related expansion must have a related-record anchor column');
  return {
    stageId: previousStep.id,
    anchorColumnId,
  };
}
