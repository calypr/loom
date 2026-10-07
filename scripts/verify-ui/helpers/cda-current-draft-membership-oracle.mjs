import assert from 'node:assert/strict';

export const MAX_CDA_MEMBERSHIP_SCAN = 2_000;
export const CDA_SOURCE_GROUP_PREVIEW_LIMIT = 25;

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

const sourceGroupKeyIsValid = value => value === null || typeof value === 'string';
const sourceGroupDisplay = value => value === null ? '—' : String(value);
const sourceGroupRowsEqual = (left, right) =>
  JSON.stringify([...left].map(row => JSON.stringify(row)).sort()) ===
  JSON.stringify([...right].map(row => JSON.stringify(row)).sort());

export function compareCdaSourceGroupPreview({
  previewRows,
  visibleRows,
  rawGroupRows,
  displayedRereadRows,
  rowCount,
  sampled,
  limit = CDA_SOURCE_GROUP_PREVIEW_LIMIT,
}) {
  const fail = reason => ({ ok: false, reason });
  if (!Number.isInteger(limit) || limit < 1 || !Array.isArray(previewRows) ||
    !Array.isArray(visibleRows) || !Array.isArray(rawGroupRows)) {
    return fail('invalid preview comparison inputs');
  }
  if (rawGroupRows.length > limit + 1) return fail('raw GROUP scan exceeded its limit-plus-one bound');

  const expectedRowCount = Math.min(rawGroupRows.length, limit);
  const expectedSampled = rawGroupRows.length >= limit;
  if (rowCount !== expectedRowCount || sampled !== expectedSampled ||
    previewRows.length !== expectedRowCount || visibleRows.length !== expectedRowCount) {
    return fail('preview row count or sampled metadata differs from the bounded raw GROUP scan');
  }

  const validRows = rows => rows.every(row => row && sourceGroupKeyIsValid(row.status) &&
    Number.isInteger(row.count) && row.count > 0);
  if (!validRows(rawGroupRows)) return fail('raw GROUP scan contains an invalid key or count');
  if (new Set(rawGroupRows.map(row => JSON.stringify(row.status))).size !== rawGroupRows.length) {
    return fail('raw GROUP scan repeated a status key');
  }

  const entries = [];
  for (const row of previewRows) {
    if (!Array.isArray(row) || row.length !== 2 || !sourceGroupKeyIsValid(row[0]) ||
      !Number.isInteger(row[1]) || row[1] < 1) return fail('preview contains an invalid status key or count');
    entries.push({ status: row[0], count: row[1] });
  }
  if (new Set(entries.map(row => JSON.stringify(row.status))).size !== entries.length) {
    return fail('preview repeated a status key');
  }

  const expectedVisibleRows = entries.map(row => [sourceGroupDisplay(row.status), String(row.count)]);
  if (!sourceGroupRowsEqualInOrder(visibleRows, expectedVisibleRows)) {
    return fail('visible GROUP rows differ from the completed preview response');
  }

  const sampledSubset = rawGroupRows.length > limit;
  const oracleRows = sampledSubset ? displayedRereadRows : rawGroupRows;
  if (!Array.isArray(oracleRows) || !validRows(oracleRows)) {
    return fail(sampledSubset
      ? 'displayed-key raw GROUP reread is missing or invalid'
      : 'complete raw GROUP oracle is missing or invalid');
  }
  if (new Set(oracleRows.map(row => JSON.stringify(row.status))).size !== oracleRows.length) {
    return fail('raw GROUP oracle repeated a status key');
  }
  if (sampledSubset && oracleRows.length !== entries.length) {
    return fail('displayed-key raw GROUP reread did not return every visible key');
  }

  const expectedOracleRows = entries.map(row => [row.status, row.count]);
  const actualOracleRows = oracleRows.map(row => [row.status, row.count]);
  if (!sourceGroupRowsEqual(actualOracleRows, expectedOracleRows)) {
    return fail(sampledSubset
      ? 'displayed status keys or counts differ from the independent raw reread'
      : 'preview status keys or counts differ from the complete independent raw GROUP oracle');
  }

  return {
    ok: true,
    comparison: sampledSubset ? 'displayed-key-reread' : 'complete-raw-group-set',
    expectedRowCount,
    expectedSampled,
    expectedRows: oracleRows.map(row => [sourceGroupDisplay(row.status), String(row.count)]),
    displayedStatusKeys: entries.map(row => row.status),
  };
}

function sourceGroupRowsEqualInOrder(left, right) {
  return Array.isArray(left) && left.length === right.length && left.every((row, index) =>
    Array.isArray(row) && row.length === 2 && row[0] === right[index][0] && row[1] === right[index][1]);
}
