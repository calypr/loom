import assert from 'node:assert/strict';

export const MAX_CDA_STARTING_COLLECTION_OBSERVATION_SCAN = 2_000;
export const MAX_CDA_STARTING_COLLECTION_WITNESSES = 4_000;

const queryValue = value => JSON.stringify(value);
const requiredText = (value, name) => {
  assert.equal(typeof value, 'string', `${name} must be a string`);
  assert.notEqual(value.trim(), '', `${name} must be non-empty`);
};
const documentID = (value, collection, name) => {
  requiredText(value, name);
  assert.match(value, new RegExp(`^${collection}/[^/]+$`), `${name} must be an exact ${collection} document ID`);
};

const resourceProjection = name => `{
  _id:${name}._id,id:${name}.id,project:${name}.project,generation:${name}.dataset_generation,
  resourceType:${name}.resourceType,payloadResourceType:${name}.payload.resourceType,
  fieldPresent:HAS(${name}.payload,"id"),fieldValue:${name}.payload.id
}`;

const edgeProjection = name => `{
  _id:${name}._id,_from:${name}._from,_to:${name}._to,
  project:${name}.project,generation:${name}.dataset_generation,
  fromType:${name}.from_type,toType:${name}.to_type,label:${name}.label
}`;

export const cdaStartingCollectionReplacementScanQuery = ({ project, generation }) => {
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  return `FOR observation IN Observation
    FILTER observation.project == ${queryValue(project)}
      AND observation.dataset_generation == ${queryValue(generation)}
      AND observation.resourceType == "Observation"
      AND observation.payload.resourceType == "Observation"
      AND IS_STRING(observation.id) AND LENGTH(TRIM(observation.id)) > 0
    SORT observation._id
    LIMIT ${MAX_CDA_STARTING_COLLECTION_OBSERVATION_SCAN}
    FOR edge IN fhir_edge
      FILTER edge._from == observation._id
        AND edge.from_type == "Observation" AND edge.to_type == "Patient"
        AND edge.label == "subject_Patient"
        AND edge.project == ${queryValue(project)}
        AND edge.dataset_generation == ${queryValue(generation)}
      LET patient = DOCUMENT(edge._to)
      FILTER patient != null
        AND patient.resourceType == "Patient"
        AND patient.payload.resourceType == "Patient"
        AND patient.project == ${queryValue(project)}
        AND patient.dataset_generation == ${queryValue(generation)}
        AND IS_STRING(patient.id) AND LENGTH(TRIM(patient.id)) > 0
      SORT observation._id, edge._id
      LIMIT ${MAX_CDA_STARTING_COLLECTION_WITNESSES}
      RETURN {observation:${resourceProjection('observation')},edge:${edgeProjection('edge')},patient:${resourceProjection('patient')}}`;
};

