import assert from 'node:assert/strict';

const REQUIRED_BUCKETS = ['many', 'one', 'zero'];

export function deriveContributorRuleOracle(witnesses) {
  assert(Array.isArray(witnesses), 'CDA contributor witnesses must be an array');
  const byBucket = new Map();
  for (const witness of witnesses) {
    assert(REQUIRED_BUCKETS.includes(witness?.bucket), `Unexpected CDA contributor bucket: ${witness?.bucket}`);
    assert(!byBucket.has(witness.bucket), `Duplicate CDA contributor bucket: ${witness.bucket}`);
    assert(typeof witness.patient?.id === 'string' && witness.patient.id.length > 0,
      `${witness.bucket} witness must have an exact Patient ID`);
    assert(typeof witness.patient?._id === 'string' && witness.patient._id.length > 0,
      `${witness.bucket} witness must have an exact Patient document key`);
    assert(Array.isArray(witness.observations), `${witness.bucket} witness must list its raw Observations`);
    for (const observation of witness.observations) {
      assert(typeof observation?.id === 'string' && observation.id.length > 0,
        `${witness.bucket} Observation must have an exact FHIR ID`);
      assert(typeof observation?._id === 'string' && observation._id.length > 0,
        `${witness.bucket} Observation must have an exact document key`);
    }
    byBucket.set(witness.bucket, witness);
  }

  assert.deepEqual([...byBucket.keys()].sort(), REQUIRED_BUCKETS,
    'CDA contributor witnesses must contain one zero, one, and many Patient');
  const ordered = REQUIRED_BUCKETS.map(bucket => byBucket.get(bucket));
  assert.equal(new Set(ordered.map(witness => witness.patient.id)).size, 3,
    'zero, one, and many Patient witnesses must have distinct FHIR IDs');
  assert.equal(new Set(ordered.map(witness => witness.patient._id)).size, 3,
    'zero, one, and many Patient witnesses must have distinct document keys');
  assert.equal(byBucket.get('zero').observations.length, 0, 'zero witness must have no related Observation');
  assert.equal(byBucket.get('one').observations.length, 1, 'one witness must have exactly one related Observation');
  assert(byBucket.get('many').observations.length >= 2, 'many witness must have at least two related Observations');

  const selectedObservationId = byBucket.get('many').observations[0].id;
  const selectedMatches = ordered.flatMap(({ patient, observations }) => observations
    .filter(observation => observation.id === selectedObservationId)
    .map(observation => [patient.id, observation.id]));
  assert.equal(selectedMatches.length, 1,
    'the selected many-witness Observation ID must identify exactly one raw related source record');

  const baselineRows = ordered.map(({ patient }) => [patient.id]);
  const preserveParentRows = ordered.map(({ patient, observations }) => {
    const match = observations.find(observation => observation.id === selectedObservationId);
    return [patient.id, match?.id ?? '—'];
  });
  const excludeRows = selectedMatches;
  assert(preserveParentRows.length > excludeRows.length,
    'PRESERVE_PARENT must retain at least one zero-match source Patient');

  return {
    selectedObservationId,
    baselineRows,
    preserveParentRows,
    excludeRows,
    countByBucket: Object.fromEntries(ordered.map(witness => [witness.bucket, witness.observations.length])),
  };
}

export function assertExactContributorRows(actualRows, expectedRows, label) {
  assert(Array.isArray(actualRows), `${label}: rendered rows must be an array`);
  assert(Array.isArray(expectedRows), `${label}: raw-oracle rows must be an array`);
  assert(expectedRows.length < 25, `${label}: raw oracle may reach the native preview cap`);
  assert(actualRows.every(Array.isArray), `${label}: each rendered row must be a value array`);
  assert(expectedRows.every(Array.isArray), `${label}: each oracle row must be a value array`);
  const serialize = rows => rows.map(row => JSON.stringify(row)).sort();
  assert.deepEqual(serialize(actualRows), serialize(expectedRows),
    `${label}: rendered rows must equal the independent raw oracle as a complete multiset`);
  return actualRows;
}
