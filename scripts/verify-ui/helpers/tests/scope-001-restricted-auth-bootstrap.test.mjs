import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import {
  createScope001SyntheticBearer,
  scope001LoadedAuthorizationWitnessAQL,
  scope001AuthorizationPathMutationAQL,
  seedAndScopeScope001Fixture,
  scope001FenceResponse,
  SCOPE001_ALLOWED_RESOURCE_PATH,
} from '../scope-001-restricted-auth-bootstrap.mjs';
import {
  SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
  SCOPE001_BLOCKED_AUTH_RESOURCE_PATH,
  SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID,
  SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID,
  SCOPE001_RESTRICTED_AUTH_PATIENT_ID,
} from '../scope-001-restricted-auth-oracle.mjs';
import { assertScope001RestrictedAuthRuntime } from '../scope-001-restricted-auth-runtime.mjs';

const claimsFrom = bearer => JSON.parse(Buffer.from(bearer.split('.')[1], 'base64url').toString('utf8'));

function fakeArangoValidateBindVars(query, bindVars) {
  const referenced = [...new Set([...query.matchAll(/@([A-Za-z_][A-Za-z0-9_]*)/g)].map(([, name]) => name))].sort();
  const supplied = Object.keys(bindVars).sort();
  const undeclared = supplied.find(name => !referenced.includes(name));
  if (undeclared) throw Object.assign(new Error(`bind parameter '${undeclared}' was not declared in the query`), { errorNum: 1552 });
  const missing = referenced.find(name => !supplied.includes(name));
  if (missing) throw new Error(`missing bind parameter: ${missing}`);
}

test('restricted auth bootstrap emits a local bearer with a container-reachable issuer and exact Fence grant', () => {
  const issuer = 'http://host.docker.internal:49152';
  const bearer = createScope001SyntheticBearer(issuer, 'scope-001-test-user');
  const claims = claimsFrom(bearer);
  assert.equal(claims.iss, issuer);
  assert.equal(claims.sub, 'scope-001-test-user');

  const denied = scope001FenceResponse({
    method: 'GET', path: '/user', authorization: 'Bearer wrong-local-token', expectedAuthorization: `Bearer ${bearer}`,
  });
  assert.equal(denied.status, 401);
  assert.deepEqual(denied.body, { error: 'unauthorized' });

  const allowed = scope001FenceResponse({
    method: 'GET', path: '/user', authorization: `Bearer ${bearer}`, expectedAuthorization: `Bearer ${bearer}`,
  });
  assert.equal(allowed.status, 200);
  assert.deepEqual(allowed.body, {
    authz: {
      [SCOPE001_ALLOWED_RESOURCE_PATH]: [
        { method: 'read', service: '*' },
        { method: 'write', service: '*' },
      ],
    },
  });
  assert.deepEqual(scope001FenceResponse({
    method: 'POST', path: '/user', authorization: `Bearer ${bearer}`, expectedAuthorization: `Bearer ${bearer}`,
  }), { status: 404, body: undefined });
});

test('synthetic bearer requires an origin-only local issuer', () => {
  assert.throws(() => createScope001SyntheticBearer('https://identity.example'), /must use HTTP/);
  assert.throws(() => createScope001SyntheticBearer('http://127.0.0.1:8123/user'), /must be an origin/);
});

test('auth fixture seeding refuses an existing generation before upload or Arango mutation', async () => {
  let calls = 0;
  await assert.rejects(seedAndScopeScope001Fixture({
    target: {
      project: 'loom_dev_scope001-auth', generation: 'scope001-auth-v1', noAuth: false,
      apiOrigin: 'http://127.0.0.1:8198', arangoContainer: 'owned-arango', arangoDatabase: 'loom_dev',
      datasetDir: '/unused',
    },
    bearer: 'synthetic-private-bearer-value-123456',
    fetchImpl: async () => {
      calls += 1;
      return new Response(JSON.stringify({ state: 'STAGED' }), { status: 200 });
    },
    execFileSyncImpl: () => { throw new Error('must not query Arango for an existing generation'); },
  }), /refusing to reuse an existing restricted-auth generation/);
  assert.equal(calls, 1);
});

