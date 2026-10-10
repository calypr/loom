import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createServer } from 'node:http';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { buildArangoShellInvocation } from './owned-arangosh-command.mjs';
import {
  assertScope001LoadedAuthorizationWitness,
  assertScope001SeededAuthorizationWitness,
  SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID,
  SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID,
  SCOPE001_RESTRICTED_AUTH_PATIENT_ID,
  SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
  SCOPE001_BLOCKED_AUTH_RESOURCE_PATH,
} from './scope-001-restricted-auth-oracle.mjs';

export const SCOPE001_ALLOWED_RESOURCE_PATH = '/programs/loom_dev_scope001/projects/auth';
export const SCOPE001_BLOCKED_RESOURCE_PATH = '/programs/loom_dev_scope001/projects/blocked';

const ARANGO_RESULT_MARKER = '__SCOPE001_AUTH_RESULT__';
const ARANGO_MAX_RUNTIME_SECONDS = 10;
const ARANGO_MEMORY_LIMIT_BYTES = 64 * 1024 * 1024;

export const scope001AuthorizationPathMutationAQL = `
LET patientMatches = (
  FOR document IN Patient
    FILTER document.project == @project
      AND document.dataset_generation == @generation
      AND document.id == @patientId
    LIMIT 3
    RETURN document
)
LET patient = FIRST(patientMatches)
LET observations = (
  FOR document IN Observation
    FILTER document.project == @project
      AND document.dataset_generation == @generation
      AND document.id IN @observationIds
    RETURN document
)
LET edges = (
  FOR edge IN fhir_edge
    FILTER edge.project == @project
      AND edge.dataset_generation == @generation
      AND edge.label == "subject_Patient"
      AND edge.from_type == "Observation"
      AND edge.to_type == "Patient"
      AND edge._from IN (FOR document IN observations RETURN document._id)
    RETURN edge
)
LET validDocuments = LENGTH(observations) == 2
  AND LENGTH(FOR document IN observations
    FILTER document.auth_resource_path == @allowedPath
      AND document.payload.subject.reference == @patientReference
      AND document.id == @allowedId
      AND document.resourceType == "Observation"
      AND document.payload.valueQuantity.value == 111
    RETURN 1
  ) == 1
  AND LENGTH(FOR document IN observations
    FILTER document.auth_resource_path == @allowedPath
      AND document.payload.subject.reference == @patientReference
      AND document.id == @blockedId
      AND document.resourceType == "Observation"
      AND document.payload.valueQuantity.value == 999
    RETURN 1
  ) == 1
LET validEdges = LENGTH(edges) == 2
  AND LENGTH(FOR edge IN edges
    FILTER edge.project == @project
      AND edge.dataset_generation == @generation
      AND edge._to == patient._id
      AND edge.auth_resource_path == @allowedPath
    RETURN 1
  ) == 2
  AND LENGTH(FOR document IN observations
    LET matching = (
      FOR edge IN edges
        FILTER edge._from == document._id
          AND edge._to == patient._id
          AND edge.auth_resource_path == @allowedPath
        RETURN 1
    )
    FILTER LENGTH(matching) == 1
    RETURN 1
  ) == 2
LET valid = LENGTH(patientMatches) == 1
  AND patient != null
  AND patient.project == @project
  AND patient.dataset_generation == @generation
  AND patient.resourceType == "Patient"
  AND patient.auth_resource_path == @allowedPath
  AND validDocuments
  AND validEdges
LET updated = (
  FOR document IN observations
    FILTER valid
      AND document.id == @blockedId
      AND document.auth_resource_path == @allowedPath
    UPDATE document WITH { auth_resource_path: @blockedPath } IN Observation
    RETURN NEW.auth_resource_path
)
RETURN {
  preconditionsPassed: valid,
  updatedDocumentCount: LENGTH(updated),
  updatedDocumentPaths: updated
}`.trim();

export const scope001LoadedAuthorizationWitnessAQL = `
LET patientMatches = (
  FOR document IN Patient
    FILTER document.project == @project
      AND document.dataset_generation == @generation
      AND document.id == @patientId
    LIMIT 3
    RETURN document
)
LET patient = FIRST(patientMatches)
LET observations = (
  FOR document IN Observation
    FILTER document.project == @project
      AND document.dataset_generation == @generation
      AND document.id IN @observationIds
    SORT document.id
    RETURN document
)
LET subjectEdges = (
  FOR edge IN fhir_edge
    FILTER edge.project == @project
      AND edge.dataset_generation == @generation
      AND edge.label == "subject_Patient"
      AND edge.from_type == "Observation"
      AND edge.to_type == "Patient"
      AND edge._from IN (FOR document IN observations RETURN document._id)
    SORT edge._from
    RETURN edge
)
RETURN { patientCount: LENGTH(patientMatches), patient, observations, subjectEdges }`.trim();

