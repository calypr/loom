import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { isDeepStrictEqual } from 'node:util';
import { constructionCandidateWireEquivalent } from '../builder-combine-draft-helpers.mjs';

const { requestConstruction, responseConstruction } = JSON.parse(
  readFileSync(new URL('./fixtures/builder-combine-epoch27-proposal.json', import.meta.url), 'utf8'),
);

const withResponse = (mutate) => {
  const changed = structuredClone(responseConstruction);
  mutate(changed);
  return changed;
};

test('retained epoch27 Join proposal matches after only omitted false output nullability is normalized', () => {
  for (const outputIndex of [1, 3]) {
    assert.equal(requestConstruction.steps[0].outputs[outputIndex].nullable, false);
    assert.equal(Object.hasOwn(responseConstruction.steps[0].outputs[outputIndex], 'nullable'), false);
  }
  assert.equal(isDeepStrictEqual(requestConstruction, responseConstruction), false);
  const requestBefore = structuredClone(requestConstruction);
  const responseBefore = structuredClone(responseConstruction);
  assert.equal(constructionCandidateWireEquivalent(requestConstruction, responseConstruction), true);
  assert.deepEqual(requestConstruction, requestBefore, 'comparison does not mutate the request');
  assert.deepEqual(responseConstruction, responseBefore, 'comparison does not mutate the response');
});

test('wire equivalence preserves nullable true, stable IDs, exact keys, projections, and array order', () => {
  assert.equal(constructionCandidateWireEquivalent(
    requestConstruction,
    withResponse((candidate) => { delete candidate.steps[0].outputs[0].nullable; }),
  ), false, 'nullable true must not collapse to absent');
  assert.equal(constructionCandidateWireEquivalent(
    requestConstruction,
    withResponse((candidate) => { candidate.steps[0].outputs[0].nullable = false; }),
  ), false, 'nullable true must not collapse to explicit false');
  assert.equal(constructionCandidateWireEquivalent(
    requestConstruction,
    withResponse((candidate) => { candidate.steps[0].outputs[0].id = 'different-output-id'; }),
  ), false, 'output IDs remain exact');
  assert.equal(constructionCandidateWireEquivalent(
    requestConstruction,
    withResponse((candidate) => { candidate.steps[0].inputs[0].outputId = 'different-source-output-id'; }),
  ), false, 'input output IDs remain exact');
  assert.equal(constructionCandidateWireEquivalent(
    requestConstruction,
    withResponse((candidate) => { candidate.steps[0].operation.combine.keys[0].leftColumnId = 'different-key-id'; }),
  ), false, 'key column IDs remain exact');
  assert.equal(constructionCandidateWireEquivalent(
    requestConstruction,
    withResponse((candidate) => { candidate.steps[0].operation.combine.projections[0].inputColumnId = 'different-projection-id'; }),
  ), false, 'projection identities remain exact');
  assert.equal(constructionCandidateWireEquivalent(
    requestConstruction,
    withResponse((candidate) => { candidate.steps[0].operation.combine.projections.reverse(); }),
  ), false, 'projection order remains exact');
  assert.equal(constructionCandidateWireEquivalent(
    requestConstruction,
    withResponse((candidate) => { candidate.steps[0].outputs.reverse(); }),
  ), false, 'output order remains exact');
});
