import assert from 'node:assert/strict';

export const CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT = 2_000;
const maxSubjectSelectionLimit = 10;
const nonempty = value => typeof value === 'string' && value.trim().length > 0;

function assertRawObservation(row, project, generation, label = 'Observation') {
  assert.equal(row?.project, project, `${label} project`);
  assert.equal(row?.generation, generation, `${label} generation`);
  assert.equal(row?.resourceType, 'Observation', `${label} FHIR resource type`);
  assert(nonempty(row?._id), `${label} Arango document key`);
  assert(nonempty(row?.id), `${label} FHIR id`);
  assert(nonempty(row?.subjectReference), `${label} subject.reference`);
  assert(nonempty(row?.status), `${label} status`);
  assert(Array.isArray(row?.codeCodingCodes), `${label} code.coding codes must be an array.`);
  for (const code of row.codeCodingCodes) {
    assert.equal(typeof code, 'string', `${label} code.coding code values must be strings.`);
  }
}

function validateRawObservationScan(rows, { project, generation, maxRows }) {
  assert(Array.isArray(rows), 'Bounded raw Observation rows are required.');
  assert(Number.isInteger(maxRows) && maxRows > 0 && rows.length <= maxRows,
    `Raw scan must remain within ${maxRows} Observations.`);
  assert(nonempty(project) && nonempty(generation), 'Exact project and generation are required.');
  const documentKeys = new Set();
  const fhirIDs = new Set();
  for (const row of rows) {
    assertRawObservation(row, project, generation);
    assert(!documentKeys.has(row._id), `Raw scan repeated Arango document key ${row._id}.`);
    assert(!fhirIDs.has(row.id), `Raw scan repeated FHIR Observation ID ${row.id}.`);
    documentKeys.add(row._id);
    fhirIDs.add(row.id);
  }
  return rows;
}

export function selectCdaGroupPivotJoinSubjects(rows, { project, generation, limit = maxSubjectSelectionLimit }) {
  assert(Number.isInteger(limit) && limit > 0 && limit <= maxSubjectSelectionLimit,
    `Subject selection limit must be between 1 and ${maxSubjectSelectionLimit}.`);
  const scopedRows = validateRawObservationScan(rows, {
    project, generation, maxRows: CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT,
  });
  const counts = new Map();
  for (const row of scopedRows) counts.set(row.subjectReference, (counts.get(row.subjectReference) ?? 0) + 1);
  return [...counts]
    .sort(([leftSubject, leftCount], [rightSubject, rightCount]) =>
      rightCount - leftCount || leftSubject.localeCompare(rightSubject))
    .slice(0, limit)
    .map(([subject]) => subject);
}

export function chooseCdaGroupPivotJoinWitness(rows, { project, generation, limit = CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT }) {
  const scopedRows = validateRawObservationScan(rows, { project, generation, maxRows: limit });
  const bySubject = new Map();
  for (const row of scopedRows) {
    const firstCode = row.codeCodingCodes[0];
    if (!nonempty(firstCode)) continue;
    const subjectRows = bySubject.get(row.subjectReference) ?? [];
    subjectRows.push(row);
    bySubject.set(row.subjectReference, subjectRows);
  }

  const subjects = [...bySubject.keys()].sort((left, right) => left.localeCompare(right));
  for (const sharedSubject of subjects) {
    const byCategory = new Map();
    for (const row of bySubject.get(sharedSubject)) {
      const category = row.codeCodingCodes[0];
      const categoryRows = byCategory.get(category) ?? [];
      categoryRows.push(row);
      byCategory.set(category, categoryRows);
    }
    const categories = [...byCategory.keys()].sort((left, right) => left.localeCompare(right));
    const repeatedCategory = categories.find(category => byCategory.get(category).length >= 2);
    const otherCategory = categories.find(category => category !== repeatedCategory);
    const leftOnlySubject = subjects.find(subject => subject !== sharedSubject);
    if (!repeatedCategory || !otherCategory || !leftOnlySubject) continue;
    const categoryRows = [
      ...byCategory.get(repeatedCategory).slice(0, 2),
      byCategory.get(otherCategory)[0],
    ];
    const leftOnly = bySubject.get(leftOnlySubject)[0];
    const left = [...categoryRows, leftOnly].sort((a, b) => a._id.localeCompare(b._id));
    const right = [...categoryRows].sort((a, b) => a._id.localeCompare(b._id));
    assert.equal(left.length, 4);
    assert.equal(right.length, 3);
    assert.equal(categoryRows.filter(row => row.subjectReference === sharedSubject && row.codeCodingCodes[0] === repeatedCategory).length, 2);
    assert.equal(categoryRows.filter(row => row.subjectReference === sharedSubject && row.codeCodingCodes[0] === otherCategory).length, 1);
    assert.equal(new Set(left.map(row => row._id)).size, 4, 'Left population must contain four distinct documents.');
    assert.equal(new Set(right.map(row => row._id)).size, 3, 'Right population must contain three distinct documents.');
    return {
      project, generation, sharedSubject, leftOnlySubject,
      categories: [repeatedCategory, otherCategory], left, right,
    };
  }
  return undefined;
}