const jwtPart = value => Buffer.from(JSON.stringify(value)).toString('base64url');

export function scope001FenceResponse({ method, path, authorization, expectedAuthorization }) {
  if (method !== 'GET' || path !== '/user') {
    return { status: 404, body: undefined };
  }
  if (authorization !== expectedAuthorization) {
    return { status: 401, body: { error: 'unauthorized' } };
  }
  return {
    status: 200,
    body: {
      authz: {
        [SCOPE001_ALLOWED_RESOURCE_PATH]: [
          { method: 'read', service: '*' },
          { method: 'write', service: '*' },
        ],
      },
    },
  };
}

export function createScope001SyntheticBearer(issuer, subject = 'scope-001-local-user') {
  const url = new URL(issuer);
  assert.equal(url.protocol, 'http:', 'The local Scope-001 Fence issuer must use HTTP.');
  assert.equal(url.pathname, '/', 'The local Scope-001 Fence issuer must be an origin.');
  assert(url.port, 'The local Scope-001 Fence issuer must include its ephemeral port.');
  assert.equal(url.username, '', 'The local Scope-001 Fence issuer must not contain credentials.');
  assert.equal(url.password, '', 'The local Scope-001 Fence issuer must not contain credentials.');
  assert.match(subject, /^[A-Za-z0-9._-]+$/, 'The synthetic principal subject contains unsupported characters.');
  const header = jwtPart({ alg: 'none', typ: 'JWT' });
  const claims = jwtPart({ iss: url.origin, sub: subject });
  return `${header}.${claims}.`;
}

/**
 * Start the local Fence `/user` endpoint used only by the isolated auth target.
 * It exposes one exact read/write grant and accepts only the matching in-memory
 * bearer. The app's Calypr path consumes this grant; this helper does not test
 * JWT signatures or external identity/ACL services.
 */
export async function startScope001FenceStub({
  bindAddress = '0.0.0.0',
  containerHostname = 'host.docker.internal',
} = {}) {
  assert(['0.0.0.0', '127.0.0.1'].includes(bindAddress),
    'The local Fence stub may bind only the container host gateway or IPv4 loopback.');
  assert.equal(containerHostname, 'host.docker.internal', 'The isolated Compose target has one approved host gateway name.');

  let expectedAuthorization = '';
  let authorizedRequests = 0;
  let rejectedRequests = 0;
  const server = createServer((request, response) => {
    const result = scope001FenceResponse({
      method: request.method,
      path: request.url,
      authorization: request.headers.authorization,
      expectedAuthorization,
    });
    if (result.status === 200) authorizedRequests += 1;
    if (result.status === 401) rejectedRequests += 1;
    response.writeHead(result.status, {
      'Content-Type': 'application/json',
      'Cache-Control': 'no-store',
    }).end(result.body === undefined ? '' : JSON.stringify(result.body));
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, bindAddress, resolve);
  });
  const address = server.address();
  assert(address && typeof address === 'object' && Number.isInteger(address.port),
    'The local Fence stub did not bind an ephemeral TCP port.');
  const issuer = `http://${containerHostname}:${address.port}`;
  const bearer = createScope001SyntheticBearer(issuer, `scope-001-${randomUUID()}`);
  expectedAuthorization = `Bearer ${bearer}`;

  let closed = false;
  return Object.freeze({
    issuer,
    bearer,
    authorizationHeader: expectedAuthorization,
    hostTestEndpoint: `http://127.0.0.1:${address.port}/user`,
    grantResourcePath: SCOPE001_ALLOWED_RESOURCE_PATH,
    signatureValidationExercised: false,
    externalACLExercised: false,
    counts: () => ({ authorizedRequests, rejectedRequests }),
    close: async () => {
      if (closed) return;
      closed = true;
      await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    },
  });
}

const scope001SeedStatusURL = target => {
  const url = new URL(`/api/v1/datasets/${encodeURIComponent(target.project)}/generations/${encodeURIComponent(target.generation)}`, target.apiOrigin);
  url.searchParams.set('auth_resource_path', SCOPE001_ALLOWED_RESOURCE_PATH);
  return url;
};

