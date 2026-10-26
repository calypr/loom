import assert from 'node:assert/strict';
import { prepareCdaGroupPivotJoinOracle } from './cda-group-pivot-join-oracle.mjs';

const expectedGroupKeyLabel = 'Subject Reference';
const nonempty = value => typeof value === 'string' && value.trim().length > 0;

export function verifyCdaGroupPivotPublicationReread(witness, rereadRows, { project, generation }) {
  assert(Array.isArray(witness?.right) && witness.right.length === 3,
    'Published right Group witness must contain exactly three selected Observations.');
  assert(Array.isArray(rereadRows), 'Exact selected right Group raw reread is required.');
  assert.equal(rereadRows.length, witness.right.length,
    'Exact selected right Group reread must return all and only three Observations.');
  const expected = new Map(witness.right.map(row => [row._id, row]));
  const actual = new Map();
  for (const row of rereadRows) {
    assert.equal(row.project, project, 'Reread Observation project must remain exact.');
    assert.equal(row.generation, generation, 'Reread Observation generation must remain exact.');
    assert.equal(row.resourceType, 'Observation', 'Reread resource type must remain Observation.');
    assert(!actual.has(row._id), `Exact right Group reread duplicated ${row._id}.`);
    const selected = expected.get(row._id);
    assert(selected, `Exact right Group reread returned unselected ${row._id}.`);
    assert.deepEqual(row, selected, `Exact right Group reread changed raw Observation ${selected.id}.`);
    actual.set(row._id, row);
  }
  assert.deepEqual([...actual.keys()].sort(), [...expected.keys()].sort(),
    'Exact right Group reread document keys must equal the selected raw IDs.');
  return [...actual.keys()].sort();
}

export function prepareCdaGroupPivotPublishedViewerOracle(witness, { project, generation }) {
  assert.equal(witness?.project, project, 'Published Group→Pivot witness must use the exact CDA project.');
  assert.equal(witness?.generation, generation, 'Published Group→Pivot witness must use the exact CDA generation.');
  assert.equal(witness?.right?.length, 3, 'Published right Group must contain the exact three selected Observations.');
  assert.equal(witness?.sharedSubject, 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2',
    'Published Group key must match the independent retained CDA witness.');
  assert.deepEqual(witness.categories, ['1222593009', '81247-9'],
    'Published Pivot categories must retain the independent raw-code order.');
  const groupedPivot = prepareCdaGroupPivotJoinOracle(witness, { project, generation });
  assert.deepEqual(groupedPivot.rightPivot, [[witness.sharedSubject, 2, 1]],
    'Published Group→Pivot must preserve exact raw category multiplicity.');
  const sourceObservationIDs = witness.right.map(row => row.id).sort();
  assert.deepEqual(sourceObservationIDs, [
    '26c7500c-f54f-5229-b5a7-a6757614bc21',
    '64704041-c4da-5375-aac0-88f951f78344',
    '7d2fe423-f934-56ff-bd96-426fb1fdf94e',
  ], 'Published Pivot must remain bound to the exact three retained raw FHIR Observation IDs.');
  assert(witness.right.every(row => row.subjectReference === witness.sharedSubject),
    'Every published Pivot source Observation must use the exact shared subject.');
  assert(witness.right.every(row => nonempty(row.codeCodingCodes?.[0])),
    'Every published Pivot source Observation must have an exact first coding code.');
  return {
    columns: [expectedGroupKeyLabel, ...witness.categories],
    rows: [{
      [expectedGroupKeyLabel]: groupedPivot.rightPivot[0][0],
      [witness.categories[0]]: groupedPivot.rightPivot[0][1],
      [witness.categories[1]]: groupedPivot.rightPivot[0][2],
    }],
    categories: [...witness.categories],
    sourceObservationIDs,
    groupPivotRows: groupedPivot.rightPivot.map(row => [...row]),
  };
}
