import assert from 'node:assert/strict';
import test from 'node:test';
import { directHeightQuantityChoices } from '../../workflows/builder-coded-source-column.mjs';

test('rendered Height label binds to its direct Quantity source token despite copy wording', () => {
  const renderedChoices = [
    {
      testId: 'frame-source-choice-height-date',
      text: 'Observation code values Example: Height · 2 source occurrences Relationship path On Observation records Observation → Patient via subject → Observation via subject',
    },
    {
      testId: 'frame-source-choice-height-quantity',
      text: 'Observation code values Example: Height · 2 source occurrences Relationship path On Observation records Observation → Patient via subject → Observation via subject',
    },
    {
      testId: 'frame-source-choice-height-related',
      text: 'Observation code values Example: Height · 2 source occurrences Relationship path On Observation records',
    },
  ];
  const sourceOptions = [
    { choiceId: 'height-date', resourceType: 'Observation', sourcePath: 'code', route: [], valuePath: 'effectiveDateTime' },
    { choiceId: 'height-quantity', resourceType: 'Observation', sourcePath: 'code', route: [], valuePath: 'valueQuantity.value' },
    { choiceId: 'height-related', resourceType: 'Observation', sourcePath: 'code', route: [{ fromResourceType: 'Patient' }], valuePath: 'valueQuantity.value' },
  ];

  const matches = directHeightQuantityChoices(renderedChoices, sourceOptions);

  assert.deepEqual(matches, [renderedChoices[1]]);
  assert.equal(matches.some(choice => choice.testId === 'frame-source-choice-height-date'), false,
    'the direct effectiveDateTime Height source must not be selected');
});
