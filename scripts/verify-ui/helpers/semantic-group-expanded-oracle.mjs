import assert from 'node:assert/strict';

export const VALUE_CODEABLE_CONCEPT_CODING_PATH = 'valueCodeableConcept.coding[]';
export const SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT = 1_000;
export const SEMANTIC_GROUP_EXPANDED_MAX_ROOTS = 8;
export const SEMANTIC_GROUP_EXPANDED_MAX_ROWS = 32;

const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
export const codingIdentityKey = coding => JSON.stringify([
  coding.system ?? null,
  coding.version ?? null,
  coding.code,
]);
const sourceSort = (left, right) => left._key.localeCompare(right._key);

function normalizeSourceRows(rawRows, project, generation) {
  assert(Array.isArray(rawRows), 'The bounded Observation oracle must return an array.');
  assert(rawRows.length <= SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT,
    `The Observation oracle exceeded its ${SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT}-row cap.`);
  const seenIDs = new Set();
  return rawRows.flatMap(raw => {
    assert.equal(raw.project, project);
    assert.equal(raw.generation, generation);
    assert.equal(raw.resourceType, 'Observation');
    assert.equal(typeof raw._key, 'string');
    assert(raw._key.trim());
    assert.equal(typeof raw.id, 'string');
    assert(raw.id.trim());
    assert.equal(typeof raw.subjectReference, 'string');
    assert(raw.subjectReference.trim());
    assert(Array.isArray(raw.codings));
    assert(!seenIDs.has(raw.id), 'Observation IDs must be unique within the bounded oracle.');
    seenIDs.add(raw.id);
    const codings = raw.codings.map((coding, index) => {
      assert.equal(coding.ordinal, index,
        `Coding ordinal ${coding.ordinal} does not preserve source-array order.`);
      const system = typeof coding.system === 'string' ? coding.system : null;
      const version = typeof coding.version === 'string' && coding.version.trim() ? coding.version : null;
      assert.equal(typeof coding.code, 'string', `Coding item ${index} has no exact string code.`);
      assert(coding.code.trim(), `Coding item ${index} has a blank code.`);
      const display = typeof coding.display === 'string' && coding.display.trim() ? coding.display : null;
      return { ordinal: index, system, version, code: coding.code, display };
    });
    if (codings.length === 0) return [];
    return [{
      _key: raw._key,
      id: raw.id,
      subjectReference: raw.subjectReference,
      codings,
    }];
  }).sort(sourceSort);
}

function tupleLabel(coding) {
  return coding.display ?? coding.code;
}

function tupleBuckets(rows) {
  const buckets = new Map();
  for (const row of rows) {
    for (const coding of row.codings) {
      const key = codingIdentityKey(coding);
      const bucket = buckets.get(key) ?? { key, coding, rows: new Map(), labels: new Set() };
      const rowCodings = bucket.rows.get(row.id) ?? [];
      rowCodings.push(coding);
      bucket.rows.set(row.id, rowCodings);
      bucket.labels.add(tupleLabel(coding));
      buckets.set(key, bucket);
    }
  }
  return [...buckets.values()].sort((left, right) =>
    right.rows.size - left.rows.size ||
    [...right.rows.values()].reduce((count, codings) => count + codings.length, 0) -
      [...left.rows.values()].reduce((count, codings) => count + codings.length, 0) ||
    left.key.localeCompare(right.key));
}

function selectRoots(rows) {
  const candidates = tupleBuckets(rows).filter(bucket => bucket.rows.size >= 2 && bucket.labels.size === 1 &&
    bucket.coding.display && clean(bucket.coding.display) !== clean(bucket.coding.code));
  const seed = candidates[0];
  assert(seed,
    'The bounded Observation source has no shared Coding identity with one useful display label across two distinct owners.');

  const selected = [];
  const selectedIDs = new Set();
  const selectedSubjects = new Set();
  const selectedLabels = new Map();
  let expandedRows = 0;
  const add = row => {
    if (selectedIDs.has(row.id) || selected.length >= SEMANTIC_GROUP_EXPANDED_MAX_ROOTS ||
        expandedRows + row.codings.length > SEMANTIC_GROUP_EXPANDED_MAX_ROWS) return false;
    const labelsInRow = new Map();
    for (const coding of row.codings) {
      const key = codingIdentityKey(coding);
      const label = tupleLabel(coding);
      if ((labelsInRow.has(key) && labelsInRow.get(key) !== label) ||
          (selectedLabels.has(key) && selectedLabels.get(key) !== label)) return false;
      labelsInRow.set(key, label);
    }
    selected.push(row);
    selectedIDs.add(row.id);
    selectedSubjects.add(row.subjectReference);
    for (const [key, label] of labelsInRow) selectedLabels.set(key, label);
    expandedRows += row.codings.length;
    return true;
  };

  const seedRows = rows.filter(row => row.codings.some(coding => codingIdentityKey(coding) === seed.key));
  for (const row of seedRows) {
    if (!selectedSubjects.has(row.subjectReference)) add(row);
    if (selected.length >= SEMANTIC_GROUP_EXPANDED_MAX_ROOTS) break;
  }
  for (const row of seedRows) add(row);

  const selectedTupleKeys = new Set(selected.flatMap(row => row.codings.map(codingIdentityKey)));
  for (const row of rows) {
    if (selected.length >= SEMANTIC_GROUP_EXPANDED_MAX_ROOTS ||
        expandedRows >= SEMANTIC_GROUP_EXPANDED_MAX_ROWS) break;
    if (selectedIDs.has(row.id)) continue;
    const addsTuple = row.codings.some(coding => !selectedTupleKeys.has(codingIdentityKey(coding)));
    const addsOwnerRoute = !selectedSubjects.has(row.subjectReference);
    if (!addsTuple && !addsOwnerRoute) continue;
    if (add(row)) {
      for (const coding of row.codings) selectedTupleKeys.add(codingIdentityKey(coding));
    }
  }

  assert(selected.length >= 2, 'The selected bounded Coding witness must contain at least two Observation owners.');
  assert(new Set(selected.flatMap(row => row.codings.map(codingIdentityKey))).size >= 2,
    'The selected bounded Coding witness must contain at least two exact Coding identities.');
  return selected.sort(sourceSort);
}