export const cdaStartingCollectionReplacementRereadQuery = ({
  project,
  generation,
  patientDocumentIDs,
  candidateObservationDocumentID,
  edgeDocumentID,
  candidatePatientDocumentID,
}) => {
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  assert(Array.isArray(patientDocumentIDs) && patientDocumentIDs.length === 2,
    'Exact Patient reread requires two selected Patient document IDs');
  patientDocumentIDs.forEach((id, index) => documentID(id, 'Patient', `patientDocumentIDs[${index}]`));
  assert.equal(new Set(patientDocumentIDs).size, 2, 'Exact Patient reread requires distinct document IDs');
  documentID(candidateObservationDocumentID, 'Observation', 'candidateObservationDocumentID');
  documentID(edgeDocumentID, 'fhir_edge', 'edgeDocumentID');
  documentID(candidatePatientDocumentID, 'Patient', 'candidatePatientDocumentID');
  assert(patientDocumentIDs.includes(candidatePatientDocumentID),
    'Candidate Observation subject must be one of the original Patient witnesses');

  return `LET patients = (
    FOR patient IN Patient
      FILTER patient._id IN ${queryValue(patientDocumentIDs)}
        AND patient.resourceType == "Patient" AND patient.payload.resourceType == "Patient"
        AND patient.project == ${queryValue(project)}
        AND patient.dataset_generation == ${queryValue(generation)}
      SORT patient._id
      RETURN ${resourceProjection('patient')}
  )
  FOR observation IN Observation
    FILTER observation._id == ${queryValue(candidateObservationDocumentID)}
      AND observation.resourceType == "Observation" AND observation.payload.resourceType == "Observation"
      AND observation.project == ${queryValue(project)}
      AND observation.dataset_generation == ${queryValue(generation)}
    FOR edge IN fhir_edge
      FILTER edge._id == ${queryValue(edgeDocumentID)}
        AND edge._from == observation._id
        AND edge._to == ${queryValue(candidatePatientDocumentID)}
        AND edge.from_type == "Observation" AND edge.to_type == "Patient"
        AND edge.label == "subject_Patient"
        AND edge.project == ${queryValue(project)}
        AND edge.dataset_generation == ${queryValue(generation)}
      RETURN {patients,candidateObservation:${resourceProjection('observation')},edge:${edgeProjection('edge')}}`;
};

export class CdaStartingCollectionReplacementWitnessUnavailable extends Error {
  constructor(message, evidence) {
    super(message);
    this.name = 'CdaStartingCollectionReplacementWitnessUnavailable';
    this.evidence = evidence;
  }
}

const validateResource = (row, resourceType, { project, generation }, name) => {
  assert(row && typeof row === 'object' && !Array.isArray(row), `${name} must be a raw resource projection`);
  documentID(row._id, resourceType, `${name}._id`);
  requiredText(row.id, `${name}.id`);
  assert.equal(row.project, project, `${name} escaped the exact project scope`);
  assert.equal(row.generation, generation, `${name} escaped the exact generation scope`);
  assert.equal(row.resourceType, resourceType, `${name} has the wrong resource type`);
  assert.equal(row.payloadResourceType, resourceType, `${name} payload has the wrong resource type`);
  assert.equal(row.fieldPresent, true, `${name} payload must contain its raw FHIR id`);
  assert.equal(row.fieldValue, row.id, `${name} payload id must match the top-level FHIR id`);
  return {
    _id: row._id,
    id: row.id,
    project: row.project,
    generation: row.generation,
    resourceType: row.resourceType,
    payloadResourceType: row.payloadResourceType,
    fieldPresent: row.fieldPresent,
    fieldValue: row.fieldValue,
  };
};

const validateEdge = (edge, observation, patient, scope) => {
  assert(edge && typeof edge === 'object' && !Array.isArray(edge), 'subject_Patient edge must be a raw edge projection');
  documentID(edge._id, 'fhir_edge', 'edge._id');
  assert.equal(edge._from, observation._id, 'subject_Patient edge must originate at the exact Observation document');
  assert.equal(edge._to, patient._id, 'subject_Patient edge must terminate at the exact Patient document');
  assert.equal(edge.project, scope.project, 'subject_Patient edge escaped the exact project scope');
  assert.equal(edge.generation, scope.generation, 'subject_Patient edge escaped the exact generation scope');
  assert.equal(edge.fromType, 'Observation', 'subject_Patient edge has the wrong source type');
  assert.equal(edge.toType, 'Patient', 'subject_Patient edge has the wrong target type');
  assert.equal(edge.label, 'subject_Patient', 'raw relationship edge has the wrong label');
  return {
    _id: edge._id,
    _from: edge._from,
    _to: edge._to,
    project: edge.project,
    generation: edge.generation,
    fromType: edge.fromType,
    toType: edge.toType,
    label: edge.label,
  };
};

