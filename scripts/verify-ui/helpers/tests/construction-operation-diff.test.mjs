import assert from 'node:assert/strict';
import test from 'node:test';
import { changedConstructionOperationSteps } from '../construction-operation-diff.mjs';

test('an unchanged saved RELATED_EXPAND is not counted when candidate object key order differs', () => {
  const savedSteps = [{
    id: 'saved-step',
    operation: {
      kind: 'RELATED_EXPAND',
      relatedExpand: {
        route: [{ fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'hasMember' }],
        source: { resourceType: 'Patient', path: 'subject' },
      },
    },
  }];
  const candidateSteps = [
    {
      id: 'saved-step',
      operation: {
        relatedExpand: {
          source: { path: 'subject', resourceType: 'Patient' },
          route: [{ relationship: 'hasMember', toResourceType: 'Observation', fromResourceType: 'Patient' }],
        },
        kind: 'RELATED_EXPAND',
      },
    },
    {
      id: 'new-step',
      operation: {
        kind: 'RELATED_EXPAND',
        relatedExpand: {
          source: { resourceType: 'Observation', path: 'subject' },
          route: [{ fromResourceType: 'Observation', toResourceType: 'Specimen', relationship: 'focus' }],
        },
      },
    },
  ];

  const changed = changedConstructionOperationSteps(savedSteps, candidateSteps);
  assert.equal(changed.length, 1);
  assert.strictEqual(changed[0], candidateSteps[1]);
});