function groupOccurrenceRows(selectedRows) {
  const bySubjectAndIdentity = new Map();
  const byIdentity = new Map();
  const rawExpandedRows = [];
  const occurrenceRows = [];
  for (const row of selectedRows) {
    for (const coding of row.codings) {
      const identity = codingIdentityKey(coding);
      const subjectKey = JSON.stringify([row.subjectReference, identity]);
      const detailed = bySubjectAndIdentity.get(subjectKey) ?? {
        identity: { system: coding.system, version: coding.version, code: coding.code },
        display: tupleLabel(coding),
        subjectReference: row.subjectReference,
        count: 0,
        ownerIDs: new Set(),
        ordinals: [],
      };
      assert(detailed.display === tupleLabel(coding),
        'A Coding identity has conflicting display labels in the same subject group.');
      detailed.count += 1;
      detailed.ownerIDs.add(row.id);
      detailed.ordinals.push({ id: row.id, ordinal: coding.ordinal });
      bySubjectAndIdentity.set(subjectKey, detailed);

      const identityGroup = byIdentity.get(identity) ?? {
        identity: { system: coding.system, version: coding.version, code: coding.code },
        display: tupleLabel(coding),
        count: 0,
        ownerIDs: new Set(),
        ordinals: [],
      };
      assert(identityGroup.display === tupleLabel(coding),
        'A Coding identity has conflicting display labels across selected owners.');
      identityGroup.count += 1;
      identityGroup.ownerIDs.add(row.id);
      identityGroup.ordinals.push({ id: row.id, ordinal: coding.ordinal });
      byIdentity.set(identity, identityGroup);
      rawExpandedRows.push([row.id]);
      occurrenceRows.push({
        ownerID: row.id,
        subjectReference: row.subjectReference,
        identity: { system: coding.system, version: coding.version, code: coding.code },
        display: tupleLabel(coding),
        ordinal: coding.ordinal,
      });
    }
  }
  const sortRecords = records => records.sort((left, right) =>
    JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const subjectGroups = sortRecords([...bySubjectAndIdentity.values()].map(group => ({
    ...group,
    ownerIDs: [...group.ownerIDs],
    ordinals: group.ordinals.sort((left, right) => left.id.localeCompare(right.id) || left.ordinal - right.ordinal),
  })));
  const identityGroups = sortRecords([...byIdentity.values()].map(group => ({
    ...group,
    ownerIDs: [...group.ownerIDs],
    ordinals: group.ordinals.sort((left, right) => left.id.localeCompare(right.id) || left.ordinal - right.ordinal),
  })));
  return {
    subjectGroups,
    identityGroups,
    occurrenceRows: sortRecords(occurrenceRows),
    rawExpandedRows: sortRecords(rawExpandedRows),
  };
}

export function buildSemanticGroupExpandedOracle(rawRows, { project, generation } = {}) {
  assert.equal(typeof project, 'string');
  assert(project.trim());
  assert.equal(typeof generation, 'string');
  assert(generation.trim());
  const normalized = normalizeSourceRows(rawRows, project, generation);
  assert(normalized.length > 0,
    `The first ${SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT} scoped Observations contain no usable ${VALUE_CODEABLE_CONCEPT_CODING_PATH} values.`);
  const selectedRows = selectRoots(normalized);
  const grouped = groupOccurrenceRows(selectedRows);
  return {
    sourceResourceType: 'Observation',
    sourcePath: VALUE_CODEABLE_CONCEPT_CODING_PATH,
    occurrenceId: 'base',
    scanLimit: SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT,
    scannedRows: rawRows.length,
    selectedRoots: selectedRows.map(row => ({
      id: row.id,
      sourceKey: row._key,
      subjectReference: row.subjectReference,
      codingCount: row.codings.length,
    })),
    sourceIDs: selectedRows.map(row => row.id),
    familyTuples: [...new Map(grouped.occurrenceRows.map(row => [
      codingIdentityKey(row.identity), row.identity,
    ])).values()].sort((left, right) => codingIdentityKey(left).localeCompare(codingIdentityKey(right))),
    displayLabels: [...new Set(grouped.occurrenceRows.map(row => row.display))].sort(),
    expectedExpandedRowCount: grouped.occurrenceRows.length,
    expectedRawExpandedRows: grouped.rawExpandedRows,
    expectedSubjectGroups: grouped.subjectGroups,
    expectedIdentityGroups: grouped.identityGroups,
    occurrenceRows: grouped.occurrenceRows,
  };
}

export const sortPreviewRows = rows => [...rows].map(row => JSON.stringify(row)).sort();

export function expectedSubjectGroupRows(oracle) {
  return oracle.expectedSubjectGroups.map(group => [
    group.subjectReference,
    group.display,
    String(group.count),
  ]);
}

export function expectedIdentityGroupRows(oracle) {
  return oracle.expectedIdentityGroups.map(group => [group.display, String(group.count)]);
}

export function expectedIdentityContributorRows(oracle) {
  return oracle.expectedIdentityGroups.map(group => [
    group.display,
    String(group.count),
    group.ownerIDs,
  ]);
}
