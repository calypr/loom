import assert from 'node:assert/strict';

const compareText = (left, right) => String(left).localeCompare(String(right));

const memberGroupWitness = member => {
  if (!member || typeof member.id !== 'string' || !Array.isArray(member.parents) || member.parents.length === 0 ||
      !Array.isArray(member.routeRows) || member.routeRows.length === 0) return undefined;
  const rows = new Map();
  for (const row of member.routeRows) {
    if (!row || typeof row.id !== 'string' || typeof row.subjectReference !== 'string' || !row.subjectReference.trim()) return undefined;
    rows.set(row.id, row.subjectReference);
  }
  if (rows.size !== member.routeRows.length) return undefined;
  const groupKeys = new Set(rows.values());
  if (groupKeys.size !== 1) return undefined;
  return { member, groupKey: groupKeys.values().next().value, observationIDs: new Set(rows.keys()) };
};

export const findMappedMemberRemovalWithGroupCountChange = (candidates, { maxPreviewRows = 24 } = {}) => {
  assert(Array.isArray(candidates), 'bounded CDA candidates must be an array');
  assert(Number.isInteger(maxPreviewRows) && maxPreviewRows > 0, 'preview row limit must be a positive integer');
  const mapped = candidates.map(memberGroupWitness).filter(Boolean);
  for (let leftIndex = 0; leftIndex < mapped.length; leftIndex += 1) {
    const removed = mapped[leftIndex];
    for (let rightIndex = leftIndex + 1; rightIndex < mapped.length; rightIndex += 1) {
      const survivor = mapped[rightIndex];
      if (removed.member.id === survivor.member.id || removed.groupKey !== survivor.groupKey) continue;
      const overlappingObservation = [...removed.observationIDs].some(id => survivor.observationIDs.has(id));
      if (overlappingObservation) continue;
      const initialObservationIDs = new Set([...removed.observationIDs, ...survivor.observationIDs]);
      if (initialObservationIDs.size > maxPreviewRows || initialObservationIDs.size <= survivor.observationIDs.size) continue;
      return {
        removedMember: removed.member,
        survivingMember: survivor.member,
        groupKey: removed.groupKey,
        initialObservationIDs: [...initialObservationIDs].sort(compareText),
        remainingObservationIDs: [...survivor.observationIDs].sort(compareText),
      };
    }
  }
  return undefined;
};

export const deriveGroupRowsFromCdaWitnesses = (members, rootRows) => {
  assert(Array.isArray(members) && Array.isArray(rootRows), 'selected members and independent raw root rows must be arrays');
  const rootsById = new Map(rootRows.map(row => [row?.id, row]));
  assert.equal(rootsById.size, rootRows.length, 'independent raw Observation roots must have unique IDs');
  const selectedObservationIDs = new Set();
  const routeSubjectReferences = new Map();
  for (const member of members) {
    assert(Array.isArray(member?.routeRows), `member ${member?.id ?? '<unknown>'} must have route rows`);
    for (const route of member.routeRows) {
      const id = typeof route === 'string' ? route : route?.id;
      assert(typeof id === 'string' && rootsById.has(id), `selected route row ${String(id)} must resolve to an independent raw Observation`);
      selectedObservationIDs.add(id);
      if (route && typeof route === 'object' && Object.hasOwn(route, 'subjectReference')) {
        const references = routeSubjectReferences.get(id) ?? new Set();
        references.add(route.subjectReference);
        routeSubjectReferences.set(id, references);
      }
    }
  }

  const groups = new Map();
  for (const id of selectedObservationIDs) {
    const root = rootsById.get(id);
    for (const routeSubjectReference of routeSubjectReferences.get(id) ?? []) {
      assert.equal(root.subjectReference, routeSubjectReference,
        `independent raw Observation ${id} must retain the same subject.reference as the mapped route witness`);
    }
    const groupKey = root.subjectReference;
    assert(typeof groupKey === 'string' && groupKey.trim(), `Observation ${id} must have a nonblank subject.reference`);
    assert(Array.isArray(root.specimenIDs), `Observation ${id} must have independently read Specimen IDs`);
    const group = groups.get(groupKey) ?? { observationIDs: new Set(), specimenIDs: new Set() };
    group.observationIDs.add(id);
    for (const specimenID of root.specimenIDs) {
      assert(typeof specimenID === 'string' && specimenID.length > 0, `Observation ${id} contains an invalid Specimen identity`);
      group.specimenIDs.add(specimenID);
    }
    groups.set(groupKey, group);
  }

  return [...groups.entries()]
    .sort(([left], [right]) => compareText(left, right))
    .map(([groupKey, group]) => [groupKey, String(group.observationIDs.size), String(group.specimenIDs.size)]);
};