test('owned Arango failures retain bounded status/error class without query, bind, or bearer text', async () => {
  const bearer = 'synthetic-private-bearer-value-123456';
  const bindVars = {
    project: 'loom_dev_scope001-auth',
    generation: 'scope001-auth-v1',
    patientId: SCOPE001_RESTRICTED_AUTH_PATIENT_ID,
    observationIds: [SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID, SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID],
  };
  let fetchCalls = 0;
  const error = Object.assign(new Error('Arango invocation failed'), {
    status: 23,
    stdout: Buffer.from(`error: true, errorNum: 1500, errorMessage: FOR branch rejected while evaluating the raw witness\n${scope001LoadedAuthorizationWitnessAQL}\n${JSON.stringify(bindVars)}\nBearer ${bearer}\n__SCOPE001_AUTH_RESULT__[{"id":"private-row"}]\n${'diagnostic '.repeat(200)}`),
    stderr: Buffer.from(`errorNum: 1500\nFOR branch rejected\npassword: ${bearer}\n{"token":"private diagnostic credential with spaces"}`),
  });
  await assert.rejects(seedAndScopeScope001Fixture({
    target: {
      project: 'loom_dev_scope001-auth', generation: 'scope001-auth-v1', noAuth: false,
      apiOrigin: 'http://127.0.0.1:8198', arangoContainer: 'owned-arango', arangoDatabase: 'loom_dev',
      datasetDir: fileURLToPath(new URL('../../../../testdata/scope-001-restricted-auth', import.meta.url)),
    },
    bearer,
    fetchImpl: async (_url, init = {}) => {
      fetchCalls += 1;
      if (fetchCalls === 1) return new Response(null, { status: 404 });
      return new Response(JSON.stringify({ state: 'STAGED' }), { status: 200 });
    },
    execFileSyncImpl: () => { throw error; },
  }), failure => {
    assert.match(failure.message, /initial raw witness Arango query failed/);
    assert.match(failure.message, /exit 23/);
    assert.match(failure.message, /errorNum 1500/);
    assert.match(failure.message, /stderr: errorNum: 1500 FOR branch rejected password: \[redacted\]/);
    assert.match(failure.message, /stdout: error: true, errorNum: 1500, errorMessage: FOR branch rejected while evaluating the raw witness/);
    assert.match(failure.message, /Bearer \[redacted\]/);
    assert.doesNotMatch(failure.message, /__SCOPE001_AUTH_RESULT__|private-row/);
    assert.equal(failure.message.includes(scope001LoadedAuthorizationWitnessAQL), false);
    assert.equal(failure.message.includes(JSON.stringify(bindVars)), false);
    assert.doesNotMatch(failure.message, /synthetic-private-bearer/);
    assert.doesNotMatch(failure.message, /private diagnostic credential with spaces/);
    assert(failure.message.length < 900, 'Arango diagnostics must remain bounded');
    return true;
  });
});