const compareID = (left, right) => left.localeCompare(right);
const addConsistentDocument = (documents, byFHIRID, row, resourceType) => {
  const existing = documents.get(row._id);
  if (existing) assert.deepEqual(row, existing, `${resourceType} document changed across raw edge witnesses`);
  else documents.set(row._id, row);
  const existingDocumentID = byFHIRID.get(row.id);
  if (existingDocumentID) assert.equal(existingDocumentID, row._id,
    `${resourceType} FHIR id maps to more than one raw document`);
  else byFHIRID.set(row.id, row._id);
};

export function prepareCdaStartingCollectionReplacementOracle(rawRows, { project, generation }) {
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  assert(Array.isArray(rawRows), 'Bounded raw CDA witness scan must return rows');
  assert(rawRows.length <= MAX_CDA_STARTING_COLLECTION_WITNESSES,
    `Bounded raw CDA witness scan exceeded its ${MAX_CDA_STARTING_COLLECTION_WITNESSES}-row limit`);

  const scope = { project, generation };
  const patients = new Map();
  const patientIDs = new Map();
  const observations = new Map();
  const observationIDs = new Map();
  const edges = new Set();
  const endpointPairs = new Set();
  const observationSubjects = new Map();
  const witnesses = [];

  for (const [index, rawRow] of rawRows.entries()) {
    const observation = validateResource(rawRow?.observation, 'Observation', scope, `rows[${index}].observation`);
    const patient = validateResource(rawRow?.patient, 'Patient', scope, `rows[${index}].patient`);
    const edge = validateEdge(rawRow?.edge, observation, patient, scope);
    assert(!edges.has(edge._id), `Bounded raw CDA witness scan repeated edge ${edge._id}`);
    edges.add(edge._id);
    const pair = `${observation._id}\u0000${patient._id}`;
    assert(!endpointPairs.has(pair), `Bounded raw CDA witness scan repeated Observation–Patient edge ${pair}`);
    endpointPairs.add(pair);
    const priorSubject = observationSubjects.get(observation._id);
    if (priorSubject) assert.equal(priorSubject, patient._id,
      `Observation ${observation.id} has conflicting Patient subjects in the bounded raw witness`);
    else observationSubjects.set(observation._id, patient._id);

    addConsistentDocument(observations, observationIDs, observation, 'Observation');
    addConsistentDocument(patients, patientIDs, patient, 'Patient');
    witnesses.push({ observation, patient, edge });
  }

  const orderedPatients = [...patients.values()].sort((left, right) => compareID(left.id, right.id));
  if (orderedPatients.length < 2) {
    const evidence = {
      project,
      generation,
      scanLimitObservations: MAX_CDA_STARTING_COLLECTION_OBSERVATION_SCAN,
      witnessLimit: MAX_CDA_STARTING_COLLECTION_WITNESSES,
      returnedEdgeWitnesses: rawRows.length,
      distinctPatients: orderedPatients.length,
      requiredPatients: 2,
      reason: 'fewer than two distinct, scoped Patient documents are reachable through Observation.subject_Patient edges',
    };
    throw new CdaStartingCollectionReplacementWitnessUnavailable(
      'The bounded raw CDA witness cannot provide two distinct Patient subjects for starting-collection replacement.', evidence);
  }

  const selectedPatients = orderedPatients.slice(0, 2);
  const firstPatient = selectedPatients[0];
  const candidateWitness = witnesses
    .filter(witness => witness.patient._id === firstPatient._id)
    .sort((left, right) => compareID(left.observation.id, right.observation.id) || compareID(left.observation._id, right.observation._id))[0];
  assert(candidateWitness, 'The first sorted Patient must have an exact Observation.subject_Patient witness');

  const originalPatientIDs = selectedPatients.map(patient => patient.id);
  const originalPatientReferences = selectedPatients.map(patient => ({
    project, generation, resourceType: 'Patient', id: patient.id,
  }));
  const candidateObservationReference = {
    project, generation, resourceType: 'Observation', id: candidateWitness.observation.id,
  };
  const candidatePatientReference = {
    project, generation, resourceType: 'Patient', id: firstPatient.id,
  };
  const selectedWitnesses = {
    originalPatients: selectedPatients,
    candidateObservation: candidateWitness.observation,
    edge: candidateWitness.edge,
  };

  return {
    scope,
    scanLimitObservations: MAX_CDA_STARTING_COLLECTION_OBSERVATION_SCAN,
    witnessLimit: MAX_CDA_STARTING_COLLECTION_WITNESSES,
    scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
    returnedEdgeWitnesses: rawRows.length,
    originalPatientIDs,
    originalPatientReferences,
    candidateObservationID: candidateWitness.observation.id,
    candidateObservationReference,
    candidatePatientID: firstPatient.id,
    candidatePatientReference,
    witnesses: selectedWitnesses,
    expectedGroupRows: {
      originalPatients: originalPatientIDs.map(id => ({ id, count: 1 })),
      candidateObservationSubjectPatients: [{ id: firstPatient.id, count: 1 }],
    },
  };
}