export function verifyCdaGroupPivotJoinReread(witness, rereadRows, { project, generation }) {
  assert(witness && Array.isArray(witness.left) && witness.left.length === 4 &&
    Array.isArray(witness.right) && witness.right.length === 3, 'A complete selected Group→Pivot witness is required.');
  assert(Array.isArray(rereadRows), 'Exact selected Observation reread must return rows.');
  const expected = new Map(witness.left.map(row => [row._id, row]));
  assert.equal(expected.size, 4, 'Selected left population must contain four exact documents.');
  assert.equal(rereadRows.length, expected.size, 'Exact reread must return all and only four selected documents.');
  const actual = new Map();
  for (const row of rereadRows) {
    assertRawObservation(row, project, generation, `Reread ${row?._id ?? '<unknown>'}`);
    assert(!actual.has(row._id), `Exact reread duplicated ${row._id}.`);
    const selected = expected.get(row._id);
    assert(selected, `Exact reread returned unselected document ${row._id}.`);
    assert.deepEqual(row, selected, `Exact reread changed raw Observation ${selected.id}.`);
    actual.set(row._id, row);
  }
  assert.deepEqual([...actual.keys()].sort(), [...expected.keys()].sort(), 'Exact reread document keys must equal the immutable selection.');
  const rightKeys = new Set(witness.right.map(row => row._id));
  assert.equal(rightKeys.size, 3);
  assert([...rightKeys].every(id => expected.has(id)), 'Right Group→Pivot population must be a subset of the left Group population.');
  assert.equal(witness.left.filter(row => row.subjectReference === witness.leftOnlySubject).length, 1);
  assert(witness.right.every(row => row.subjectReference === witness.sharedSubject));
  return { leftIDs: [...expected.keys()].sort(), rightIDs: [...rightKeys].sort(), exact: true };
}

function sortTupleRows(rows) {
  return [...rows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
}

export function prepareCdaGroupPivotJoinOracle(witness, { project, generation }) {
  assert(witness?.project === project && witness?.generation === generation, 'Group→Pivot oracle must remain in the exact project and generation.');
  const { sharedSubject, leftOnlySubject, categories } = witness;
  assert.equal(categories.length, 2);
  const leftCounts = new Map();
  const rightCounts = new Map();
  for (const row of witness.left) {
    const category = row.codeCodingCodes?.[0];
    assert(nonempty(category), 'Group→Pivot witness rows require a nonempty first code.coding code.');
    leftCounts.set(row.subjectReference, (leftCounts.get(row.subjectReference) ?? 0) + 1);
  }
  for (const row of witness.right) {
    const category = row.codeCodingCodes?.[0];
    assert(nonempty(category), 'Group→Pivot witness rows require a nonempty first code.coding code.');
    const key = JSON.stringify([row.subjectReference, category]);
    rightCounts.set(key, (rightCounts.get(key) ?? 0) + 1);
  }
  const leftGroups = sortTupleRows([...leftCounts].map(([subject, count]) => [subject, count]));
  const rightGroups = sortTupleRows([...rightCounts].map(([key, count]) => [...JSON.parse(key), count]));
  const rightPivot = [[sharedSubject, ...categories.map(category => rightCounts.get(JSON.stringify([sharedSubject, category])) ?? null)]];
  const leftBySubject = new Map(leftGroups.map(row => [row[0], row]));
  const rightBySubject = new Map(rightPivot.map(row => [row[0], row]));
  const leftJoin = sortTupleRows(leftGroups.map(([subject, leftCount]) => {
    const pivot = rightBySubject.get(subject);
    return [subject, leftCount, pivot?.[0] ?? null, pivot?.[1] ?? null, pivot?.[2] ?? null];
  }));
  const innerJoin = sortTupleRows([...leftBySubject].flatMap(([subject, [key, leftCount]]) => {
    const pivot = rightBySubject.get(subject);
    return pivot ? [[key, leftCount, pivot[0], pivot[1], pivot[2]]] : [];
  }));
  assert.equal(leftGroups.length, 2, 'Left Group must have exactly shared and left-only subject rows.');
  assert.equal(leftGroups.find(row => row[0] === sharedSubject)?.[1], 3);
  assert.equal(leftGroups.find(row => row[0] === leftOnlySubject)?.[1], 1);
  assert.deepEqual(rightGroups, sortTupleRows([[sharedSubject, categories[0], 2], [sharedSubject, categories[1], 1]]));
  assert.deepEqual(rightPivot, [[sharedSubject, 2, 1]], 'Pivot must preserve coding-code multiplicity as A=2, B=1.');
  assert.equal(leftJoin.length, 2, 'LEFT Join must not fan out either grouped identity.');
  assert.equal(innerJoin.length, 1, 'INNER Join must retain only the matched shared subject.');
  assert.deepEqual(leftJoin.find(row => row[0] === leftOnlySubject), [leftOnlySubject, 1, null, null, null]);
  assert.deepEqual(innerJoin, [[sharedSubject, 3, sharedSubject, 2, 1]]);
  return { leftGroups, rightGroups, rightPivot, leftJoin, innerJoin };
}