async function readJSON(response, label) {
  let value;
  try { value = await response.json(); }
  catch { throw new Error(`${label} returned invalid JSON (HTTP ${response.status})`); }
  if (!response.ok) throw new Error(`${label} returned HTTP ${response.status}`);
  return value;
}

async function waitForScope001Generation(url, authorization, fetchImpl, timeoutMs = 120_000) {
  const started = Date.now();
  let lastState = 'not observed';
  while (Date.now() - started < timeoutMs) {
    const response = await fetchImpl(url, {
      headers: { authorization },
      signal: AbortSignal.timeout(10_000),
    });
    if (response.ok) {
      const status = await response.json();
      const state = String(status?.state ?? '').toUpperCase();
      if (['STAGED', 'READY', 'ACTIVE'].includes(state)) return status;
      if (['FAILED', 'ERROR'].includes(state)) throw new Error(`restricted-auth fixture generation failed (${state})`);
      lastState = state || 'unknown';
    } else if (response.status !== 404) {
      throw new Error(`restricted-auth fixture status returned HTTP ${response.status}`);
    }
    await new Promise(resolve => setTimeout(resolve, 500));
  }
  throw new Error(`timed out waiting for restricted-auth fixture generation (${lastState})`);
}

function executeScope001ArangoQuery({ target, query, bindVars, queryLabel, execFileSyncImpl = execFileSync }) {
  assert(typeof queryLabel === 'string' && queryLabel.trim(), 'owned Arango query requires a diagnostic label');
  const script = `const rows = db._query(${JSON.stringify(query)}, ${JSON.stringify(bindVars)}, { maxRuntime: ${ARANGO_MAX_RUNTIME_SECONDS}, memoryLimit: ${ARANGO_MEMORY_LIMIT_BYTES} }).toArray();\nprint(${JSON.stringify(ARANGO_RESULT_MARKER)} + JSON.stringify(rows));`;
  const invocation = buildArangoShellInvocation({
    container: target.arangoContainer,
    database: target.arangoDatabase ?? 'loom_dev',
    script,
  });
  let stdout;
  try {
    stdout = execFileSyncImpl(invocation.command, invocation.args, {
      encoding: 'utf8',
      timeout: 20_000,
      maxBuffer: 2 * 1024 * 1024,
    });
  } catch (error) {
    const status = Number.isInteger(error?.status) ? `exit ${error.status}` : error?.signal ? `signal ${error.signal}` : String(error?.code ?? 'failed');
    const sanitizeDiagnostic = value => String(value ?? '')
      .replaceAll(query, '[AQL omitted]')
      .replaceAll(JSON.stringify(bindVars), '[binds omitted]')
      .replace(new RegExp(`${ARANGO_RESULT_MARKER}[^\\r\\n]*`), '[result omitted]')
      .replace(/\bBearer\s+\S+/gi, 'Bearer [redacted]')
      .replace(/(["']?(?:authorization|token|secret|password)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;}]+)/gi, '$1[redacted]')
      .replace(/\s+/g, ' ')
      .trim();
    const diagnostic = [
      ['stderr', error?.stderr],
      ['stdout', error?.stdout],
    ].filter(([, value]) => value !== undefined && value !== null && String(value).trim())
      .map(([stream, value]) => `${stream}: ${sanitizeDiagnostic(value)}`)
      .join(' | ')
      .slice(0, 700);
    const errorNum = /(?:errorNum|error_num)["'\s:=]+(\d+)/i.exec(diagnostic)?.[1];
    throw new Error(`restricted-auth ${queryLabel} Arango query failed (${status})${errorNum ? ` errorNum ${errorNum}` : ''}${diagnostic ? `: ${diagnostic}` : ''}`);
  }
  const markerLine = String(stdout).split(/\r?\n/).filter(line => line.startsWith(ARANGO_RESULT_MARKER)).at(-1);
  assert(markerLine, 'restricted-auth owned Arango query returned no bounded result marker');
  try { return JSON.parse(markerLine.slice(ARANGO_RESULT_MARKER.length)); }
  catch { throw new Error('restricted-auth owned Arango query returned malformed result JSON'); }
}

function scope001WitnessBindVars(target) {
  return {
    project: target.project,
    generation: target.generation,
    patientId: SCOPE001_RESTRICTED_AUTH_PATIENT_ID,
    observationIds: [SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID, SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID],
  };
}

function scope001MutationBindVars(target) {
  return {
    ...scope001WitnessBindVars(target),
    patientReference: `Patient/${SCOPE001_RESTRICTED_AUTH_PATIENT_ID}`,
    allowedId: SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID,
    blockedId: SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID,
    allowedPath: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
    blockedPath: SCOPE001_BLOCKED_AUTH_RESOURCE_PATH,
  };
}

/** Seed the isolated generation, move only the blocked target document to its sibling path, and prove both rows remain loaded. */
export async function seedAndScopeScope001Fixture({ target, bearer, fetchImpl = globalThis.fetch, execFileSyncImpl = execFileSync } = {}) {
  assert(target && target.project === 'loom_dev_scope001-auth' && target.generation === 'scope001-auth-v1',
    'restricted-auth seeding requires the exact owned project and generation');
  assert.equal(target.noAuth, false, 'restricted-auth fixture seeding requires Calypr authorization');
  assert(typeof bearer === 'string' && bearer.length >= 24 && !/\s/.test(bearer), 'restricted-auth bearer must be supplied privately');
  assert.equal(typeof fetchImpl, 'function', 'restricted-auth seeding requires a fetch implementation');
  const authorization = `Bearer ${bearer}`;
  const statusURL = scope001SeedStatusURL(target);
  const preflight = await fetchImpl(statusURL, { headers: { authorization }, signal: AbortSignal.timeout(10_000) });
  if (preflight.status !== 404) {
    throw new Error(preflight.ok
      ? 'refusing to reuse an existing restricted-auth generation'
      : `restricted-auth generation preflight returned HTTP ${preflight.status}`);
  }

  const form = new FormData();
  for (const name of ['Patient.ndjson', 'Observation.ndjson']) {
    const contents = await readFile(join(target.datasetDir, name));
    form.append('file', new Blob([contents], { type: 'application/x-ndjson' }), name);
  }
  form.append('auth_resource_path', SCOPE001_ALLOWED_RESOURCE_PATH);
  form.append('defer_activation', 'false');
  const uploadURL = new URL(statusURL);
  uploadURL.search = '';
  const submitted = await fetchImpl(uploadURL, {
    method: 'POST',
    headers: { authorization },
    body: form,
    signal: AbortSignal.timeout(120_000),
  });
  await readJSON(submitted, 'restricted-auth fixture seed');
  const generationStatus = await waitForScope001Generation(statusURL, authorization, fetchImpl);

  const bindVars = scope001WitnessBindVars(target);
  const initialRows = executeScope001ArangoQuery({
    target,
    query: scope001LoadedAuthorizationWitnessAQL,
    bindVars,
    queryLabel: 'initial raw witness',
    execFileSyncImpl,
  });
  assert.equal(initialRows.length, 1, 'restricted-auth initial raw reread must return exactly one witness row');
  assertScope001SeededAuthorizationWitness({ project: target.project, generation: target.generation, ...initialRows[0] });

  const mutationRows = executeScope001ArangoQuery({
    target,
    query: scope001AuthorizationPathMutationAQL,
    bindVars: scope001MutationBindVars(target),
    queryLabel: 'blocked-document path split',
    execFileSyncImpl,
  });
  assert.equal(mutationRows.length, 1, 'restricted-auth path split must return exactly one mutation summary');
  assert.equal(mutationRows[0].preconditionsPassed, true, 'restricted-auth path split preconditions must match both exact loaded witnesses');
  assert.equal(mutationRows[0].updatedDocumentCount, 1, 'only the blocked Observation target document must change path');
  assert.deepEqual(mutationRows[0].updatedDocumentPaths, [SCOPE001_BLOCKED_AUTH_RESOURCE_PATH]);

  const loadedRows = executeScope001ArangoQuery({
    target,
    query: scope001LoadedAuthorizationWitnessAQL,
    bindVars,
    queryLabel: 'final raw witness',
    execFileSyncImpl,
  });
  assert.equal(loadedRows.length, 1, 'restricted-auth final raw reread must return exactly one witness row');
  const evidence = assertScope001LoadedAuthorizationWitness({ project: target.project, generation: target.generation, ...loadedRows[0] });
  return {
    generationState: String(generationStatus.state).toUpperCase(),
    loadedWitnessCount: evidence.loadedObservationIds.length,
    allowedDocumentCount: 1,
    blockedDocumentCount: 1,
    subjectEdgeCount: loadedRows[0].subjectEdges.length,
    readScopeAuthResourcePaths: evidence.readScopeAuthResourcePaths,
    blockedAuthResourcePath: evidence.blockedAuthResourcePath,
  };
}
