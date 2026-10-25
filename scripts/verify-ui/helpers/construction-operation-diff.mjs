import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';

export const changedConstructionOperationSteps = (savedSteps, candidateSteps) => {
  assert(Array.isArray(savedSteps), 'Saved construction steps must be an array.');
  assert(Array.isArray(candidateSteps), 'Candidate construction steps must be an array.');
  const savedById = new Map();
  for (const step of savedSteps) {
    assert(typeof step?.id === 'string' && step.id.length > 0,
      'Saved construction steps must retain their actual IDs.');
    assert(!savedById.has(step.id), `Saved construction repeats step ID ${step.id}.`);
    savedById.set(step.id, step);
  }
  return candidateSteps.filter(step => {
    assert(typeof step?.id === 'string' && step.id.length > 0,
      'Candidate construction steps must retain their actual IDs.');
    const saved = savedById.get(step.id);
    return !saved || !isDeepStrictEqual(saved.operation, step.operation);
  });
};
