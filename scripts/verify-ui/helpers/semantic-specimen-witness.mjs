import assert from 'node:assert/strict';

export function parseSpecimenSemanticWitnessReport(report, { project, generation }) {
  assert.equal(report?.status, 'passed', 'The independent Specimen source witness must pass its bounded read.');
  assert.equal(typeof report?.evidence?.querySha256, 'string');
  assert.match(report.evidence.querySha256, /^[a-f0-9]{64}$/);
  assert(report.evidence.queryReport, 'The source witness must retain its query report path.');
  assert(report.evidence.explainReport, 'The source witness must retain its indexed query plan.');

  const witness = report.witness;
  assert.equal(witness?.project, project, 'The Specimen source witness belongs to another project.');
  assert.equal(witness?.generation, generation, 'The Specimen source witness belongs to another generation.');
  assert.equal(witness?.resourceType, 'Specimen', 'The root semantic coverage witness must be a Specimen.');
  assert.equal(typeof witness?.id, 'string');
  assert(witness.id.trim(), 'The Specimen source witness has an empty resource ID.');
  assert(Array.isArray(witness?.raw?.type?.coding) && witness.raw.type.coding.length > 0,
    'The independent Specimen.type oracle must contain at least one raw Coding.');

  const coding = witness.raw.type.coding.filter((item) =>
    typeof item?.system === 'string' && item.system.trim() &&
    typeof item?.code === 'string' && item.code.trim(),
  );
  assert(coding.length > 0, 'The independent Specimen.type oracle has no exact system/code Coding.');
  const selectedCoding = coding[0];
  const familyCodings = coding.filter((item) =>
    item.system === selectedCoding.system && (item.version ?? '') === (selectedCoding.version ?? ''),
  );
  assert(familyCodings.length > 0);

  const noncoded = witness.raw.noncoded;
  assert.equal(typeof noncoded?.path, 'string');
  assert(noncoded.path.trim(), 'The witness must name one independently read noncoded Specimen field.');
  assert(!/\.coding(?:\[\])?(?:\.|$)/.test(noncoded.path),
    'The independently selected Specimen field must not be a Coding leaf.');
  assert(noncoded.value !== undefined, 'The noncoded source field must have an independently read value.');

  return {
    source: {
      project: witness.project,
      generation: witness.generation,
      resourceType: witness.resourceType,
      id: witness.id,
      ...(typeof witness.sourceKey === 'string' ? { sourceKey: witness.sourceKey } : {}),
      ...(typeof witness.authResourcePath === 'string' ? { authResourcePath: witness.authResourcePath } : {}),
    },
    raw: witness.raw,
    coding: selectedCoding,
    familyCodings,
    expectedSemanticValues: familyCodings.map((item) => item.display?.trim() || item.code),
    noncoded,
  };
}
