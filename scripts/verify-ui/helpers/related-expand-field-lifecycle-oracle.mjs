import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const RESOURCE_TYPES = ['MedicationAdministration', 'Observation', 'Patient'];
const EXPECTED_IDS = Object.freeze({
  Patient: ['field-lifecycle-patient-matched', 'field-lifecycle-patient-unmatched'],
  Observation: ['field-lifecycle-observation-matched', 'field-lifecycle-observation-unmatched'],
  MedicationAdministration: ['field-lifecycle-administration-a', 'field-lifecycle-administration-b'],
});

const readResources = (fixtureDir, resourceType) => {
  const path = join(fixtureDir, `${resourceType}.ndjson`);
  const sizeBytes = statSync(path).size;
  assert(sizeBytes > 0 && sizeBytes <= 4096, `${resourceType}.ndjson must be a bounded nonempty source file`);
  const source = readFileSync(path, 'utf8');
  const records = source.split(/\r?\n/).filter(line => line.trim()).map(line => JSON.parse(line));
  assert(records.every(record => record.resourceType === resourceType), `${resourceType}.ndjson contains a different resource type`);
  assert.deepEqual(records.map(record => record.id).sort(), EXPECTED_IDS[resourceType], `${resourceType} fixture IDs changed`);
  assert.equal(new Set(records.map(record => record.id)).size, records.length, `${resourceType} IDs must be unique`);
  return {
    records,
    manifest: {
      name: `${resourceType}.ndjson`,
      sizeBytes,
      recordCount: records.length,
      sha256: createHash('sha256').update(source).digest('hex'),
    },
  };
};

export const readRelatedExpandFieldLifecycleOracle = (fixtureDir) => {
  const loaded = Object.fromEntries(RESOURCE_TYPES.map(type => [type, readResources(fixtureDir, type)]));
  const patients = new Map(loaded.Patient.records.map(patient => [patient.id, patient]));
  const administrationsByPatient = new Map();
  for (const administration of loaded.MedicationAdministration.records) {
    const reference = administration.subject?.reference;
    assert.match(reference ?? '', /^Patient\//, `MedicationAdministration/${administration.id} must reference one fixture Patient`);
    const patientId = reference.slice('Patient/'.length);
    assert(patients.has(patientId), `MedicationAdministration/${administration.id} references an unknown Patient`);
    const matches = administrationsByPatient.get(patientId) ?? [];
    matches.push(administration);
    administrationsByPatient.set(patientId, matches);
  }

  const roots = loaded.Observation.records.map(observation => {
    const reference = observation.subject?.reference;
    assert.match(reference ?? '', /^Patient\//, `Observation/${observation.id} must reference one fixture Patient`);
    const patientId = reference.slice('Patient/'.length);
    const patient = patients.get(patientId);
    assert(patient, `Observation/${observation.id} references an unknown Patient`);
    assert.equal(typeof observation.status, 'string', `Observation/${observation.id} must have a public status value`);
    assert.equal(typeof patient.gender, 'string', `Patient/${patientId} must have a populated gender value`);
    assert.match(patient.birthDate ?? '', /^\d{4}-\d{2}-\d{2}$/, `Patient/${patientId} must have a populated birthDate`);
    return { observation, patient };
  });
  const observationsById = new Map(roots.map(({ observation }) => [observation.id, observation]));
  assert.equal(observationsById.get('field-lifecycle-observation-matched')?.status, 'final');
  assert.equal(observationsById.get('field-lifecycle-observation-unmatched')?.status, 'preliminary');
  assert.equal(administrationsByPatient.get('field-lifecycle-patient-matched')?.length, 2,
    'The matched Patient must have exactly two MedicationAdministration records');
  assert.equal(administrationsByPatient.has('field-lifecycle-patient-unmatched'), false,
    'The unmatched Patient must have no MedicationAdministration records');

  const rows = roots.flatMap(({ observation, patient }) => {
    const patientId = patient.id;
    const matches = administrationsByPatient.get(patientId) ?? [];
    return matches.length > 0
      ? matches.map(administration => ({
        observationId: observation.id,
        observationStatus: observation.status,
        patientId,
        patientGender: patient.gender,
        patientBirthDate: patient.birthDate,
        medicationAdministrationId: administration.id,
        medicationAdministrationStatus: administration.status ?? null,
      }))
      : [{
        observationId: observation.id,
        observationStatus: observation.status,
        patientId,
        patientGender: patient.gender,
        patientBirthDate: patient.birthDate,
        medicationAdministrationId: null,
        medicationAdministrationStatus: null,
      }];
  }).sort((a, b) => a.observationId.localeCompare(b.observationId) ||
    String(a.medicationAdministrationId).localeCompare(String(b.medicationAdministrationId)));

  const groups = [...rows.reduce((counts, row) => counts.set(row.observationStatus,
    (counts.get(row.observationStatus) ?? 0) + 1), new Map())]
    .map(([observationStatus, count]) => ({ observationStatus, count }))
    .sort((a, b) => a.observationStatus.localeCompare(b.observationStatus));

  assert.deepEqual(rows, [
    {
      observationId: 'field-lifecycle-observation-matched',
      observationStatus: 'final',
      patientId: 'field-lifecycle-patient-matched',
      patientGender: 'female',
      patientBirthDate: '1980-04-03',
      medicationAdministrationId: 'field-lifecycle-administration-a',
      medicationAdministrationStatus: 'completed',
    },
    {
      observationId: 'field-lifecycle-observation-matched',
      observationStatus: 'final',
      patientId: 'field-lifecycle-patient-matched',
      patientGender: 'female',
      patientBirthDate: '1980-04-03',
      medicationAdministrationId: 'field-lifecycle-administration-b',
      medicationAdministrationStatus: 'in-progress',
    },
    {
      observationId: 'field-lifecycle-observation-unmatched',
      observationStatus: 'preliminary',
      patientId: 'field-lifecycle-patient-unmatched',
      patientGender: 'male',
      patientBirthDate: '1990-02-10',
      medicationAdministrationId: null,
      medicationAdministrationStatus: null,
    },
  ], 'The independent row oracle must preserve both matches and the unmatched parent');
  assert.deepEqual(groups, [
    { observationStatus: 'final', count: 2 },
    { observationStatus: 'preliminary', count: 1 },
  ], 'Grouping by the carried public root field must preserve exact multiplicity');

  return {
    sourceFiles: RESOURCE_TYPES.map(type => loaded[type].manifest),
    scope: { rootResourceType: 'Observation', relatedResourceType: 'MedicationAdministration' },
    route: [
      { fromResourceType: 'Observation', toResourceType: 'Patient', relationship: 'subject_Patient', storageDirection: 'OUTBOUND' },
      { fromResourceType: 'Patient', toResourceType: 'MedicationAdministration', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
    ],
    rows,
    groups,
  };
};