test('auth fixture path split changes only blocked target document after exact raw witnesses', async () => {
  const project = 'loom_dev_scope001-auth';
  const generation = 'scope001-auth-v1';
  const patient = {
    _id: 'Patient/scope-root', id: SCOPE001_RESTRICTED_AUTH_PATIENT_ID, resourceType: 'Patient',
    project, dataset_generation: generation, auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
  };
  const observations = blockedPath => [
    {
      _id: 'Observation/allowed', id: SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID, resourceType: 'Observation',
      project, dataset_generation: generation, auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
      payload: { subject: { reference: `Patient/${SCOPE001_RESTRICTED_AUTH_PATIENT_ID}` }, valueQuantity: { value: 111 } },
    },
    {
      _id: 'Observation/blocked', id: SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID, resourceType: 'Observation',
      project, dataset_generation: generation, auth_resource_path: blockedPath,
      payload: { subject: { reference: `Patient/${SCOPE001_RESTRICTED_AUTH_PATIENT_ID}` }, valueQuantity: { value: 999 } },
    },
  ];
  const subjectEdges = [
    {
      _from: 'Observation/allowed', _to: patient._id, from_type: 'Observation', to_type: 'Patient',
      label: 'subject_Patient', project, dataset_generation: generation,
      auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
    },
    {
      _from: 'Observation/blocked', _to: patient._id, from_type: 'Observation', to_type: 'Patient',
      label: 'subject_Patient', project, dataset_generation: generation,
      auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
    },
  ];
  const marker = '__SCOPE001_AUTH_RESULT__';
  const bearer = 'synthetic-private-bearer-value-123456';
  const target = {
    project, generation, noAuth: false,
    apiOrigin: 'http://127.0.0.1:8198', arangoContainer: 'scope001-owned-arangodb', arangoDatabase: 'loom_dev',
    datasetDir: fileURLToPath(new URL('../../../../testdata/scope-001-restricted-auth', import.meta.url)),
  };
  const witnessBindVars = {
    project,
    generation,
    patientId: SCOPE001_RESTRICTED_AUTH_PATIENT_ID,
    observationIds: [SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID, SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID],
  };
  const mutationBindVars = {
    ...witnessBindVars,
    patientReference: `Patient/${SCOPE001_RESTRICTED_AUTH_PATIENT_ID}`,
    allowedId: SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID,
    blockedId: SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID,
    allowedPath: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
    blockedPath: SCOPE001_BLOCKED_AUTH_RESOURCE_PATH,
  };
  assert.throws(() => fakeArangoValidateBindVars(scope001LoadedAuthorizationWitnessAQL, mutationBindVars), error => {
    assert.equal(error.errorNum, 1552);
    assert.match(error.message, /bind parameter 'allowedId' was not declared/);
    return true;
  }, 'Arango rejects mutation-only bind values on the raw witness query');
  assert.throws(() => fakeArangoValidateBindVars(scope001AuthorizationPathMutationAQL, witnessBindVars), /missing bind parameter: allowedId/);
  const fetchCalls = [];
  const fetchImpl = async (url, init = {}) => {
    fetchCalls.push({ url: new URL(url), init });
    if (fetchCalls.length === 1) return new Response(null, { status: 404 });
    if (init.method === 'POST') return new Response(JSON.stringify({ state: 'STAGED' }), { status: 200 });
    return new Response(JSON.stringify({ state: 'STAGED' }), { status: 200 });
  };
  const arangoCalls = [];
  const execFileSyncImpl = (command, args, options) => {
    arangoCalls.push({ command, args, options });
    const resultIndex = arangoCalls.length;
    const query = resultIndex === 2 ? scope001AuthorizationPathMutationAQL : scope001LoadedAuthorizationWitnessAQL;
    const queryBindVars = resultIndex === 2 ? mutationBindVars : witnessBindVars;
    fakeArangoValidateBindVars(query, queryBindVars);
    assert(args.at(-1).includes(`db._query(${JSON.stringify(query)}, ${JSON.stringify(queryBindVars)},`),
      'the owned shell invocation must carry only the bind variables declared by its exact AQL');
    if (resultIndex === 2) {
      return `${marker}${JSON.stringify([{ preconditionsPassed: true, updatedDocumentCount: 1, updatedDocumentPaths: [SCOPE001_BLOCKED_AUTH_RESOURCE_PATH] }])}\n`;
    }
    const blockedPath = resultIndex === 1 ? SCOPE001_ALLOWED_AUTH_RESOURCE_PATH : SCOPE001_BLOCKED_AUTH_RESOURCE_PATH;
    return `${marker}${JSON.stringify([{
      patientCount: 1,
      patient,
      observations: observations(blockedPath),
      subjectEdges,
    }])}\n`;
  };

  const evidence = await seedAndScopeScope001Fixture({ target, bearer, fetchImpl, execFileSyncImpl });
  assert.deepEqual(evidence, {
    generationState: 'STAGED',
    loadedWitnessCount: 2,
    allowedDocumentCount: 1,
    blockedDocumentCount: 1,
    subjectEdgeCount: 2,
    readScopeAuthResourcePaths: [SCOPE001_ALLOWED_AUTH_RESOURCE_PATH],
    blockedAuthResourcePath: SCOPE001_BLOCKED_AUTH_RESOURCE_PATH,
  });
  assert.equal(fetchCalls.length, 3);
  assert.equal(fetchCalls[0].url.searchParams.get('auth_resource_path'), SCOPE001_ALLOWED_RESOURCE_PATH);
  assert.equal(fetchCalls[0].init.headers.authorization, `Bearer ${bearer}`);
  assert.equal(fetchCalls[1].url.search, '', 'the seed upload uses the path and multipart scope contract only');
  const form = fetchCalls[1].init.body;
  assert.equal(form.get('auth_resource_path'), SCOPE001_ALLOWED_RESOURCE_PATH);
  assert.equal(form.get('defer_activation'), 'false');
  assert.deepEqual(form.getAll('file').map(file => file.name).sort(), ['Observation.ndjson', 'Patient.ndjson']);
  assert.equal(arangoCalls.length, 3, 'the helper must raw-read, split one document path, then independently reread');
  for (const { command, args } of arangoCalls) {
    const shell = args.at(-1);
    assert.equal(command, 'rtk');
    assert.equal(args[3], 'scope001-owned-arangodb');
    assert(shell.includes('--server.database'));
    assert(shell.includes('loom_dev'));
    assert.equal(shell.includes(bearer), false, 'the synthetic bearer must never reach the Arango command');
  }
  assert(arangoCalls[1].args.at(-1).includes(JSON.stringify(scope001AuthorizationPathMutationAQL)));
  assert.match(scope001AuthorizationPathMutationAQL, /edge\.auth_resource_path == @allowedPath/);
  assert.match(scope001AuthorizationPathMutationAQL, /patient\.resourceType == "Patient"/);
  assert.match(scope001AuthorizationPathMutationAQL, /UPDATE document WITH \{ auth_resource_path: @blockedPath \} IN Observation/);
  assert.doesNotMatch(scope001AuthorizationPathMutationAQL, /UPDATE edge/);
  assert.equal(JSON.stringify(evidence).includes(bearer), false);
});

test('runtime guard accepts only one explicitly configured Calypr process without --no-auth', () => {
  const passing = assertScope001RestrictedAuthRuntime({
    apiContainer: 'loom-dev-scope001-auth-loom-api-1',
    dockerTop: () => 'PID COMMAND\n42 /workspace/tmp/arango-fhir-server --config /workspace/scope-001-restricted-auth.server.yaml',
  });
  assert.equal(passing.configArgumentVerified, true);
  assert.equal(passing.noAuthArgumentAbsent, true);

  const cases = [
    'PID COMMAND\n42 /workspace/tmp/arango-fhir-server --no-auth',
    'PID COMMAND\n42 /workspace/tmp/arango-fhir-server --config /workspace/other.yaml',
    'PID COMMAND\n42 /workspace/tmp/arango-fhir-server --config /workspace/scope-001-restricted-auth.server.yaml\n43 /workspace/tmp/arango-fhir-server --config /workspace/scope-001-restricted-auth.server.yaml',
  ];
  for (const processList of cases) {
    assert.throws(() => assertScope001RestrictedAuthRuntime({
      apiContainer: 'loom-dev-scope001-auth-loom-api-1', dockerTop: () => processList,
    }));
  }
});
