import assert from 'node:assert/strict';
import test from 'node:test';
import { relatedChoiceStageContext } from '../related-choice-stage-context.mjs';

const sourceProjectionDocument = {
  output: { id: 'out_88b0748626467999ffc3682c' },
  rootResourceType: 'Specimen',
  population: { selectionRevisionId: 'selection', route: [] },
};

test('source projection without authored construction uses the root row context', () => {
  assert.deepEqual(relatedChoiceStageContext(sourceProjectionDocument, 'source_projection'), {
    stageId: 'source_projection',
    anchorColumnId: '_key',
  });
});

test('saved construction cannot silently fall back to the source projection', () => {
  assert.throws(
    () => relatedChoiceStageContext(sourceProjectionDocument, 'authored_construction'),
    /authored construction must include a saved step/,
  );
});

test('saved Group keeps its step identity and root row anchor', () => {
  assert.deepEqual(relatedChoiceStageContext({
    construction: { version: 1, steps: [{ id: 'group-step', operation: { kind: 'GROUP' } }] },
  }, 'authored_construction'), {
    stageId: 'group-step',
    anchorColumnId: '_key',
  });
});

test('saved related expansion keeps its related-record anchor', () => {
  assert.deepEqual(relatedChoiceStageContext({
    construction: { version: 1, steps: [{
      id: 'related-step',
      operation: { kind: 'RELATED_EXPAND', relatedExpand: { relatedRecordColumnId: 'col-related' } },
    }] },
  }, 'authored_construction'), {
    stageId: 'related-step',
    anchorColumnId: 'col-related',
  });
});
