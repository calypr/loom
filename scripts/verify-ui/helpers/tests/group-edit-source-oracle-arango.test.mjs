import assert from 'node:assert/strict';
import test from 'node:test';
import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { buildArangoShellInvocation } from '../owned-arangosh-command.mjs';
import { assertCdaNoAuthRuntime } from '../cda-no-auth-runtime.mjs';
import { assertOwnedCdaTarget } from '../owned-cda-target.mjs';
import { sanitizePayload } from '../playwright-browser.mjs';

const arangoContainer = process.env.LOOM_CDA_ARANGO_CONTAINER;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const enabled = process.env.LOOM_GROUP_EDIT_AQL_FIXTURE === '1';
const marker = '__GROUP_EDIT_TYPED_ORACLE_FIXTURE__';

const workflowSource = readFileSync(new URL('../../workflows/verify-cda-group-edit-before-related-column-browser.mjs', import.meta.url), 'utf8');
const sourceQueryTemplate = workflowSource.match(/const sourceQuery = `([\s\S]*?)`;/)?.[1];
assert(sourceQueryTemplate, 'Group-edit workflow must retain its bounded source oracle query');

test('owned API runtime proof survives CDA report sanitization', () => {
  const sanitized = sanitizePayload({ ownedApiRuntimeProof: {
    apiContainer:'loom-api', executable:'arango-fhir-server', noAuthArgumentVerified:true,
  } });
  assert.equal(sanitized.ownedApiRuntimeProof.noAuthArgumentVerified, true);
});

