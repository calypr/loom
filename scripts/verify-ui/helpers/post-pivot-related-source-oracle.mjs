import assert from 'node:assert/strict';

const nonempty = value => typeof value === 'string' && value.trim().length > 0;

function assertScope(record, project, generation, label) {
  assert.equal(record.project, project, `${label} project scope`);
  assert.equal(record.generation, generation, `${label} generation scope`);
}

export function choosePostPivotRelatedSourcePair(rows, { project, generation, limit = 2000 }) {
  assert(Array.isArray(rows), 'The bounded Observation scan must return rows.');
  assert(Number.isInteger(limit) && limit > 0 && rows.length <= limit,
    `The raw Observation scan must stay within ${limit} rows.`);
  assert(nonempty(project) && nonempty(generation), 'Exact project and generation are required.');

  const scoped = rows.filter(row => {
    assertScope(row, project, generation, `Observation ${row?._id ?? '<unknown>'}`);
    assert.equal(row.resourceType, 'Observation');
    return nonempty(row._id) && nonempty(row.id) && nonempty(row.status)
      && nonempty(row.patientReference) && row.patientReference.startsWith('Patient/');
  }).sort((left, right) => left._id.localeCompare(right._id));

  for (let leftIndex = 0; leftIndex < scoped.length; leftIndex += 1) {
    const left = scoped[leftIndex];
    for (let rightIndex = leftIndex + 1; rightIndex < scoped.length; rightIndex += 1) {
      const right = scoped[rightIndex];
      if (left.status !== right.status || left.patientReference === right.patientReference) continue;
      return { project, generation, status: left.status, members: [left, right] };
    }
  }
  return undefined;
}

export function verifyPostPivotRelatedSourceWitness(pair, linkedSources, { project, generation }) {
  assert(pair && Array.isArray(pair.members) && pair.members.length === 2,
    'The Pivot oracle needs exactly two selected Observation roots.');
  assert(Array.isArray(linkedSources), 'Exact subject_Patient edge reads must be an array.');
  assertScope(pair, project, generation, 'Pivot pair');
  assert(nonempty(pair.status), 'The two Observation roots need one nonempty shared status.');

  const members = [...pair.members].sort((left, right) => left._id.localeCompare(right._id));
  assert.notEqual(members[0]._id, members[1]._id, 'Pivot members must be distinct source documents.');
  assert.notEqual(members[0].id, members[1].id, 'Pivot members must have distinct FHIR IDs.');
  assert(members.every(member => member.status === pair.status), 'The selected roots must coalesce under the exact status key.');
  assert.notEqual(members[0].patientReference, members[1].patientReference,
    'Distinct Pivot contributors must have distinct Patient references.');

  const bySource = new Map();
  for (const linked of linkedSources) {
    const source = linked?.source;
    assert(source && nonempty(source._id), 'Every edge result must retain its exact source identity.');
    assertScope(source, project, generation, `Observation ${source._id}`);
    assert.equal(source.resourceType, 'Observation');
    const entries = bySource.get(source._id) ?? [];
    entries.push(linked);
    bySource.set(source._id, entries);
  }
  assert.deepEqual([...bySource.keys()].sort(), members.map(member => member._id).sort(),
    'Exact edge reread must return only the two selected Observation roots.');

  const resolved = members.map(member => {
    const entries = bySource.get(member._id) ?? [];
    assert.equal(entries.length, 1,
      `Observation ${member.id} must have exactly one scoped subject_Patient edge, got ${entries.length}.`);
    const linked = entries[0];
    assert.equal(linked.source.id, member.id, 'Exact edge reread changed the selected Observation ID.');
    assert.equal(linked.source.status, pair.status, 'Exact edge reread changed the shared Pivot key.');
    assert.equal(linked.source.patientReference, member.patientReference,
      'Exact edge reread changed the raw subject.reference value.');
    const patient = linked.patient;
    assert(patient, `Observation ${member.id} must link to a raw Patient document.`);
    assertScope(patient, project, generation, `Patient ${patient._id ?? '<unknown>'}`);
    assert.equal(patient.resourceType, 'Patient');
    assert(nonempty(patient._id) && nonempty(patient.id), 'Patient target identity is required.');
    assert.equal(member.patientReference, `Patient/${patient.id}`,
      'The raw subject.reference must resolve to this exact scoped Patient target.');
    return { observation: member, patient };
  });

  assert.notEqual(resolved[0].patient._id, resolved[1].patient._id,
    'The coalesced Pivot row must retain two distinct Patient terminal identities.');
  assert.notEqual(resolved[0].patient.id, resolved[1].patient.id,
    'The coalesced Pivot row must project two distinct Patient IDs.');

  return {
    project,
    generation,
    status: pair.status,
    members: resolved,
    patientIDsInCompilerOrder: [...resolved]
      .sort((left, right) => left.patient._id.localeCompare(right.patient._id))
      .map(entry => entry.patient.id),
    patientResourceTypesInCompilerOrder: [...resolved]
      .sort((left, right) => left.patient._id.localeCompare(right.patient._id))
      .map(entry => entry.patient.resourceType),
  };
}
