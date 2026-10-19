import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const RESOURCE_FILES = Object.freeze({
  Patient: 'Patient.ndjson',
  Observation: 'Observation.ndjson',
  ResearchStudy: 'ResearchStudy.ndjson',
  DiagnosticReport: 'DiagnosticReport.ndjson',
});

const readResources = (fixtureDir, resourceType) => {
  const file = RESOURCE_FILES[resourceType];
  if (!file) throw new Error(`Unsupported replacement oracle resource type: ${resourceType}`);
  const path = join(fixtureDir, file);
  const contents = readFileSync(path);
  const records = contents.toString('utf8').split(/\r?\n/).filter(line => line.trim()).map((line, index) => {
    let value;
    try { value = JSON.parse(line); }
    catch (error) { throw new Error(`Invalid ${file} JSON at line ${index + 1}: ${error.message}`); }
    if (value.resourceType !== resourceType || typeof value.id !== 'string' || !value.id.trim()) {
      throw new Error(`${file} line ${index + 1} must contain a ${resourceType} with a non-empty id`);
    }
    return value;
  });
  const ids = records.map(record => record.id);
  if (records.length === 0 || new Set(ids).size !== ids.length) {
    throw new Error(`${file} must contain at least one resource with unique IDs`);
  }
  return { path, sha256: createHash('sha256').update(contents).digest('hex'), records };
};

export const readBasicRootReplacementOracle = ({ fixtureDir }) => {
  const patient = readResources(fixtureDir, 'Patient');
  const observation = readResources(fixtureDir, 'Observation');
  const researchStudy = readResources(fixtureDir, 'ResearchStudy');
  const diagnosticReport = readResources(fixtureDir, 'DiagnosticReport');
  const patientIDs = patient.records.map(record => record.id).sort();
  const observationPatientReferences = observation.records
    .map(record => record.subject?.reference)
    .filter(Boolean)
    .sort();
  if (observationPatientReferences.length !== observation.records.length ||
    observationPatientReferences.some(reference => !reference.startsWith('Patient/')) ||
    observationPatientReferences.map(reference => reference.slice('Patient/'.length)).sort().join('\n') !== patientIDs.slice(0, observationPatientReferences.length).sort().join('\n')) {
    throw new Error('Observation fixture must provide a literal populated route into the Patient starting table');
  }
  const researchStudyRows = researchStudy.records
    .map(({ id, title }) => ({ id, title }))
    .sort((left, right) => left.id.localeCompare(right.id));
  const diagnosticReportIDs = diagnosticReport.records.map(record => record.id).sort();
  const studyReportReferences = researchStudy.records.flatMap(record =>
    (record.result ?? []).map(result => result.reference).filter(Boolean));
  if (studyReportReferences.length !== diagnosticReportIDs.length ||
    studyReportReferences.some(reference => !reference.startsWith('DiagnosticReport/')) ||
    studyReportReferences.map(reference => reference.slice('DiagnosticReport/'.length)).sort().join('\n') !== diagnosticReportIDs.join('\n')) {
    throw new Error('ResearchStudy fixture must expose exactly one local DiagnosticReport relationship per study');
  }
  const patientReferences = [...researchStudy.records, ...diagnosticReport.records]
    .flatMap(record => JSON.stringify(record).match(/(?:Patient|patient)\/[^"\\]+/g) ?? []);
  if (patientReferences.length > 0) {
    throw new Error('Replacement resources must remain disconnected from the Patient starting table');
  }
  return {
    source: {
      projectFixturePath: fixtureDir,
      files: [patient, observation, researchStudy, diagnosticReport].map(({ path, sha256, records }) => ({
        path,
        sha256,
        recordCount: records.length,
      })),
    },
    patients: patient.records.map(({ id, birthDate }) => ({ id, birthDate })).sort((left, right) => left.id.localeCompare(right.id)),
    observations: observation.records.map(record => ({
      id: record.id,
      patientId: record.subject.reference.slice('Patient/'.length),
    })).sort((left, right) => left.id.localeCompare(right.id)),
    patientPopulationIDs: patientIDs.slice(0, 2),
    patientFilter: { fieldPath: 'birthDate', operator: 'GTE', value: '1990-01-01' },
    patientFilteredPopulationIDs: patient.records
      .filter(record => patientIDs.slice(0, 2).includes(record.id) && record.birthDate >= '1990-01-01')
      .map(record => record.id)
      .sort(),
    researchStudyRows,
    diagnosticReportIDs,
    disconnectedResourceTypes: ['ResearchStudy'],
    connectedPreservationRelationship: {
      fromResourceType: 'Patient',
      relationship: 'subject_Patient',
      toResourceType: 'Observation',
    },
    connectedReplacementRelationship: {
      fromResourceType: 'ResearchStudy',
      relationship: 'result_DiagnosticReport',
      toResourceType: 'DiagnosticReport',
    },
  };
};