test('Group-edit source AQL selects the literal typed path and rejects wrong endpoints/documents', {
  skip: !enabled ? 'Set LOOM_GROUP_EDIT_AQL_FIXTURE=1 with the validated owned CDA environment' : false,
}, async t => {
  assert(arangoContainer, 'The validated owned Arango container is required');
  assert(apiContainer, 'The validated owned API container is required');
  assert.equal(process.env.LOOM_CDA_NO_AUTH, '1', 'The owned local fixture must explicitly opt into no-auth mode');
  const validatedTarget = await assertOwnedCdaTarget({
    project:process.env.LOOM_CDA_PROJECT,
    apiOrigin:process.env.LOOM_CDA_API_ORIGIN,
    uiOrigin:process.env.LOOM_CDA_UI_ORIGIN,
    apiContainer,
    composeProject:process.env.LOOM_CDA_COMPOSE_PROJECT,
    sourceRoot:process.env.LOOM_CDA_SOURCE_ROOT,
    arangoContainer,
    clickhouseContainer:process.env.LOOM_CDA_CLICKHOUSE_CONTAINER,
  });
  assert.equal(validatedTarget.apiContainer, apiContainer);
  assert.equal(validatedTarget.arangoContainer, arangoContainer);
  const noAuthEvidence = assertCdaNoAuthRuntime({ apiContainer:validatedTarget.apiContainer });
  assert.equal(noAuthEvidence.noAuthArgumentVerified, true);
  assert.equal(sanitizePayload({ ownedApiRuntimeProof:noAuthEvidence }).ownedApiRuntimeProof.noAuthArgumentVerified, true,
    'Sanitized workflow reports must retain the positive runtime proof boolean');

  const suffix = randomUUID().replaceAll('-', '');
  const collections = {
    specimens: `qa_group_edit_specimens_${suffix}`,
    patients: `qa_group_edit_patients_${suffix}`,
    observations: `qa_group_edit_observations_${suffix}`,
    edges: `qa_group_edit_edges_${suffix}`,
  };
  const project = `loom_dev_group_edit_aql_${suffix}`;
  const generation = 'cda-fhir-v1';
  let query = sourceQueryTemplate
    .replaceAll('${JSON.stringify(project)}', JSON.stringify(project))
    .replaceAll('${JSON.stringify(generation)}', JSON.stringify(generation));
  query = query
    .replaceAll('FOR candidate IN Specimen', `FOR candidate IN ${collections.specimens}`)
    .replaceAll('FOR e IN fhir_edge', `FOR e IN ${collections.edges}`);
  assert(!query.includes('${JSON.stringify('), 'The exact fixture project/generation must be bound into the query');
  assert.equal((query.match(new RegExp(`FOR candidate IN ${collections.specimens}`, 'g')) ?? []).length, 1);
  assert.equal((query.match(new RegExp(`FOR e IN ${collections.edges}`, 'g')) ?? []).length, 2);

  const variants = {
    exact: query,
    withoutSpecimenPayloadType: query.replace(' AND candidate.payload.resourceType == "Specimen"', ''),
    withoutFirstHopEdgeTypes: query.replace(' AND e.from_type == "Specimen" AND e.to_type == "Patient"', ''),
    withoutPatientPayloadType: query.replace(' AND p.payload.resourceType == "Patient"', ''),
    withoutObservationPayloadType: query.replace(' AND o.payload.resourceType == "Observation"', ''),
    withoutSecondHopEdgeTypes: query.replace(' AND e.from_type == "Observation" AND e.to_type == "Patient"', ''),
  };
  for (const [name, variant] of Object.entries(variants)) {
    if (name !== 'exact') assert.notEqual(variant, query, `Negative query variant ${name} must remove one typed predicate`);
  }

  const script = `(() => {
  const marker = ${JSON.stringify(marker)};
  const names = ${JSON.stringify(collections)};
  const project = ${JSON.stringify(project)};
  const generation = ${JSON.stringify(generation)};
  const queries = ${JSON.stringify(variants)};
  const created = [];
  let cleanupErrors = 0;
  let phase = 'create-collections';
  let output = {ok:false};
  const safeNumber = value => Number.isFinite(value) ? value : null;
  const summarize = rows => {
    const row = rows[0] ?? null;
    return {
      resultCount: rows.length,
      specimenId: row?.specimen?.id ?? null,
      specimenType: row?.specimen?.resourceType ?? null,
      patientType: row?.patient?.resourceType ?? null,
      patientCollection: row?.patient?._id?.split('/')[0] ?? null,
      patientGeneration: row?.patient?.generation ?? null,
      observationIDs: row?.observations?.map(value => value.id) ?? [],
      observationTypes: row?.observations?.map(value => value.resourceType) ?? [],
      observationCollections: row?.observations?.map(value => value._id.split('/')[0]) ?? [],
      observationGenerations: row?.observations?.map(value => value.generation) ?? [],
      observationStatuses: row?.observations?.map(value => value.status) ?? [],
    };
  };
  const run = (name, query) => {
    phase = 'query:' + name;
    return db._query(query, {}, {maxRuntime:2, memoryLimit:16777216}).toArray();
  };
  try {
    for (const name of [names.specimens, names.patients, names.observations]) {
      db._create(name);
      created.push(name);
    }
    db._createEdgeCollection(names.edges);
    created.push(names.edges);
    const specimens = db._collection(names.specimens);
    const patients = db._collection(names.patients);
    const observations = db._collection(names.observations);
    const edges = db._collection(names.edges);
    const putDoc = (collection, key, id, resourceType, payloadType, status, documentProject = project, documentGeneration = generation) => collection.insert({
      _key:key, id, project:documentProject, dataset_generation:documentGeneration, resourceType,
      payload:{resourceType:payloadType, ...(status === undefined ? {} : {status})},
    });
    phase = 'insert-documents-and-edges';
    const wrongSpecimen = putDoc(specimens, 'wrong_payload', '00-wrong-payload-specimen', 'Specimen', 'Patient');
    const validSpecimen = putDoc(specimens, 'valid', 'valid-specimen', 'Specimen', 'Specimen');
    const validPatient = putDoc(patients, 'valid', 'valid-patient', 'Patient', 'Patient');
    const wrongPatientPayload = putDoc(patients, 'wrong_payload', 'wrong-payload-patient', 'Patient', 'Observation');
    const wrongPatientEdge = putDoc(patients, 'wrong_edge', 'wrong-edge-patient', 'Patient', 'Patient');
    const crossCollectionPatient = putDoc(observations, 'cross_patient', 'cross-collection-patient', 'Patient', 'Patient');
    const crossCollectionObservation = putDoc(patients, 'cross_observation', 'cross-collection-observation', 'Observation', 'Observation', 'amended');
    const wrongProjectObservation = putDoc(observations, 'wrong_project', 'wrong-project-observation', 'Observation', 'Observation', 'final', 'unrelated-project');
    const wrongGenerationObservation = putDoc(observations, 'wrong_generation', 'wrong-generation-observation', 'Observation', 'Observation', 'final', project, 'unrelated-generation');
    const wrongProjectEdgeObservation = putDoc(observations, 'wrong_project_edge', 'wrong-project-edge-observation', 'Observation', 'Observation', 'final');
    const observationA = putDoc(observations, 'a', 'observation-a', 'Observation', 'Observation', 'final');
    const observationB = putDoc(observations, 'b', 'observation-b', 'Observation', 'Observation', 'preliminary');
    putDoc(observations, 'wrong_payload', 'observation-wrong-payload', 'Observation', 'Patient', 'final');
    putDoc(observations, 'wrong_edge', 'observation-wrong-edge', 'Observation', 'Observation', 'final');
    const edge = (key, from, to, fromType, toType, edgeProject = project) => edges.insert({
      _key:key, _from:from, _to:to, from_type:fromType, to_type:toType,
      label:'subject_Patient', project:edgeProject, dataset_generation:generation,
    });
    edge('s_valid', validSpecimen._id, crossCollectionPatient._id, 'Specimen', 'Patient');
    edge('s_duplicate', validSpecimen._id, crossCollectionPatient._id, 'Specimen', 'Patient');
    edge('s_wrong_type', validSpecimen._id, wrongPatientEdge._id, 'Observation', 'Patient');
    edge('s_wrong_payload', validSpecimen._id, wrongPatientPayload._id, 'Specimen', 'Patient');
    edge('s_wrong_root', wrongSpecimen._id, validPatient._id, 'Specimen', 'Patient');
    edge('o_a', observationA._id, crossCollectionPatient._id, 'Observation', 'Patient');
    edge('o_a_duplicate', observationA._id, crossCollectionPatient._id, 'Observation', 'Patient');
    edge('o_b', observationB._id, crossCollectionPatient._id, 'Observation', 'Patient');
    edge('o_cross_collection', crossCollectionObservation._id, crossCollectionPatient._id, 'Observation', 'Patient');
    edge('o_valid_patient_a', observationA._id, validPatient._id, 'Observation', 'Patient');
    edge('o_valid_patient_b', observationB._id, validPatient._id, 'Observation', 'Patient');
    edge('o_wrong_payload', names.observations + '/wrong_payload', crossCollectionPatient._id, 'Observation', 'Patient');
    edge('o_wrong_type', names.observations + '/wrong_edge', crossCollectionPatient._id, 'Specimen', 'Patient');
    edge('o_missing_document', names.observations + '/missing', crossCollectionPatient._id, 'Observation', 'Patient');
    edge('o_wrong_project_document', wrongProjectObservation._id, crossCollectionPatient._id, 'Observation', 'Patient');
    edge('o_wrong_generation_document', wrongGenerationObservation._id, crossCollectionPatient._id, 'Observation', 'Patient');
    edge('o_wrong_project_edge', wrongProjectEdgeObservation._id, crossCollectionPatient._id, 'Observation', 'Patient', 'unrelated-project');
    output = {
      ok:true,
      exact:summarize(run('exact', queries.exact)),
      withoutSpecimenPayloadType:summarize(run('withoutSpecimenPayloadType', queries.withoutSpecimenPayloadType)),
      withoutFirstHopEdgeTypes:summarize(run('withoutFirstHopEdgeTypes', queries.withoutFirstHopEdgeTypes)),
      withoutPatientPayloadType:summarize(run('withoutPatientPayloadType', queries.withoutPatientPayloadType)),
      withoutObservationPayloadType:summarize(run('withoutObservationPayloadType', queries.withoutObservationPayloadType)),
      withoutSecondHopEdgeTypes:summarize(run('withoutSecondHopEdgeTypes', queries.withoutSecondHopEdgeTypes)),
    };
  } catch (error) {
    output = {ok:false, phase, errorName:typeof error?.name === 'string' ? error.name : null,
      errorNum:safeNumber(error?.errorNum), errorCode:safeNumber(error?.code)};
  } finally {
    for (const name of created.reverse()) {
      try { db._drop(name); } catch (_) { cleanupErrors++; }
    }
  }
  print(marker + JSON.stringify({...output, cleanupErrors}));
})();`;
  const invocation = buildArangoShellInvocation({ container: arangoContainer, database: 'loom_dev', script });
  const result = spawnSync(invocation.command, invocation.args, { encoding: 'utf8', timeout: 15_000, maxBuffer: 2 * 1024 * 1024 });
  const resultLine = String(result.stdout ?? '').split(/\r?\n/).find((line) => line.includes(marker));
  assert.equal(result.status, 0, JSON.stringify({ status:result.status, signal:result.signal ?? null,
    errorCode:result.error?.code ?? null, stdoutBytes:Buffer.byteLength(result.stdout ?? ''), stderrBytes:Buffer.byteLength(result.stderr ?? '') }));
  assert(resultLine, 'The owned AQL fixture must return its sanitized result marker');
  const actual = JSON.parse(resultLine.slice(resultLine.indexOf(marker) + marker.length));
  const safeFailure = { phase:actual.phase ?? null, errorName:actual.errorName ?? null,
    errorNum:actual.errorNum ?? null, errorCode:actual.errorCode ?? null, cleanupErrors:actual.cleanupErrors ?? null };
  assert.equal(actual.ok, true, 'Typed oracle fixture failed: ' + JSON.stringify(safeFailure));
  assert.equal(actual.cleanupErrors, 0, 'Every owned temporary collection must be removed: ' + JSON.stringify(safeFailure));
  t.diagnostic(JSON.stringify({ exact:actual.exact, cleanupErrors:actual.cleanupErrors,
    negativeResultCounts:Object.fromEntries(['withoutSpecimenPayloadType', 'withoutFirstHopEdgeTypes',
      'withoutPatientPayloadType', 'withoutObservationPayloadType', 'withoutSecondHopEdgeTypes']
      .map(name => [name, actual[name]?.resultCount ?? null])) }));
  assert.deepEqual(actual.exact, {
    resultCount:1,
    specimenId:'valid-specimen',
    specimenType:'Specimen',
    patientType:'Patient',
    patientCollection:collections.observations,
    patientGeneration:generation,
    observationIDs:['cross-collection-observation','observation-a','observation-b'],
    observationTypes:['Observation','Observation','Observation'],
    observationCollections:[collections.patients,collections.observations,collections.observations],
    observationGenerations:[generation,generation,generation],
    observationStatuses:['amended','final','preliminary'],
  });
  assert.equal(actual.withoutSpecimenPayloadType.specimenId, '00-wrong-payload-specimen');
  assert.equal(actual.withoutFirstHopEdgeTypes.resultCount, 0);
  assert.equal(actual.withoutPatientPayloadType.resultCount, 0);
  assert.equal(actual.withoutObservationPayloadType.observationIDs.length, 4);
  assert.equal(actual.withoutSecondHopEdgeTypes.observationIDs.length, 4);
});
