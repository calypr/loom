import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

export const CDA_SAVED_ROUTE_EDIT_SCOPE = Object.freeze({
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
  specimenId: 'b7cad184-db67-5542-a975-10fffa3e89e7',
  specimenKey: 'Specimen/g_00003b7dca775d32e82a19f3c3c1227d234ca8d3486df068194137e93b1f796b',
  patientId: 'afcfb15e-7617-5691-ae2c-ab675322fb33',
  patientKey: 'Patient/g_1e07dda19360e97fd8380d3a631c4eb6df35aaa9cf0494415312c0e4c06e9895',
});

export const CDA_SAVED_ROUTE_EDIT_FIXTURE_PATH = fileURLToPath(new URL(
  '../../../testdata/cda-saved-related-route-edit/epoch126-raw-oracle.json', import.meta.url,
));

const sortByIdentity = rows => [...rows].sort((left, right) => left._id.localeCompare(right._id));
const pairsForRoute = (specimenId, observations) => observations
  .map(observation => [specimenId, observation.id])
  .sort((left, right) => left[0].localeCompare(right[0]) || left[1].localeCompare(right[1]));

export function projectVisibleRoutePairs(grid, rootLabel, relatedLabel, scalarize) {
  const rootIndex = grid.headers.indexOf(rootLabel);
  const relatedIndex = grid.headers.indexOf(relatedLabel);
  return grid.rows.map(row => [row[rootIndex], scalarize(row[relatedIndex])]);
}

export function readCdaSavedRelatedRouteOracle({ fixturePath = CDA_SAVED_ROUTE_EDIT_FIXTURE_PATH } = {}) {
  const value = JSON.parse(readFileSync(fixturePath, 'utf8'));
  assert.equal(value.version, 1, 'CDA route oracle must use the supported snapshot version.');
  assert.equal(value.scope?.project, CDA_SAVED_ROUTE_EDIT_SCOPE.project);
  assert.equal(value.scope?.generation, CDA_SAVED_ROUTE_EDIT_SCOPE.generation);
  assert.equal(value.scope?.specimen?.id, CDA_SAVED_ROUTE_EDIT_SCOPE.specimenId);
  assert.equal(value.scope?.specimen?._id, CDA_SAVED_ROUTE_EDIT_SCOPE.specimenKey);
  assert.equal(value.scope?.patient?.id, CDA_SAVED_ROUTE_EDIT_SCOPE.patientId);
  assert.equal(value.scope?.patient?._id, CDA_SAVED_ROUTE_EDIT_SCOPE.patientKey);
  assert.equal(value.routes?.direct?.displayLabel, 'Specimen <-[specimen]- Observation');
  assert.equal(value.routes?.viaPatient?.displayLabel, 'Specimen -[subject]-> Patient <-[subject]- Observation');
  assert.deepEqual(value.routes?.direct?.observationIds, ['35cfec85-56e8-5257-af99-2e9345be2011']);
  assert.equal(value.routes?.viaPatient?.observationIds?.length, 31);
  assert.equal(value.observations?.length, 31);
  assert.equal(value.summary?.distinctObservationIdentityCount, 31);
  assert.equal(value.summary?.nonnullSpecimenReferenceCount, 29);
  assert.equal(value.summary?.distinctNonnullSpecimenReferenceCount, 29);
  assert.equal(value.summary?.nullOrMissingSpecimenReferenceCount, 2);
  assert.equal(value.provenance?.kind, 'retained raw Arango witness from epoch126');
  assert.match(value.provenance?.attachmentSha256 ?? '', /^[a-f0-9]{64}$/);

  const observations = sortByIdentity(value.observations);
  assert.equal(new Set(observations.map(row => row._id)).size, observations.length,
    'Raw CDA route oracle must retain each terminal Observation identity once.');
  assert.deepEqual(observations.map(row => row.id).sort(), [...value.routes.viaPatient.observationIds].sort(),
    'Patient route IDs must bind the complete retained raw Observation set.');
  assert(observations.every(row => row.specimenReferenceState === (row.specimenReference === null ? 'null_or_missing' : 'present')),
    'Raw fixture must mark null/missing separately from present reference values.');
  assert.equal(observations.find(row => row.id === value.routes.direct.observationIds[0])?.specimenReference,
    'Specimen/b7cad184-db67-5542-a975-10fffa3e89e7',
    'Direct-route Observation must retain its exact raw reference to the pinned Specimen.');

  return {
    ...value,
    observations,
    routeA: {
      ...value.routes.direct,
      rows: pairsForRoute(value.scope.specimen.id,
        observations.filter(row => value.routes.direct.observationIds.includes(row.id))),
    },
    routeB: {
      ...value.routes.viaPatient,
      rows: pairsForRoute(value.scope.specimen.id, observations),
    },
  };
}