export function assertCdaStartingCollectionReplacementReread(actualRows, oracle) {
  assert(Array.isArray(actualRows) && actualRows.length === 1,
    'Exact CDA starting-collection reread must return one combined witness');
  assert(oracle?.witnesses?.originalPatients?.length === 2,
    'Exact CDA starting-collection reread requires two original Patient witnesses');
  const actual = actualRows[0];
  assert(actual && Array.isArray(actual.patients), 'Exact CDA starting-collection reread omitted Patient resources');
  const patients = actual.patients.map(row => validateResource(row, 'Patient', oracle.scope, 'reread.patients[]'))
    .sort((left, right) => compareID(left._id, right._id));
  const candidateObservation = validateResource(actual.candidateObservation, 'Observation', oracle.scope,
    'reread.candidateObservation');
  const firstPatient = oracle.witnesses.originalPatients.find(patient => patient.id === oracle.candidatePatientID);
  assert(firstPatient, 'Oracle candidate Patient must be one of the original Patient witnesses');
  const edge = validateEdge(actual.edge, candidateObservation, firstPatient, oracle.scope);
  const expectedPatients = [...oracle.witnesses.originalPatients].sort((left, right) => compareID(left._id, right._id));
  assert.deepEqual(patients, expectedPatients, 'Exact selected Patient reread differs from the raw bounded witness');
  assert.deepEqual(candidateObservation, oracle.witnesses.candidateObservation,
    'Exact candidate Observation reread differs from the raw bounded witness');
  assert.deepEqual(edge, oracle.witnesses.edge, 'Exact subject_Patient edge reread differs from the raw bounded witness');
  return { exact: true, patients: patients.length, candidateObservationID: candidateObservation.id, ...oracle.scope };
}

export function bindCdaStartingCollectionReplacementSelectionRevisions(oracle, {
  patientRevisionId,
  observationRevisionId,
}) {
  assert(oracle?.scope && Array.isArray(oracle.originalPatientReferences) && oracle.originalPatientReferences.length === 2,
    'Starting-collection selection revision binding requires the exact two-Patient oracle');
  requiredText(patientRevisionId, 'patientRevisionId');
  requiredText(observationRevisionId, 'observationRevisionId');
  return {
    scope: { ...oracle.scope },
    sources: {
      patient: {
        resourceType: 'Patient',
        revisionId: patientRevisionId,
        references: oracle.originalPatientReferences.map(reference => ({ ...reference })),
        expectedGroupRows: oracle.expectedGroupRows.originalPatients.map(row => ({ ...row })),
      },
      observation: {
        resourceType: 'Observation',
        revisionId: observationRevisionId,
        references: [{ ...oracle.candidateObservationReference }],
        expectedGroupRows: oracle.expectedGroupRows.candidateObservationSubjectPatients.map(row => ({ ...row })),
      },
    },
    relation: {
      label: 'subject_Patient',
      from: { ...oracle.candidateObservationReference },
      to: { ...oracle.candidatePatientReference },
      edgeDocumentID: oracle.witnesses.edge._id,
    },
  };
}
