import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const FILES = [
  'Condition.ndjson',
  'Medication.ndjson',
  'MedicationAdministration.ndjson',
  'Observation.ndjson',
  'Patient.ndjson',
  'Specimen.ndjson',
];

const parseRows = (fixtureDir, fileName, resourceType) => {
  const path = join(fixtureDir, fileName);
  const bytes = readFileSync(path);
  const rows = bytes.toString('utf8').trim().split(/\r?\n/).filter(Boolean).map((line, index) => {
    let resource;
    try { resource = JSON.parse(line); }
    catch (error) { throw new Error(`Invalid ${fileName} JSON on line ${index + 1}: ${error.message}`); }
    assert.equal(resource.resourceType, resourceType, `${fileName} line ${index + 1} must be ${resourceType}.`);
    assert.equal(typeof resource.id, 'string', `${fileName} line ${index + 1} requires an ID.`);
    return resource;
  });
  return { rows, sha256: createHash('sha256').update(bytes).digest('hex'), path };
};

const patientId = resource => {
  const reference = resource.subject?.reference;
  assert.equal(typeof reference, 'string', `${resource.resourceType}/${resource.id} requires a Patient subject.`);
  assert(reference.startsWith('Patient/'), `${resource.resourceType}/${resource.id} must reference a Patient.`);
  return reference.slice('Patient/'.length);
};

const codingRows = codeableConcept => (codeableConcept?.coding ?? []).map(coding => ({
  system: coding.system,
  version: coding.version,
  code: coding.code,
  display: coding.display,
}));

const rowsByPatient = (rows, project) => Object.fromEntries(rows.map(resource => [
  patientId(resource), project(resource),
]));

export const readStandaloneCodedFieldFixture = fixtureDir => {
  const files = Object.fromEntries(FILES.map(fileName => {
    const resourceType = fileName.slice(0, -'.ndjson'.length);
    return [resourceType, parseRows(fixtureDir, fileName, resourceType)];
  }));
  const { Patient, Condition, Observation, Specimen, MedicationAdministration, Medication } = files;
  assert.deepEqual(Patient.rows.map(row => row.id).sort(), [
    'coded-catalog-patient-01', 'coded-catalog-patient-02',
  ]);
  assert.deepEqual(Condition.rows.map(patientId).sort(), [...Patient.rows.map(row => row.id)].sort());
  assert.deepEqual(Observation.rows.map(patientId).sort(), [...Patient.rows.map(row => row.id)].sort());
  assert.deepEqual(Specimen.rows.map(patientId).sort(), [...Patient.rows.map(row => row.id)].sort());
  assert.deepEqual(MedicationAdministration.rows.map(patientId).sort(), [...Patient.rows.map(row => row.id)].sort());

  const medicationById = new Map(Medication.rows.map(row => [row.id, row]));
  const medicationPerPatient = rowsByPatient(MedicationAdministration.rows, administration => {
    const reference = administration.medication?.reference?.reference;
    assert.equal(typeof reference, 'string', `${administration.id} requires a Medication reference.`);
    assert(reference.startsWith('Medication/'), `${administration.id} must reference Medication.`);
    const medication = medicationById.get(reference.slice('Medication/'.length));
    assert(medication, `${administration.id} must reference a fixture Medication.`);
    return codingRows(medication.code);
  });

  const expected = {
    patientIDs: Patient.rows.map(row => row.id).sort(),
    conditionCodesByPatient: rowsByPatient(Condition.rows, row => codingRows(row.code)),
    medicationCodesByPatient: medicationPerPatient,
    specimenCodesByPatient: rowsByPatient(Specimen.rows, row => codingRows(row.type)),
    observationPairsByPatient: rowsByPatient(Observation.rows, row => ({
      codes: codingRows(row.code),
      quantity: row.valueQuantity?.value ?? null,
    })),
    sourceFiles: Object.fromEntries(Object.entries(files).map(([resourceType, entry]) => [resourceType, {
      path: entry.path,
      sha256: entry.sha256,
      resourceCount: entry.rows.length,
    }])),
    routes: {
      Condition: [{ fromResourceType: 'Patient', toResourceType: 'Condition', relationship: 'subject_Patient', storageDirection: 'INBOUND' }],
      Observation: [{ fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' }],
      Specimen: [{ fromResourceType: 'Patient', toResourceType: 'Specimen', relationship: 'subject_Patient', storageDirection: 'INBOUND' }],
      Medication: [
        { fromResourceType: 'Patient', toResourceType: 'MedicationAdministration', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
        { fromResourceType: 'MedicationAdministration', toResourceType: 'Medication', relationship: 'medication_reference', storageDirection: 'OUTBOUND' },
      ],
    },
  };

  assert.deepEqual(expected.conditionCodesByPatient['coded-catalog-patient-01'].map(({ system, version, code, display }) => ({ system, version, code, display })), [
    { system: 'urn:loom:basic-coded-semantic:condition', version: 'v1', code: 'condition-shared', display: 'Condition v1 display' },
    { system: 'urn:loom:basic-coded-semantic:condition', version: 'v2', code: 'condition-shared', display: 'Condition v2 display' },
    { system: 'urn:loom:basic-coded-semantic:condition', version: undefined, code: 'condition-shared', display: 'Condition unversioned display' },
  ], 'The Condition CodeableConcept must retain each Coding display with its own code.');
  assert.deepEqual(expected.conditionCodesByPatient['coded-catalog-patient-02'], [],
    'The text-only Condition code is the absent-coded control.');
  assert.deepEqual(expected.medicationCodesByPatient['coded-catalog-patient-01'].map(({ code, display }) => ({ code, display })), [
    { code: 'medication-alpha', display: 'Medication Alpha' },
    { code: 'medication-beta', display: 'Medication Beta' },
  ], 'The Medication CodeableConcept must retain each Coding display with its own code.');
  assert.deepEqual(expected.medicationCodesByPatient['coded-catalog-patient-02'], [],
    'The text-only Medication code must not invent a Coding.');
  assert.deepEqual(expected.specimenCodesByPatient['coded-catalog-patient-01'], [{
    system: 'urn:loom:basic-coded-semantic:specimen',
    version: '1',
    code: 'specimen-type-no-display',
    display: undefined,
  }], 'The Specimen type Coding exercises code-only display fallback.');
  assert.deepEqual(expected.specimenCodesByPatient['coded-catalog-patient-02'], [],
    'The missing Specimen type is the absent-field control.');
  assert.deepEqual(expected.observationPairsByPatient['coded-catalog-patient-01'].quantity, 42);
  assert.deepEqual(expected.observationPairsByPatient['coded-catalog-patient-02'].quantity, null,
    'The Observation paired-value control has a code and no value.');

  return expected;
};