export function buildCdaSavedRelatedRouteQuery({ project, generation }) {
  assert.equal(project, CDA_SAVED_ROUTE_EDIT_SCOPE.project, 'Raw route query requires the pinned CDA project.');
  assert.equal(generation, CDA_SAVED_ROUTE_EDIT_SCOPE.generation, 'Raw route query requires the pinned CDA generation.');
  const scope = CDA_SAVED_ROUTE_EDIT_SCOPE;
  return `
LET specimen = DOCUMENT(${JSON.stringify(scope.specimenKey)})
FILTER specimen != null AND specimen.id == ${JSON.stringify(scope.specimenId)}
  AND specimen.project == ${JSON.stringify(project)}
  AND specimen.dataset_generation == ${JSON.stringify(generation)}
LET patientKeys = (
  FOR edge IN fhir_edge
    FILTER edge._from == specimen._id AND edge.label == "subject_Patient"
      AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
    COLLECT patientKey = edge._to
    RETURN patientKey
)
FILTER LENGTH(patientKeys) == 1 AND FIRST(patientKeys) == ${JSON.stringify(scope.patientKey)}
LET patient = DOCUMENT(FIRST(patientKeys))
FILTER patient != null AND patient.id == ${JSON.stringify(scope.patientId)}
  AND patient.project == ${JSON.stringify(project)}
  AND patient.dataset_generation == ${JSON.stringify(generation)}
LET directObservations = (
  FOR edge IN fhir_edge
    FILTER edge._to == specimen._id AND STARTS_WITH(edge._from, "Observation/")
      AND edge.label == "specimen_Specimen" AND edge.project == ${JSON.stringify(project)}
      AND edge.dataset_generation == ${JSON.stringify(generation)}
    COLLECT observationKey = edge._from
    LET observation = DOCUMENT(observationKey)
    FILTER observation != null AND observation.project == ${JSON.stringify(project)}
      AND observation.dataset_generation == ${JSON.stringify(generation)}
      AND observation.payload.resourceType == "Observation"
      AND observation.payload.specimen.reference == CONCAT("Specimen/", specimen.id)
    SORT observation._id
    LIMIT 2
    RETURN { id: observation.id, _id: observation._id }
)
LET patientObservations = (
  FOR edge IN fhir_edge
    FILTER edge._to == patient._id AND STARTS_WITH(edge._from, "Observation/")
      AND edge.label == "subject_Patient" AND edge.project == ${JSON.stringify(project)}
      AND edge.dataset_generation == ${JSON.stringify(generation)}
    COLLECT observationKey = edge._from
    LET observation = DOCUMENT(observationKey)
    FILTER observation != null AND observation.project == ${JSON.stringify(project)}
      AND observation.dataset_generation == ${JSON.stringify(generation)}
      AND observation.payload.resourceType == "Observation"
      AND observation.payload.subject.reference == CONCAT("Patient/", patient.id)
    SORT observation._id
    LIMIT 32
    RETURN {
      id: observation.id,
      _id: observation._id,
      specimenReference: observation.payload.specimen.reference == null
        ? null
        : observation.payload.specimen.reference,
      specimenReferenceState: !HAS(observation.payload, "specimen")
        ? "missing"
        : !HAS(observation.payload.specimen, "reference")
          ? "missing"
          : observation.payload.specimen.reference == null
            ? "null"
            : "present"
    }
)
RETURN {
  specimen: { id: specimen.id, _id: specimen._id, patientReference: specimen.payload.subject.reference },
  patient: { id: patient.id, _id: patient._id, reference: CONCAT("Patient/", patient.id) },
  directObservations,
  patientObservations
}
`;
}

export function assertCdaSavedRelatedRouteRows(rows, oracle) {
  assert(Array.isArray(rows) && rows.length === 1, 'Raw CDA route query must return its exact single pinned witness.');
  const actual = rows[0];
  assert.deepEqual(actual.specimen, {
    id: oracle.scope.specimen.id,
    _id: oracle.scope.specimen._id,
    patientReference: oracle.scope.specimen.patientReference,
  }, 'Raw CDA route query changed the pinned Specimen or its Patient reference.');
  assert.deepEqual(actual.patient, oracle.scope.patient,
    'Raw CDA route query changed the exact Patient on the two-hop path.');
  const direct = sortByIdentity(actual.directObservations ?? []);
  const expectedDirect = oracle.observations.filter(row => oracle.routes.direct.observationIds.includes(row.id));
  assert.deepEqual(direct.map(row => [row.id, row._id]), expectedDirect.map(row => [row.id, row._id]),
    'Direct Specimen-to-Observation route differs from the raw witness.');
  const viaPatient = sortByIdentity(actual.patientObservations ?? []);
  assert.deepEqual(viaPatient.map(row => [row.id, row._id]), oracle.observations.map(row => [row.id, row._id]),
    'Patient route must contain the exact 31 raw Observation identities.');
  assert.deepEqual(viaPatient.map(row => row.specimenReference),
    oracle.observations.map(row => row.specimenReference),
    'Raw Patient-route specimen reference values differ from the epoch126 oracle.');
  assert(viaPatient.every(row => row.specimenReference === null
    ? ['null', 'missing'].includes(row.specimenReferenceState)
    : row.specimenReferenceState === 'present'),
  'Raw Patient-route must distinguish absent references from present values.');
  assert.equal(new Set(viaPatient.map(row => row._id)).size, 31);
  assert.equal(viaPatient.filter(row => row.specimenReferenceState === 'present').length, 29);
  assert.equal(viaPatient.filter(row => row.specimenReferenceState !== 'present').length, 2);
  return {
    routeA: { rows: oracle.routeA.rows, observationIds: direct.map(row => row.id) },
    routeB: { rows: oracle.routeB.rows, observationIds: viaPatient.map(row => row.id) },
    rawReferenceCounts: {
      present: 29,
      nullOrMissing: 2,
      null: viaPatient.filter(row => row.specimenReferenceState === 'null').length,
      missing: viaPatient.filter(row => row.specimenReferenceState === 'missing').length,
    },
  };
}
