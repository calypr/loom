import assert from 'node:assert/strict';

export const MAX_CDA_MEMBERSHIP_SCAN = 2_000;

export const cdaMembershipObservationQuery = ({ project, generation }) => {
  assert.equal(typeof project, 'string');
  assert.equal(typeof generation, 'string');
  return `FOR r IN Observation FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)} AND r.payload.resourceType == "Observation" AND IS_STRING(r.id) AND LENGTH(TRIM(r.id)) > 0 SORT r._id LIMIT ${MAX_CDA_MEMBERSHIP_SCAN} RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType}`;
};

export function prepareCdaMembershipOracle(rows, { project, generation }) {
  assert(Array.isArray(rows), 'Bounded CDA Membership oracle requires raw Observation rows');
  assert(rows.length <= MAX_CDA_MEMBERSHIP_SCAN, 'Bounded CDA Membership oracle exceeded its 2,000-row limit');
  assert(typeof project === 'string' && project.length > 0, 'Bounded CDA Membership oracle requires an exact project');
  assert(typeof generation === 'string' && generation.length > 0, 'Bounded CDA Membership oracle requires an exact generation');
  assert(rows.every(row => row?.project === project && row?.generation === generation &&
    row?.resourceType === 'Observation' && typeof row?._id === 'string' && row._id.length > 0 &&
    typeof row?.id === 'string' && row.id.trim().length > 0),
  'Bounded CDA Membership oracle contains an out-of-scope or invalid Observation');
  assert.equal(new Set(rows.map(row => row._id)).size, rows.length, 'Bounded CDA Membership scan repeated a raw document key');
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length, 'Bounded CDA Membership scan repeated a FHIR Observation id');

  const ordered = [...rows].sort((left, right) => left._id.localeCompare(right._id));
  assert(ordered.length >= 3, `The first ${MAX_CDA_MEMBERSHIP_SCAN} scoped Observations need at least three distinct nonempty FHIR ids`);
  const [a, b, c] = ordered.slice(0, 3);
  const left = [a, b];
  const right = [a, c];
  const leftIDs = left.map(row => row.id);
  const rightIDs = right.map(row => row.id);
  const overlap = leftIDs.filter(id => new Set(rightIDs).has(id));
  const includeIDs = leftIDs.filter(id => new Set(rightIDs).has(id)).sort();
  const excludeIDs = leftIDs.filter(id => !new Set(rightIDs).has(id)).sort();
  assert.deepEqual(overlap, [a.id], 'CDA Membership witness must have exactly one shared Observation id');
  assert.deepEqual(includeIDs, [a.id].sort(), 'INCLUDE oracle must retain only the shared left id');
  assert.deepEqual(excludeIDs, [b.id].sort(), 'EXCLUDE oracle must retain only the left-only id');
  assert.deepEqual(rightIDs.sort(), [a.id, c.id].sort(), 'Right source must contain the shared and independent third id');

  return {
    scannedCount: rows.length,
    selectionLimit: 3,
    selected: { a: { _id: a._id, id: a.id }, b: { _id: b._id, id: b.id }, c: { _id: c._id, id: c.id } },
    left: left.map(row => ({ _id: row._id, id: row.id })),
    right: right.map(row => ({ _id: row._id, id: row.id })),
    leftIDs,
    rightIDs,
    includeIDs,
    excludeIDs,
  };
}
