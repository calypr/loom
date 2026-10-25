import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';

export const POLICY_TRACE_ABSENT = 'Absent';
const POLICY_ONE_DIAGNOSTIC_INPUT_ENCODING = 'policy-one-trace-input-diagnostic/v1';
const MODELED_TRANSPORTS = new Map([
  ['POST construction-proposals', false],
  ['POST construction-choice-proposals', false],
  ['POST commands', true],
]);
const PENDING_FORMAL_SUPPORT_TRANSPORTS = new Set([
  'POST semantic-inventory',
  'POST construction-capabilities',
]);
const READ_ONLY_TRANSPORTS = new Map([
  ['GET builder', 'BuilderState read'],
  ['GET capability', 'Explorer capability read'],
  ['GET row-definition-choices', 'Row-definition choices read'],
  ['POST suggestions', 'Source suggestions read'],
  ['POST semantic-inventory', 'Semantic inventory read'],
  ['POST configured-column-context', 'Configured-column context read'],
  ['POST construction-capabilities', 'Construction capabilities read'],
  ['POST construction-inputs', 'Construction stage inputs read'],
  ['POST construction-choices', 'Construction source choices read'],
  ['POST frame-source-options', 'Frame source options read'],
  ['POST preview', 'Receipt-backed preview read'],
]);

const isRecord = value => value !== null && typeof value === 'object' && !Array.isArray(value) &&
  (Object.getPrototypeOf(value) === Object.prototype || Object.getPrototypeOf(value) === null);

function record(value, label) {
  assert(isRecord(value), `${label} must be a plain object`);
  return value;
}

function nonEmptyString(value, label) {
  assert.equal(typeof value, 'string', `${label} must be a string`);
  assert(value.length > 0, `${label} must not be empty`);
  return value;
}

function optionalString(value, label) {
  if (value === undefined || value === null) return POLICY_TRACE_ABSENT;
  return nonEmptyString(value, label);
}

function finiteJSON(value, label, ancestors = new Set()) {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return;
  if (typeof value === 'number') {
    assert(Number.isFinite(value), `${label} must contain only finite numbers`);
    return;
  }
  assert(typeof value === 'object', `${label} must contain only JSON values`);
  assert(!ancestors.has(value), `${label} must not contain cycles`);
  ancestors.add(value);
  if (Array.isArray(value)) {
    for (let index = 0; index < value.length; index += 1) {
      assert(Object.hasOwn(value, index), `${label}[${index}] must not be missing`);
      finiteJSON(value[index], `${label}[${index}]`, ancestors);
    }
  } else {
    record(value, label);
    for (const [key, entry] of Object.entries(value)) finiteJSON(entry, `${label}.${key}`, ancestors);
  }
  ancestors.delete(value);
}

function finiteClone(value, label) {
  finiteJSON(value, label);
  return structuredClone(value);
}

function withoutFields(value, fields, label) {
  const source = record(value, label);
  return finiteClone(Object.fromEntries(Object.entries(source).filter(([key]) => !fields.has(key))), label);
}

function opaqueBrowserHandle(value, label) {
  assert(value && (typeof value === 'object' || typeof value === 'function'),
    `${label} must retain its captured browser object before diagnostic encoding`);
  return value;
}

export function encodePolicyOneTraceDiagnosticInput(input) {
  record(input, 'Policy One projection input');
  const inputFields = ['expected', 'requests', 'responses', 'saved', 'dom', 'fixtureIDs', 'context'];
  assert.deepEqual(Object.keys(input).sort(), [...inputFields].sort(),
    'Policy One diagnostic encoding must cover every projection input field');
  const requests = record(input.requests, 'Policy One request captures');
  const responses = record(input.responses, 'Policy One response captures');
  const requestLabels = Object.keys(requests).sort();
  assert.deepEqual(Object.keys(responses).sort(), requestLabels,
    'Policy One diagnostic request and response labels must match');
  const requestObjects = new Map();
  const requestObjectIDs = new Set();
  const requestRows = {};
  const requestReferences = {};
  for (const label of requestLabels) {
    const request = record(requests[label], `Policy One ${label} request`);
    const requestObject = opaqueBrowserHandle(request.requestObject, `Policy One ${label} requestObject`);
    const captureId = nonEmptyString(request.captureId, `Policy One ${label} captureId`);
    const requestObjectId = nonEmptyString(request.requestObjectId ?? captureId,
      `Policy One ${label} requestObjectId`);
    assert(!requestObjects.has(captureId), 'Policy One request capture IDs must be unique');
    assert(!requestObjectIDs.has(requestObject), 'Policy One requests must retain distinct browser request objects');
    requestObjects.set(captureId, requestObject);
    requestObjectIDs.add(requestObject);
    requestRows[label] = withoutFields(request, new Set(['requestObject']),
      `Policy One ${label} JSON request capture`);
    requestReferences[label] = { captureId, requestObjectId };
  }
  const responseRows = {};
  const responseReferences = {};
  const responseObjects = new Set();
  const responseObjectTokens = new Set();
  for (const label of requestLabels) {
    const response = record(responses[label], `Policy One ${label} response`);
    const requestObject = opaqueBrowserHandle(response.requestObject,
      `Policy One ${label} response requestObject`);
    const responseObject = opaqueBrowserHandle(response.responseObject,
      `Policy One ${label} responseObject`);
    const captureId = nonEmptyString(response.captureId, `Policy One ${label} response captureId`);
    const requestCaptureId = captureId;
    const requestRecord = requests[label];
    assert.equal(captureId, requestRecord.captureId,
      `Policy One ${label} response capture ID must match its exact request`);
    assert.equal(requestObject, requestRecord.requestObject,
      `Policy One ${label} response must retain its exact request object alias`);
    assert(!responseObjects.has(responseObject), 'Policy One responses must retain distinct browser response objects');
    assert.notEqual(responseObject, requestObject, 'Policy One response objects must differ from request objects');
    const responseObjectToken = `response:${label}:${captureId}`;
    assert(!responseObjectTokens.has(responseObjectToken), 'Policy One diagnostic response tokens must be unique');
    responseObjects.add(responseObject);
    responseObjectTokens.add(responseObjectToken);
    responseRows[label] = withoutFields(response, new Set(['requestObject', 'responseObject']),
      `Policy One ${label} JSON response capture`);
    responseReferences[label] = {
      captureId,
      requestCaptureId,
      requestObjectId: requestRecord.requestObjectId ?? requestRecord.captureId,
      responseObjectToken,
    };
  }
  const diagnostic = {
    encoding: POLICY_ONE_DIAGNOSTIC_INPUT_ENCODING,
    input: {
      expected: finiteClone(input.expected, 'Policy One expected contract'),
      requests: requestRows,
      responses: responseRows,
      saved: finiteClone(input.saved, 'Policy One saved observations'),
      dom: finiteClone(input.dom, 'Policy One DOM observations'),
      fixtureIDs: finiteClone(input.fixtureIDs, 'Policy One fixture IDs'),
      context: finiteClone(input.context, 'Policy One context observations'),
    },
    opaqueBrowserObjectReferences: {
      requests: requestReferences,
      responses: responseReferences,
    },
  };
  finiteJSON(diagnostic, 'Policy One diagnostic input');
  return diagnostic;
}

export function decodePolicyOneTraceDiagnosticInput(diagnostic) {
  record(diagnostic, 'Policy One diagnostic input');
  assert.deepEqual(Object.keys(diagnostic).sort(),
    ['encoding', 'input', 'opaqueBrowserObjectReferences'].sort(),
    'Policy One diagnostic input must reject unknown top-level fields');
  assert.equal(diagnostic.encoding, POLICY_ONE_DIAGNOSTIC_INPUT_ENCODING,
    'Policy One diagnostic input must declare its supported encoding');
  finiteJSON(diagnostic, 'Policy One diagnostic input');
  const source = structuredClone(record(diagnostic.input, 'Policy One encoded projection input'));
  const inputFields = ['expected', 'requests', 'responses', 'saved', 'dom', 'fixtureIDs', 'context'];
  assert.deepEqual(Object.keys(source).sort(), [...inputFields].sort(),
    'Policy One encoded input must retain the complete projection field set');
  const references = record(diagnostic.opaqueBrowserObjectReferences,
    'Policy One opaque browser-object reference table');
  assert.deepEqual(Object.keys(references).sort(), ['requests', 'responses'],
    'Policy One opaque reference table must reject unknown handle groups');
  const requestReferences = record(references.requests, 'Policy One request reference table');
  const responseReferences = record(references.responses, 'Policy One response reference table');
  const requests = record(source.requests, 'Policy One encoded requests');
  const responses = record(source.responses, 'Policy One encoded responses');
  const labels = Object.keys(requests).sort();
  assert.deepEqual(Object.keys(responses).sort(), labels,
    'Policy One encoded response labels must match request labels');
  assert.deepEqual(Object.keys(requestReferences).sort(), labels,
    'Policy One reference table must map every and only captured request');
  assert.deepEqual(Object.keys(responseReferences).sort(), labels,
    'Policy One reference table must map every and only captured response');
  const requestObjects = new Map();
  for (const label of labels) {
    const request = record(requests[label], `Policy One encoded ${label} request`);
    assert.equal(Object.hasOwn(request, 'requestObject'), false,
      `Policy One ${label} diagnostic request cannot contain a raw browser object`);
    const reference = record(requestReferences[label], `Policy One ${label} request reference`);
    const captureId = nonEmptyString(request.captureId, `Policy One encoded ${label} captureId`);
    assert.equal(reference.captureId, captureId,
      `Policy One ${label} request reference must preserve its actual capture ID`);
    assert.equal(reference.requestObjectId, request.requestObjectId ?? captureId,
      `Policy One ${label} request reference must preserve its observed object ID`);
    assert(!requestObjects.has(captureId), 'Policy One diagnostic request capture IDs must be unique');
    const requestObject = Object.freeze({ kind: 'policy-one-diagnostic-request', captureId });
    requestObjects.set(captureId, requestObject);
    request.requestObject = requestObject;
  }
  const responseObjectTokens = new Set();
  for (const label of labels) {
    const response = record(responses[label], `Policy One encoded ${label} response`);
    assert.equal(Object.hasOwn(response, 'requestObject'), false,
      `Policy One ${label} diagnostic response cannot contain a raw request object`);
    assert.equal(Object.hasOwn(response, 'responseObject'), false,
      `Policy One ${label} diagnostic response cannot contain a raw browser response`);
    const reference = record(responseReferences[label], `Policy One ${label} response reference`);
    const captureId = nonEmptyString(response.captureId, `Policy One encoded ${label} response captureId`);
    assert.equal(reference.captureId, captureId,
      `Policy One ${label} response reference must preserve its actual capture ID`);
    assert.equal(reference.requestCaptureId, captureId,
      `Policy One ${label} response reference must bind its request capture`);
    const requestObject = requestObjects.get(reference.requestCaptureId);
    assert(requestObject, `Policy One ${label} response request reference must resolve to a captured request`);
    const matchingRequest = requests[label];
    assert.equal(reference.requestObjectId, matchingRequest.requestObjectId ?? matchingRequest.captureId,
      `Policy One ${label} response must preserve the captured request object ID`);
    const responseObjectToken = nonEmptyString(reference.responseObjectToken,
      `Policy One ${label} response object token`);
    assert.equal(responseObjectToken, `response:${label}:${captureId}`,
      `Policy One ${label} response token must match its diagnostic capture mapping`);
    assert(!responseObjectTokens.has(responseObjectToken), 'Policy One response object tokens must be unique');
    responseObjectTokens.add(responseObjectToken);
    response.requestObject = requestObject;
    response.responseObject = Object.freeze({ kind: 'policy-one-diagnostic-response', responseObjectToken });
  }
  return source;
}

function assertUniqueIDs(values, label, { allowEmpty = false } = {}) {
  assert(Array.isArray(values), `${label} must be an array`);
  if (!allowEmpty) assert(values.length > 0, `${label} must not be empty`);
  for (const [index, value] of values.entries()) nonEmptyString(value, `${label}[${index}]`);
  assert.equal(new Set(values).size, values.length, `${label} must not contain duplicates`);
  return values;
}

function sameIDSet(actual, expected, label) {
  assertUniqueIDs(actual, `${label} observed IDs`);
  assertUniqueIDs(expected, `${label} expected IDs`);
  assert.deepEqual([...actual].sort(), [...expected].sort(), `${label} must match the independent fixture IDs`);
}

function choiceProposalInput(body, label) {
  record(body, `${label} request body`);
  return record(body.variables?.input ?? body, `${label} request input`);
}

function pathMatches(path, suffix, label) {
  nonEmptyString(path, `${label} path`);
  nonEmptyString(suffix, `${label} expected path`);
  let pathname;
  try {
    pathname = new URL(path, 'http://loom.invalid').pathname;
  } catch {
    assert.fail(`${label} path must be a valid URL or pathname`);
  }
  assert(pathname.endsWith(suffix), `${label} must target ${suffix}`);
}

function transportEndpoint(path, label) {
  nonEmptyString(path, `${label} path`);
  let pathname;
  try {
    pathname = new URL(path, 'http://loom.invalid').pathname;
  } catch {
    assert.fail(`${label} path must be a valid URL or pathname`);
  }
  const prefix = '/authoring/v2/';
  const marker = pathname.indexOf(prefix);
  const endpoint = marker < 0 ? pathname.replace(/^\/+/, '') : pathname.slice(marker + prefix.length);
  assert(endpoint.length > 0, `${label} must name an authoring endpoint`);
  return endpoint;
}

function transportOwnerScope(resourcePath, label) {
  nonEmptyString(resourcePath, `${label} resource path`);
  let url;
  try {
    url = new URL(resourcePath, 'http://loom.invalid');
  } catch {
    assert.fail(`${label} resource path must be a valid URL or pathname`);
  }
  const marker = url.pathname.indexOf('/authoring/v2/');
  assert(marker >= 0, `${label} resource path must retain its authoring owner scope`);
  return `${url.origin}${url.pathname.slice(0, marker)}`;
}

function modeledRequestReferences(requests, context) {
  return [
    { label: 'OneSubmitted', event: 'choice-preview', request: requests.one },
    { label: 'AllSubmitted', event: 'choice-preview', request: requests.all },
    { label: 'AllApplySubmitted', event: 'apply-construction-choice', request: requests.apply },
    ...context.actions.filter(action => isRecord(action.request)).map(action => ({
      label: action.label,
      event: action.event,
      request: action.request,
    })),
  ];
}

function savedCASAfterCommand(capture, actions) {
  const matchingResponses = actions.filter(action => action.response?.requestObjectId === capture.requestCaptureId
    && action.response?.status >= 200 && action.response?.status < 300);
  assert.equal(matchingResponses.length, 1,
    `successful command transport ${capture.transportIndex} must link one exact response action`);
  const responseAction = matchingResponses[0];
  const saved = responseAction.savedAfter ?? responseAction.observation?.saved ?? {};
  const draftVersion = saved.draftVersion ?? responseAction.observation?.draftVersion;
  const draftDigest = saved.draftDigest ?? responseAction.observation?.draftDigest;
  assert(Number.isInteger(draftVersion) && draftVersion > 0,
    `successful command transport ${capture.transportIndex} must retain its actual after-Builder draft version`);
  nonEmptyString(draftDigest,
    `successful command transport ${capture.transportIndex} must retain its actual after-Builder draft digest`);
  assert.equal(capture.response.draftVersion, draftVersion,
    `successful command transport ${capture.transportIndex} response version must match its after-Builder read`);
  assert.equal(capture.response.draftDigest, draftDigest,
    `successful command transport ${capture.transportIndex} response digest must match its after-Builder read`);
  return { draftVersion, draftDigest };
}

function ownerChangingCommands(rows, ownerScope, draftVersion, draftDigest, afterEventIndex, beforeEventIndex) {
  return rows.filter(candidate => candidate.classification === 'modeled'
    || candidate.classification === 'setup')
    .filter(candidate => candidate.path === 'commands'
    && candidate.ownerScope === ownerScope
    && candidate.settlement === 'response'
    && candidate.status >= 200 && candidate.status < 300
    && candidate.settlementEventIndex > afterEventIndex
    && candidate.settlementEventIndex < beforeEventIndex
    && (candidate.classification === 'setup'
      ? candidate.requestBody?.commandId === candidate.response?.commandId
        && (candidate.response?.draftVersion !== draftVersion || candidate.response?.draftDigest !== draftDigest)
      : candidate.savedCASAfter
        && (candidate.savedCASAfter.draftVersion !== draftVersion
          || candidate.savedCASAfter.draftDigest !== draftDigest)));
}

function formalExitFromCaptures(captures, observedFormalExit) {
  if (observedFormalExit !== undefined) return observedFormalExit;
  const pending = captures.filter(capture => capture.settlement === 'pending');
  assert.equal(pending.length, 0,
    'pending raw transport requests require the observer’s actual formal exit boundary');
  const eventIndices = captures.flatMap(capture => [capture.requestEventIndex, capture.settlementEventIndex]);
  return {
    afterTransportIndex: Math.max(0, ...captures.map(capture => capture.transportIndex)),
    afterEventIndex: Math.max(0, ...eventIndices),
    observedAt: null,
    drainTimeoutMs: 5000,
    deadlineReached: false,
    pendingTransportIndices: [],
    pendingResponseReadTransportIndices: [],
  };
}

function canonicalTransportLedger(captures, references, initialPreview, initialObservation, actions,
  formalEntry, formalExit) {
  assert(Array.isArray(captures), 'workflow transport captures must be an array');
  record(formalEntry, 'formal entry boundary');
  record(formalExit, 'formal exit boundary');
  assert(Number.isInteger(formalExit.afterTransportIndex) && formalExit.afterTransportIndex >= 0,
    'formal exit must retain the final observed physical transport index');
  assert(Number.isInteger(formalExit.afterEventIndex) && formalExit.afterEventIndex >= 0,
    'formal exit must retain the final observed transport event index');
  assert(formalExit.observedAt === null || (Number.isInteger(formalExit.observedAt) && formalExit.observedAt > 0),
    'formal exit observedAt must be an actual capture time or null for a fully settled synthetic input');
  assert(Number.isInteger(formalExit.drainTimeoutMs) && formalExit.drainTimeoutMs > 0,
    'formal exit must retain its bounded transport drain');
  assert.equal(typeof formalExit.deadlineReached, 'boolean', 'formal exit must retain whether its drain expired');
  assert(Array.isArray(formalExit.pendingTransportIndices),
    'formal exit must enumerate physical requests pending at its boundary');
  assert(Array.isArray(formalExit.pendingResponseReadTransportIndices),
    'formal exit must enumerate pending response-body reads');
  assert(Number.isInteger(formalEntry.afterTransportIndex) && formalEntry.afterTransportIndex >= 0,
    'formal entry must retain the exact final setup transport index');
  assert(Number.isInteger(formalEntry.afterEventIndex) && formalEntry.afterEventIndex >= 0,
    'formal entry must retain the exact final setup settlement event index');
  record(formalEntry.initialSavedState, 'formal entry initial saved Builder snapshot');
  assert.equal(formalEntry.initialSavedState.outputId, initialObservation.outputId,
    'formal entry saved snapshot must bind the observed initial output');
  assert.equal(formalEntry.initialSavedState.snapshotToken, initialObservation.snapshotToken,
    'formal entry saved snapshot must bind the observed initial snapshot token');
  assert.equal(formalEntry.initialSavedState.draftVersion, initialObservation.draftVersion,
    'formal entry saved snapshot must bind the observed initial draft version');
  assert.equal(formalEntry.initialSavedState.draftDigest, initialObservation.draftDigest,
    'formal entry saved snapshot must bind the observed initial draft digest');
  assert(isDeepStrictEqual(formalEntry.initialSavedState.workspaceProjection, initialObservation.workspace),
    'formal entry saved snapshot projection must equal the actual initial workspace observation');
  const ordered = [...captures].sort((left, right) => left.transportIndex - right.transportIndex);
  const formalActorScope = ordered.find(capture => capture.capturePhase === 'FORMAL'
    && MODELED_TRANSPORTS.has(`${String(capture.method).toUpperCase()} ${transportEndpoint(capture.path,
      `transport ${capture.transportIndex}`)}`))?.ownerScope;
  nonEmptyString(formalActorScope, 'formal journey Explorer owner scope');
  const byIndex = new Map();
  const setupCaptures = ordered.filter(capture => capture.capturePhase === 'SETUP');
  assert.equal(formalEntry.afterTransportIndex, setupCaptures.at(-1)?.transportIndex ?? 0,
    'formal entry must follow every captured setup request without renumbering observer indexes');
  assert.equal(formalEntry.afterEventIndex, setupCaptures.reduce((max, capture) =>
    Math.max(max, capture.settlementEventIndex), 0),
  'formal entry must follow every captured setup settlement');
  for (const [position, capture] of ordered.entries()) {
    record(capture, `transport capture ${position}`);
    assert(Number.isInteger(capture.transportIndex) && capture.transportIndex > 0,
      `transport capture ${position} must retain its positive observer index`);
    assert.equal(capture.transportIndex, position + 1,
      'transport observer indexes must remain contiguous in request order');
    assert(!byIndex.has(capture.transportIndex), 'transport observer indexes must be unique');
    const method = nonEmptyString(capture.method, `transport ${capture.transportIndex} method`).toUpperCase();
    const path = transportEndpoint(capture.path, `transport ${capture.transportIndex}`);
    const route = `${method} ${path}`;
    const resourcePath = nonEmptyString(capture.resourcePath,
      `transport ${capture.transportIndex} resource path`);
    assert.equal(transportEndpoint(resourcePath, `transport ${capture.transportIndex} resource path`), path,
      `transport ${capture.transportIndex} resource path must match its authoring endpoint`);
    const ownerScope = transportOwnerScope(resourcePath, `transport ${capture.transportIndex}`);
    assert.equal(capture.ownerScope, ownerScope,
      `transport ${capture.transportIndex} owner scope must come from its exact resource path`);
    const requestBody = capture.requestBody ?? null;
    finiteJSON(requestBody, `transport ${capture.transportIndex} request body`);
    assert(['response', 'request-failed', 'pending'].includes(capture.settlement),
      `transport ${capture.transportIndex} must retain its actual terminal or pending state`);
    assert(Number.isInteger(capture.requestEventIndex) && capture.requestEventIndex > 0,
      `transport ${capture.transportIndex} must retain its request event order`);
    if (position > 0) {
      assert(ordered[position - 1].requestEventIndex < capture.requestEventIndex,
        'transport observer indexes must preserve request event order');
    }
    if (capture.settlement === 'pending') {
      assert(PENDING_FORMAL_SUPPORT_TRANSPORTS.has(route),
        `pending transport ${capture.transportIndex} must use an explicitly supported formal read-only route`);
      assert.equal(capture.capturePhase, 'FORMAL',
        `pending transport ${capture.transportIndex} cannot remain in setup`);
      assert.equal(capture.settlementEventIndex ?? null, null,
        `pending transport ${capture.transportIndex} cannot claim a terminal event`);
      assert.equal(capture.responseHeaders ?? null, null,
        `pending transport ${capture.transportIndex} cannot be accepted after response headers were observed`);
      assert.equal(capture.responseReadError ?? null, null,
        `pending transport ${capture.transportIndex} cannot hide an observed response-body error`);
      assert.equal(capture.responseBody ?? null, null,
        `pending transport ${capture.transportIndex} cannot retain an unmodeled response body`);
      assert.equal(capture.status ?? null, null,
        `pending transport ${capture.transportIndex} cannot claim an HTTP status`);
      assert.equal(capture.responseOwnerIndex ?? null, null,
        `pending transport ${capture.transportIndex} cannot claim a response owner`);
      assert.equal(capture.response ?? null, null,
        `pending transport ${capture.transportIndex} cannot claim response facts`);
      assert.equal(capture.failure ?? null, null,
        `pending transport ${capture.transportIndex} cannot claim a request failure`);
      assert.equal(capture.responseObservedBeforeFailure, false,
        `pending transport ${capture.transportIndex} cannot claim a failed terminal`);
    } else {
      assert(Number.isInteger(capture.settlementEventIndex)
        && capture.settlementEventIndex > capture.requestEventIndex,
      `transport ${capture.transportIndex} settlement must follow its request`);
    }
    assert(['SETUP', 'FORMAL'].includes(capture.capturePhase),
      `transport ${capture.transportIndex} must retain its actual capture phase`);
    assert.equal(typeof capture.responseObservedBeforeFailure, 'boolean',
      `transport ${capture.transportIndex} must retain whether headers preceded a failed terminal`);
    const responseHeaders = capture.responseHeaders ?? null;
    const responseReadError = capture.responseReadError ?? null;
    if (responseHeaders) {
      record(responseHeaders, `transport ${capture.transportIndex} response headers`);
      assert(Number.isInteger(responseHeaders.status) && responseHeaders.status > 0,
        `transport ${capture.transportIndex} response headers must retain their actual status`);
      assert(responseHeaders.requestId === null || typeof responseHeaders.requestId === 'string',
        `transport ${capture.transportIndex} response headers request ID must be a string or null`);
      finiteJSON(responseHeaders, `transport ${capture.transportIndex} response headers`);
    } else {
      assert.equal(capture.responseObservedBeforeFailure, false,
        `transport ${capture.transportIndex} cannot report headers before failure without captured headers`);
      assert.equal(responseReadError, null,
        `transport ${capture.transportIndex} cannot report a response body read error without response headers`);
    }
    if (responseReadError !== null) {
      nonEmptyString(responseReadError, `transport ${capture.transportIndex} response body read error`);
      assert(responseHeaders,
        `transport ${capture.transportIndex} response body read error must follow observed response headers`);
    }
    if (capture.responseBody !== undefined && capture.responseBody !== null) {
      finiteJSON(capture.responseBody, `transport ${capture.transportIndex} response body facts`);
    }
    if (capture.capturePhase === 'SETUP') {
      assert(capture.transportIndex <= formalEntry.afterTransportIndex,
        `setup transport ${capture.transportIndex} must precede the formal entry`);
      assert(capture.settlementEventIndex <= formalEntry.afterEventIndex,
        `setup transport ${capture.transportIndex} must settle before the formal entry`);
    } else {
      assert(capture.transportIndex > formalEntry.afterTransportIndex,
        `formal transport ${capture.transportIndex} must follow the setup transport boundary`);
      assert(capture.requestEventIndex > formalEntry.afterEventIndex,
        `formal transport ${capture.transportIndex} request must follow all setup settlements`);
    }
    if (route === 'POST frame-source-options') {
      record(requestBody, `frame source options transport ${capture.transportIndex} request body`);
      assert.equal(capture.capturePhase, 'FORMAL',
        `frame source options transport ${capture.transportIndex} must be inside the formal lifecycle`);
      assert.equal(ownerScope, formalActorScope,
        `frame source options transport ${capture.transportIndex} must use the exact formal Explorer owner scope`);
      const allowedBodyKeys = ['limit', 'outputId', 'resourceType', 'snapshotToken'];
      if (Object.hasOwn(requestBody, 'cursor')) allowedBodyKeys.push('cursor');
      assert.deepEqual(Object.keys(requestBody).sort(), allowedBodyKeys.sort(),
        `frame source options transport ${capture.transportIndex} must retain the UI's exact request shape`);
      assert.equal(requestBody.outputId, initialObservation.outputId,
        `frame source options transport ${capture.transportIndex} must target the initial saved output`);
      assert.equal(requestBody.snapshotToken, formalEntry.initialSavedState.snapshotToken,
        `frame source options transport ${capture.transportIndex} must use the formal entry snapshot`);
      assert.equal(requestBody.resourceType, initialObservation.workspace.rootResourceType,
        `frame source options transport ${capture.transportIndex} must use the actual root resource type`);
      assert.equal(requestBody.limit, 50,
        `frame source options transport ${capture.transportIndex} must preserve the UI's source page size`);
      if (Object.hasOwn(requestBody, 'cursor')) {
        nonEmptyString(requestBody.cursor, `frame source options transport ${capture.transportIndex} cursor`);
      }
      assert.equal(capture.settlement, 'response',
        `frame source options transport ${capture.transportIndex} must retain its completed response`);
      assert.equal(capture.status, 200,
        `frame source options transport ${capture.transportIndex} must retain HTTP 200`);
      assert.equal(capture.responseOwnerIndex, capture.transportIndex,
        `frame source options transport ${capture.transportIndex} response must retain its exact request owner`);
      assert.equal(capture.responseReadError ?? null, null,
        `frame source options transport ${capture.transportIndex} response body must be fully captured`);
      assert.equal(capture.response?.status, 200,
        `frame source options transport ${capture.transportIndex} response facts must retain HTTP 200`);
      assert.equal(capture.response?.frameSourceOptionsOutputId, requestBody.outputId,
        `frame source options transport ${capture.transportIndex} response must echo its output`);
      assert.equal(capture.response?.frameSourceOptionsSnapshotToken, requestBody.snapshotToken,
        `frame source options transport ${capture.transportIndex} response must echo its snapshot`);
      assert.equal(typeof capture.response?.frameSourceOptionsComplete, 'boolean',
        `frame source options transport ${capture.transportIndex} response must retain completeness`);
      assert.equal(typeof capture.response?.frameSourceOptionsTruncated, 'boolean',
        `frame source options transport ${capture.transportIndex} response must retain truncation`);
      assert(Number.isInteger(capture.response?.frameSourceOptionsSourceCount)
        && capture.response.frameSourceOptionsSourceCount >= 0
        && capture.response.frameSourceOptionsSourceCount <= requestBody.limit,
      `frame source options transport ${capture.transportIndex} response source count must fit its request limit`);
      if (capture.response.frameSourceOptionsNextCursor !== null
          && capture.response.frameSourceOptionsNextCursor !== undefined) {
        nonEmptyString(capture.response.frameSourceOptionsNextCursor,
          `frame source options transport ${capture.transportIndex} next cursor`);
      }
    }
    if (capture.settlement === 'response') {
      assert(responseHeaders,
        `transport ${capture.transportIndex} response terminal must retain its response headers`);
      assert.equal(capture.responseObservedBeforeFailure, false,
        `transport ${capture.transportIndex} successful terminal cannot follow request failure`);
      assert.equal(responseHeaders.status, capture.status,
        `transport ${capture.transportIndex} response headers must match its terminal status`);
      assert.equal(capture.responseOwnerIndex, capture.transportIndex,
        `transport ${capture.transportIndex} response must retain its exact request owner`);
      assert(isRecord(capture.response), `transport ${capture.transportIndex} response facts are required`);
      assert.equal(capture.response.status, capture.status,
        `transport ${capture.transportIndex} response status must match its settlement`);
      finiteJSON(capture.response, `transport ${capture.transportIndex} response facts`);
    } else if (capture.settlement === 'request-failed') {
      assert.equal(capture.responseObservedBeforeFailure, Boolean(responseHeaders),
        `transport ${capture.transportIndex} must preserve whether headers preceded its failed terminal`);
      assert.equal(capture.status, null, `failed transport ${capture.transportIndex} cannot have a response status`);
      assert.equal(capture.responseOwnerIndex, null,
        `failed transport ${capture.transportIndex} cannot claim a response owner`);
      assert.equal(capture.response ?? null, null,
        `failed transport ${capture.transportIndex} cannot contain response facts`);
    }
    byIndex.set(capture.transportIndex, {
      ...capture,
      method,
      path,
      resourcePath,
      ownerScope,
      requestBody,
      requestId: capture.requestId ?? null,
      requestCaptureId: capture.requestCaptureId ?? null,
      response: capture.response ?? null,
    });
  }

  const eventIndices = ordered.flatMap(capture => [
    capture.requestEventIndex,
    ...(capture.settlement === 'pending' ? [] : [capture.settlementEventIndex]),
  ]).sort((left, right) => left - right);
  assert.equal(new Set(eventIndices).size, eventIndices.length,
    'every observed transport request and terminal must have a distinct event index');
  assert.deepEqual(eventIndices, Array.from({ length: eventIndices.length }, (_, index) => index + 1),
    'raw transport ledger must account for every observed request and terminal event in its scope');
  const pendingRows = ordered.filter(capture => capture.settlement === 'pending');
  assert.equal(formalExit.afterTransportIndex, ordered.at(-1)?.transportIndex ?? 0,
    'formal exit must follow every observed physical request without renumbering observer indexes');
  assert.equal(formalExit.afterEventIndex, eventIndices.at(-1) ?? 0,
    'formal exit must match the last actual request or terminal event');
  assert.deepEqual(formalExit.pendingTransportIndices, pendingRows.map(capture => capture.transportIndex),
    'formal exit must retain the exact pending physical request set');
  assert.deepEqual(formalExit.pendingResponseReadTransportIndices, [],
    'formal exit cannot leave response-body reads outside its drain');
  assert.equal(formalExit.deadlineReached, pendingRows.length > 0,
    'only requests still pending at the bounded deadline may use the support-read boundary');
  if (pendingRows.length) {
    assert.equal(formalExit.observedAt > 0, true,
      'pending support reads require the actual formal-exit observation time');
    assert.equal(formalExit.drainTimeoutMs, 5000,
      'pending support reads must preserve the existing 5000ms observation budget');
  }

  const referencesByIndex = new Map();
  for (const reference of references) {
    record(reference.request, `${reference.label} modeled request`);
    assert(Number.isInteger(reference.request.transportIndex) && reference.request.transportIndex > 0,
      `${reference.label} must retain its raw observer transport index`);
    assert(!referencesByIndex.has(reference.request.transportIndex),
      'each modeled request must bind one raw observer transport index');
    const capture = byIndex.get(reference.request.transportIndex);
    assert(capture, `${reference.label} transport index was not captured by the raw observer`);
    assert.equal(capture.method, reference.request.method.toUpperCase(),
      `${reference.label} raw transport method must match its request capture`);
    assert.equal(capture.path, transportEndpoint(reference.request.path, reference.label),
      `${reference.label} raw transport path must match its request capture`);
    assert.equal(capture.requestId, reference.request.requestId ?? null,
      `${reference.label} raw request ID must match its request capture`);
    nonEmptyString(reference.request.captureId, `${reference.label} request captureId`);
    assert.equal(capture.requestCaptureId, reference.request.captureId,
      `${reference.label} transport capture ID must match its exact request capture`);
    assert(isDeepStrictEqual(capture.requestBody, reference.request.body),
      `${reference.label} raw request body must match its request capture`);
    referencesByIndex.set(reference.request.transportIndex, reference);
  }

  const rows = [...byIndex.values()].map(capture => {
    const reference = referencesByIndex.get(capture.transportIndex);
    const route = `${capture.method} ${capture.path}`;
    const modeledMutation = MODELED_TRANSPORTS.get(route);
    if (capture.capturePhase === 'SETUP' && route === 'POST commands') {
      assert(!reference, 'setup commands must remain explicitly outside the formal action span');
      assert.equal(capture.settlement, 'response',
        `setup mutation ${capture.transportIndex} must settle successfully before formal entry`);
      assert(capture.status >= 200 && capture.status < 300,
        `setup mutation ${capture.transportIndex} must return a successful HTTP status`);
      const commandId = nonEmptyString(capture.requestBody?.commandId,
        `setup mutation ${capture.transportIndex} commandId`);
      assert.equal(capture.response.commandId, commandId,
        `setup mutation ${capture.transportIndex} response must echo its exact commandId`);
      assert(Number.isInteger(capture.response.draftVersion) && capture.response.draftVersion > 0,
        `setup mutation ${capture.transportIndex} must retain its response draft version`);
      nonEmptyString(capture.response.draftDigest,
        `setup mutation ${capture.transportIndex} must retain its response draft digest`);
      return {
        ...capture,
        classification: 'setup',
        mutatesDraft: true,
        setupMutation: true,
        actionLabel: POLICY_TRACE_ABSENT,
        actionEvent: POLICY_TRACE_ABSENT,
      };
    }
    if (modeledMutation !== undefined) {
      assert.equal(capture.capturePhase, 'FORMAL',
        `${route} must not be modeled as a formal action before the entry snapshot`);
      assert(reference, `${route} must be linked to a semantic journey request`);
      return {
        ...capture,
        classification: 'modeled',
        mutatesDraft: modeledMutation,
        setupMutation: false,
        actionLabel: reference.label,
        actionEvent: reference.event,
      };
    }
    if (route === 'POST reconcile') {
      assert(!reference, 'reconcile requests use the reconcile owner/CAS/consumption abstraction');
      return { ...capture, classification: 'reconcile', mutatesDraft: false, setupMutation: false };
    }
    const support = READ_ONLY_TRANSPORTS.get(route);
    assert(support, `unsupported authoring transport ${route}`);
    assert(!reference, `${route} is read-only and cannot replace a modeled request`);
    return {
      ...capture,
      classification: 'read-only',
      mutatesDraft: false,
      setupMutation: false,
      readOnlyReason: support,
      ...(capture.settlement === 'pending' ? {
        previewDisposition: POLICY_TRACE_ABSENT,
        currentReceiptProducerTransportIndices: [],
        hasInitialCurrentReceiptProducer: false,
        successfulPreviewConsumerTransportIndices: [],
        compatibleProposalTransportIndices: [],
        invalidatedByTransportIndices: [],
        reconcileOwnerChangeCaptureIds: [],
        reconcileOwnerChangeSetupCommandIds: [],
      } : {}),
    };
  });
  assert.equal(referencesByIndex.size, rows.filter(row => row.classification === 'modeled').length,
    'every modeled request must have exactly one raw transport row');
  const formalModeledRows = rows.filter(row => row.capturePhase === 'FORMAL' && row.classification === 'modeled');
  assert(formalModeledRows.length > 0, 'formal transport ledger must retain modeled requests');
  assert.equal(formalModeledRows[0].ownerScope, formalActorScope,
    'formal actor scope must come from the first actual modeled request');
  for (const row of rows.filter(candidate => candidate.settlement === 'pending')) {
    assert.equal(row.classification, 'read-only',
      `pending transport ${row.transportIndex} cannot be modeled, reconcile, or setup activity`);
    assert.equal(row.ownerScope, formalActorScope,
      `pending support transport ${row.transportIndex} must belong to the exact formal Explorer owner scope`);
  }

  for (const row of rows) {
    if (row.classification === 'modeled' && row.path === 'commands'
      && row.settlement === 'response' && row.status >= 200 && row.status < 300) {
      row.savedCASAfter = savedCASAfterCommand(row, actions);
    }
  }

  const setupMutations = rows.filter(row => row.classification === 'setup');
  const finalSetupMutation = setupMutations.at(-1);
  if (finalSetupMutation) {
    assert.equal(finalSetupMutation.response.draftVersion, formalEntry.initialSavedState.draftVersion,
      'formal entry saved Builder version must include the final successful setup mutation');
    assert.equal(finalSetupMutation.response.draftDigest, formalEntry.initialSavedState.draftDigest,
      'formal entry saved Builder digest must include the final successful setup mutation');
  }

  const initialReceipt = initialPreview?.binding?.receiptId ?? null;
  const initialOutputId = initialPreview?.outputId ?? null;
  for (const row of rows) {
    if (row.method === 'POST' && row.path === 'preview') {
      const receiptId = row.requestBody.receiptId;
      const outputId = row.requestBody.outputId;
      nonEmptyString(receiptId, `preview transport ${row.transportIndex} receiptId`);
      nonEmptyString(outputId, `preview transport ${row.transportIndex} outputId`);
      row.previewDisposition = row.settlement === 'request-failed'
        ? 'request-failed'
        : row.status >= 200 && row.status < 300
          ? 'successful-response'
          : 'http-error';
      if (row.previewDisposition === 'successful-response') {
        assert.equal(row.responseOwnerIndex, row.transportIndex,
          `preview transport ${row.transportIndex} successful response must belong to its exact request`);
        assert(row.response.receiptIds?.includes(receiptId),
          `preview transport ${row.transportIndex} successful response must echo its submitted receipt`);
        assert(row.response.outputIds?.includes(outputId),
          `preview transport ${row.transportIndex} successful response must echo its submitted output`);
      }
      const compatible = rows.filter(candidate => candidate.classification === 'reconcile'
        && candidate.transportIndex < row.transportIndex
        && candidate.ownerScope === row.ownerScope
        && candidate.settlement === 'response'
        && candidate.status >= 200 && candidate.status < 300
        && candidate.settlementEventIndex < row.requestEventIndex
        && candidate.response.receiptIds?.includes(receiptId)
        && candidate.response.outputIds?.includes(outputId)
        && ownerChangingCommands(rows, row.ownerScope,
          candidate.requestBody.draftVersion,
          candidate.requestBody.draftDigest,
          candidate.requestEventIndex,
          row.settlementEventIndex).length === 0);
      row.currentReceiptProducerTransportIndices = compatible.map(candidate => candidate.transportIndex);
      row.hasInitialCurrentReceiptProducer = initialReceipt === receiptId
        && initialOutputId === outputId
        && ownerChangingCommands(rows, row.ownerScope,
          initialObservation.draftVersion,
          initialObservation.draftDigest,
          0,
          row.settlementEventIndex).length === 0;
      assert(compatible.length > 0 || row.hasInitialCurrentReceiptProducer,
        `preview transport ${row.transportIndex} must have a current captured or initial receipt producer`);
    }
  }

  for (const row of rows) {
    if (row.classification !== 'reconcile') continue;
    const body = row.requestBody;
    nonEmptyString(row.requestId, `reconcile transport ${row.transportIndex} owner requestId`);
    const ownerMatch = /^builder-(\d+)-(\d+)$/.exec(row.requestId);
    assert(ownerMatch,
      `reconcile transport ${row.transportIndex} must expose its compile generation and retry attempt`);
    assert(Number.isInteger(body.draftVersion) && body.draftVersion > 0,
      `reconcile transport ${row.transportIndex} must carry its current draft version`);
    nonEmptyString(body.draftDigest, `reconcile transport ${row.transportIndex} draft digest`);
    nonEmptyString(body.snapshotToken, `reconcile transport ${row.transportIndex} snapshot token`);
    const response = row.response;
    const matchingProposals = rows.filter(candidate => candidate.classification === 'modeled'
      && ['construction-proposals', 'construction-choice-proposals'].includes(candidate.path)
      && candidate.ownerScope === row.ownerScope
      && candidate.transportIndex !== row.transportIndex
      && candidate.settlement === 'response'
      && candidate.status === 200
      && candidate.settlementEventIndex < row.settlementEventIndex
      && candidate.response.receiptIds?.includes(response?.receiptId)
      && candidate.response.snapshotToken === response?.snapshotToken
      && (body.outputId === undefined || candidate.response.outputIds?.includes(body.outputId))
      && typeof response?.intentDigest === 'string'
      && response.intentDigest.length > 0
      && candidate.response.candidateWorkspaceDigest === response.intentDigest);
    const previewConsumers = rows.filter(candidate => candidate.method === 'POST'
      && candidate.path === 'preview'
      && candidate.ownerScope === row.ownerScope
      && candidate.transportIndex > row.transportIndex
      && candidate.requestEventIndex > row.settlementEventIndex
      && candidate.previewDisposition === 'successful-response'
      && candidate.responseOwnerIndex === candidate.transportIndex
      && candidate.requestBody.receiptId === response?.receiptId
      && (body.outputId === undefined || candidate.requestBody.outputId === body.outputId)
      && response?.outputIds?.includes(candidate.requestBody.outputId)
      && candidate.response.receiptIds?.includes(response?.receiptId)
      && candidate.response.outputIds?.includes(candidate.requestBody.outputId)
      && ownerChangingCommands(rows, row.ownerScope,
        body.draftVersion,
        body.draftDigest,
        row.requestEventIndex,
        candidate.settlementEventIndex).length === 0);
    row.reconcileOwner = {
      requestId: row.requestId,
      compileGeneration: Number(ownerMatch[1]),
      attempt: Number(ownerMatch[2]),
    };
    row.reconcileCAS = {
      snapshotToken: body.snapshotToken,
      draftVersion: body.draftVersion,
      draftDigest: body.draftDigest,
      outputId: body.outputId ?? null,
    };
    row.compatibleProposalTransportIndices = matchingProposals.map(candidate => candidate.transportIndex);
    row.successfulPreviewConsumerTransportIndices = previewConsumers.map(candidate => candidate.transportIndex);
    if (row.settlement === 'response' && response.status === 200) {
      nonEmptyString(response.receiptId, `reconcile transport ${row.transportIndex} receiptId`);
      assert.equal(response.snapshotToken, body.snapshotToken,
        `reconcile transport ${row.transportIndex} receipt must bind its requested snapshot`);
      assert(Array.isArray(response.outputIds) && response.outputIds.length > 0,
        `reconcile transport ${row.transportIndex} receipt must expose output identities`);
      if (body.outputId) assert(response.outputIds.includes(body.outputId),
        `reconcile transport ${row.transportIndex} receipt must include its selected output`);
      const changedCommandsBeforeResponse = ownerChangingCommands(rows, row.ownerScope,
        body.draftVersion, body.draftDigest, row.requestEventIndex, row.settlementEventIndex);
      const invalidatedBy = rows.filter(candidate => changedCommandsBeforeResponse.includes(candidate)
        || candidate.classification === 'reconcile'
          && candidate.ownerScope === row.ownerScope
          && candidate.requestEventIndex > row.requestEventIndex
          && candidate.requestEventIndex < row.settlementEventIndex
          && Number(/^builder-(\d+)-/.exec(candidate.requestId ?? '')?.[1] ?? 0) > Number(ownerMatch[1]));
      row.invalidatedByTransportIndices = invalidatedBy.map(candidate => candidate.transportIndex);
      const ownerChanging = ownerChangingCommands(rows, row.ownerScope,
        body.draftVersion,
        body.draftDigest,
        row.requestEventIndex,
        Number.POSITIVE_INFINITY);
      row.reconcileOwnerChangeCaptureIds = ownerChanging
        .filter(candidate => candidate.classification === 'modeled')
        .map(candidate => candidate.requestCaptureId);
      row.reconcileOwnerChangeSetupCommandIds = ownerChanging
        .filter(candidate => candidate.classification === 'setup')
        .map(candidate => candidate.requestBody.commandId);
      row.ownerInvalidationObserved = row.invalidatedByTransportIndices.length > 0;
      row.reconcileDisposition = row.ownerInvalidationObserved
        ? 'invalidated-by-captured-owner-event'
        : 'no-captured-owner-invalidation';
    } else if (row.settlement === 'response') {
      row.ownerInvalidationObserved = false;
      row.reconcileDisposition = 'rejected';
      row.compatibleProposalTransportIndices = [];
      row.successfulPreviewConsumerTransportIndices = [];
      row.invalidatedByTransportIndices = [];
      row.reconcileOwnerChangeCaptureIds = [];
      row.reconcileOwnerChangeSetupCommandIds = [];
    } else {
      row.ownerInvalidationObserved = false;
      row.reconcileDisposition = 'request-failed';
      row.compatibleProposalTransportIndices = [];
      row.successfulPreviewConsumerTransportIndices = [];
      row.invalidatedByTransportIndices = [];
      row.reconcileOwnerChangeCaptureIds = [];
      row.reconcileOwnerChangeSetupCommandIds = [];
    }
  }

  return rows;
}

function draftIdentity(snapshot, label) {
  record(snapshot, `${label} saved snapshot`);
  assert(Number.isInteger(snapshot.draftVersion) && snapshot.draftVersion >= 0,
    `${label} must include an integer draft version`);
  nonEmptyString(snapshot.draftDigest, `${label} draft digest`);
  nonEmptyString(snapshot.snapshotToken, `${label} snapshot token`);
  record(snapshot.workspace, `${label} full saved workspace`);
  record(snapshot.projection, `${label} finite workspace projection`);
  record(snapshot.group, `${label} Group record`);
  finiteJSON(snapshot.projection, `${label} workspace projection`);
  finiteJSON(snapshot.group, `${label} Group record`);
  return snapshot;
}

function validateExpected(expected, fixtureIDs) {
  record(expected, 'expected contract');
  nonEmptyString(expected.outputId, 'expected outputId');
  nonEmptyString(expected.choiceId, 'expected choiceId');
  nonEmptyString(expected.field, 'expected selected field');
  nonEmptyString(expected.snapshotToken, 'expected snapshotToken');
  nonEmptyString(expected.draftDigest, 'expected draftDigest');
  assert(Number.isInteger(expected.draftVersion) && expected.draftVersion >= 0,
    'expected draftVersion must be a non-negative integer');
  nonEmptyString(expected.proposalPath, 'expected proposalPath');
  nonEmptyString(expected.commandPath, 'expected commandPath');
  record(expected.one, 'expected ONE contract');
  record(expected.all, 'expected ALL contract');
  record(expected.apply, 'expected Apply contract');
  nonEmptyString(expected.one.policy, 'expected ONE policy');
  nonEmptyString(expected.one.errorCode, 'expected ONE error code');
  nonEmptyString(expected.all.policy, 'expected ALL policy');
  nonEmptyString(expected.all.previewStatus, 'expected ALL preview status');
  nonEmptyString(expected.apply.policy, 'expected Apply policy');
  assert(Number.isInteger(expected.one.status), 'expected ONE status must be an integer');
  assert(Number.isInteger(expected.all.status), 'expected ALL status must be an integer');
  assert(Number.isInteger(expected.apply.status), 'expected Apply status must be an integer');
  assert(Number.isInteger(expected.all.rowCount) && expected.all.rowCount > 0,
    'expected ALL rowCount must be a positive integer');
  if (expected.all.rowIndex !== undefined) {
    assert(Number.isInteger(expected.all.rowIndex) && expected.all.rowIndex >= 0,
      'expected ALL rowIndex must be a non-negative integer');
  }
  nonEmptyString(expected.all.memberIdsField, 'expected ALL member IDs field');
  nonEmptyString(expected.all.countField, 'expected ALL count field');
  assert(Number.isInteger(expected.all.count), 'expected ALL count must be an integer');
  assert.equal(expected.apply.policy, expected.all.policy,
    'Apply must persist the expected ALL policy');
  assert(Object.hasOwn(expected.apply, 'inputColumnBinding'),
    'expected Apply contract must include the new input column binding');
  finiteJSON(expected.apply.inputColumnBinding, 'expected Apply input column binding');
  record(expected.apply.outputColumnBinding, 'expected Apply output column binding');
  nonEmptyString(expected.apply.outputColumnBinding.columnId, 'expected Apply output column ID');
  assert.equal(expected.apply.outputColumnBinding.type, 'array',
    'ALL Apply must declare an array output column');
  assert.notEqual(expected.apply.inputColumnBinding.columnId, expected.apply.outputColumnBinding.columnId,
    'ALL Apply input and output column IDs must be distinct');
  assert.notEqual(expected.one.policy, expected.all.policy,
    'ONE and ALL must be distinct expected policies');
  assertUniqueIDs(fixtureIDs, 'independent fixture IDs');
  if (expected.form !== undefined) nonEmptyString(expected.form, 'expected choice form');
  record(expected.editorIdentity, 'expected editor identity');
  nonEmptyString(expected.editorIdentity.editorId, 'expected editor mount identity');
  assert(Number.isFinite(expected.editorIdentity.documentTimeOrigin),
    'expected editor documentTimeOrigin must be finite');
}

function inspectRequest(request, label, expectedPath, expected, policy, { linkedCommandId } = {}) {
  record(request, `${label} request capture`);
  assert(request.requestObject && (typeof request.requestObject === 'object' || typeof request.requestObject === 'function'),
    `${label} must retain its actual request object`);
  assert.equal(request.method, 'POST', `${label} must be a POST`);
  pathMatches(request.path, expectedPath, label);
  nonEmptyString(request.captureId, `${label} captureId`);
  assert(Number.isInteger(request.transportIndex) && request.transportIndex > 0,
    `${label} must retain its raw observer transport index`);
  const requestId = optionalString(request.requestId, `${label} requestId`);
  finiteJSON(request.body, `${label} full request body`);
  const input = choiceProposalInput(request.body, label);
  const expectedRequestFields = {
    snapshotToken: expected.snapshotToken,
    expectedDraftVersion: expected.draftVersion,
    expectedDraftDigest: expected.draftDigest,
  };
  if (label !== 'Apply') expectedRequestFields.outputId = expected.outputId;
  for (const [field, value] of Object.entries(expectedRequestFields)) {
    assert.deepEqual(input[field], value, `${label} request must carry the current ${field}`);
  }
  const capturedCommandId = request.commandId ?? undefined;
  const bodyCommandId = input.commandId ?? undefined;
  const commandId = capturedCommandId ?? bodyCommandId;
  if (capturedCommandId !== undefined && bodyCommandId !== undefined) {
    assert.equal(capturedCommandId, bodyCommandId, `${label} capture commandId must match its full request body`);
  }
  if (commandId !== undefined) nonEmptyString(commandId, `${label} commandId`);

  let selected;
  if (label === 'Apply') {
    assert.equal(commandId, linkedCommandId,
      'Apply must reuse the successful ALL response commandId');
    const commands = input.commands;
    assert(Array.isArray(commands) && commands.length === 1,
      'Apply request must contain exactly one command');
    const [command] = commands;
    record(command, 'Apply command');
    assert.equal(command.type, 'APPLY_CONSTRUCTION_CHOICE', 'Apply must submit a construction choice');
    assert.equal(command.outputId, expected.outputId, 'Apply command must target the expected output');
    selected = record(command.constructionChoice, 'Apply construction choice');
  } else {
    const choices = input.constructionChoices;
    assert(Array.isArray(choices) && choices.length === 1,
      `${label} request must contain exactly one construction choice`);
    [selected] = choices;
    record(selected, `${label} construction choice`);
  }
  assert.equal(selected.choiceId, expected.choiceId, `${label} must retain the exact selected field choice`);
  assert.equal(selected.rowValuePolicy, policy, `${label} request must use ${policy}`);
  if (expected.form !== undefined) {
    assert.equal(selected.form, expected.form, `${label} must retain the expected choice form`);
  }
  const requestObjectId = request.requestObjectId === undefined
    ? request.captureId
    : nonEmptyString(request.requestObjectId, `${label} requestObjectId`);
  return {
    requestObject: request.requestObject,
    requestObjectId,
    requestId,
    captureId: request.captureId,
    transportIndex: request.transportIndex,
    commandId: commandId ?? POLICY_TRACE_ABSENT,
    input,
    selected,
  };
}

function inspectResponse(response, request, label, expectedStatus) {
  record(response, `${label} response capture`);
  assert(response.responseObject && (typeof response.responseObject === 'object' || typeof response.responseObject === 'function'),
    `${label} must retain its actual response object`);
  assert.equal(response.requestObject, request.requestObject,
    `${label} response must be owned by the exact captured request object`);
  assert.equal(response.captureId, request.captureId,
    `${label} response captureId must match its captured request`);
  const responseRequestId = optionalString(response.requestId, `${label} response requestId`);
  if (request.requestId !== POLICY_TRACE_ABSENT && responseRequestId !== POLICY_TRACE_ABSENT) {
    assert.equal(responseRequestId, request.requestId, `${label} response requestId must match its request header`);
  }
  if (response.commandId !== undefined && response.commandId !== null) {
    nonEmptyString(response.commandId, `${label} echoed commandId`);
    if (request.commandId !== POLICY_TRACE_ABSENT) {
      assert.equal(response.commandId, request.commandId,
        `${label} response commandId must match its actual request`);
    }
  }
  assert.equal(response.status, expectedStatus, `${label} response status must be ${expectedStatus}`);
  return {
    requestId: responseRequestId,
    captureId: response.captureId,
    requestObjectId: request.requestObjectId,
    transportIndex: request.transportIndex,
    commandId: response.commandId ?? POLICY_TRACE_ABSENT,
    status: response.status,
    code: response.errorCode ?? POLICY_TRACE_ABSENT,
  };
}

function inspectEditor(dom, label, expected, policy, { requireRefusalVisible = false } = {}) {
  record(dom, `${label} DOM observation`);
  assert.equal(dom.mounted, true, `${label} must retain the mounted choice editor`);
  assert.equal(dom.documentId, expected.outputId,
    `${label} must retain the Builder output document identity`);
  assert.equal(dom.editorId, expected.editorIdentity.editorId,
    `${label} must retain the same editor identity`);
  assert.equal(dom.documentTimeOrigin, expected.editorIdentity.documentTimeOrigin,
    `${label} must retain the same document time origin`);
  assert.equal(dom.selectedField, expected.field, `${label} must retain the exact selected field`);
  assert.equal(dom.policy, policy, `${label} must select ${policy}`);
  assert.equal(dom.fieldSelected, true, `${label} must retain the selected field`);
  if (expected.form !== undefined) assert.equal(dom.form, expected.form, `${label} must retain the expected form`);
  assert(Array.isArray(dom.availablePolicies) && dom.availablePolicies.includes(expected.all.policy),
    `${label} must keep ${expected.all.policy} available`);
  if (requireRefusalVisible) {
    assert.equal(dom.errorVisible, true, `${label} must retain the visible ONE refusal`);
  }
  return {
    documentId: dom.documentId,
    editorId: dom.editorId,
    documentTimeOrigin: dom.documentTimeOrigin,
    mounted: dom.mounted,
    allAvailable: true,
  };
}

function inspectClosedEditor(dom, label, expected) {
  record(dom, `${label} DOM observation`);
  assert.equal(dom.mounted, false, `${label} must observe the Add Columns editor closed`);
  assert.equal(dom.documentId, null, `${label} must not claim a mounted output document`);
  assert.equal(dom.editorId, null, `${label} must not retain a mounted editor identity`);
  assert.equal(dom.documentTimeOrigin, expected.editorIdentity.documentTimeOrigin,
    `${label} must remain in the same browser document after Apply`);
  assert.equal(dom.selectedField, null, `${label} must not claim a selected field after editor closure`);
  assert.equal(dom.fieldSelected, false, `${label} field control must be absent after editor closure`);
  return {
    documentId: POLICY_TRACE_ABSENT,
    editorId: POLICY_TRACE_ABSENT,
    documentTimeOrigin: dom.documentTimeOrigin,
    mounted: false,
    allAvailable: false,
  };
}

function inspectSavedSnapshots(saved, expected) {
  record(saved, 'saved Builder reads');
  const before = draftIdentity(saved.before, 'before ONE');
  const afterOne = draftIdentity(saved.afterOne, 'after ONE rejection');
  const afterApply = draftIdentity(saved.afterApply, 'after Apply');
  for (const snapshot of [before, afterOne, afterApply]) {
    assert.equal(snapshot.snapshotToken, expected.snapshotToken, 'the saved Builder read must retain its snapshot token');
  }
  for (const field of ['workspace', 'projection', 'group']) {
    assert(isDeepStrictEqual(before[field], afterOne[field]),
      `ONE rejection must preserve the exact saved ${field}`);
  }
  assert.equal(afterOne.draftVersion, before.draftVersion,
    'ONE rejection must preserve the saved draft version');
  assert.equal(afterOne.draftDigest, before.draftDigest,
    'ONE rejection must preserve the saved draft digest');
  assert.equal(before.draftVersion, expected.draftVersion,
    'the before Builder read must match the proposed draft version');
  assert.equal(before.draftDigest, expected.draftDigest,
    'the before Builder read must match the proposed draft digest');
  assert(afterApply.draftVersion > before.draftVersion,
    'Apply must persist a newer Builder draft version');
  assert.notEqual(afterApply.draftDigest, before.draftDigest,
    'Apply must persist a changed Builder draft digest');
  assert.equal(before.group.present, true, 'the source Group must be present before Apply');
  assert.equal(afterApply.group.present, true, 'the saved Group must remain present after Apply');
  for (const field of ['keys', 'aggregate', 'rowCount', 'count']) {
    assert(isDeepStrictEqual(before.group[field], afterApply.group[field]),
      `Apply must preserve the saved Group ${field}`);
  }
  assert.equal(afterApply.group.rowValuePolicy, expected.apply.policy,
    'the saved Group must persist the ALL row-value policy');
  assert(!isDeepStrictEqual(before.group.inputColumnBinding, expected.apply.inputColumnBinding),
    'Apply must add a new Group input column binding');
  assert(isDeepStrictEqual(afterApply.group.inputColumnBinding, expected.apply.inputColumnBinding),
    'the saved Group must include the exact added input column binding');
  assert(!isDeepStrictEqual(before.group.outputColumnBinding, expected.apply.outputColumnBinding),
    'Apply must add a new Group output column binding');
  assert(isDeepStrictEqual(afterApply.group.outputColumnBinding, expected.apply.outputColumnBinding),
    'the saved Group must include the exact declared ALL output binding');
  assert.notEqual(afterApply.group.inputColumnBinding.columnId, afterApply.group.outputColumnBinding.columnId,
    'the saved Group must retain distinct row-value input and output column IDs');
  assert.equal(afterApply.policy, expected.apply.policy,
    'the saved Builder read after Apply must retain the expected policy');
  assert.equal(afterApply.choiceId, expected.choiceId,
    'the saved Builder read after Apply must retain the exact choice');
  assert.equal(afterApply.outputId, expected.outputId,
    'the saved Builder read after Apply must retain the exact output');
  finiteJSON(afterApply.projection, 'after Apply workspace projection');
  return { before, afterOne, afterApply };
}

function requestObservation(request, {
  requestId = request.requestId,
  responseCommandId = POLICY_TRACE_ABSENT,
  status = POLICY_TRACE_ABSENT,
  code = POLICY_TRACE_ABSENT,
} = {}) {
  return {
    requestId,
    captureId: request.captureId,
    requestObjectId: request.requestObjectId,
    transportIndex: request.transportIndex ?? 0,
    commandId: request.commandId,
    responseCommandId,
    status,
    code,
    candidateId: POLICY_TRACE_ABSENT,
    candidateRequestId: POLICY_TRACE_ABSENT,
    candidateCaptureId: POLICY_TRACE_ABSENT,
  };
}

function emptyRequestObservation() {
  return requestObservation({
    requestId: POLICY_TRACE_ABSENT,
    captureId: POLICY_TRACE_ABSENT,
    requestObjectId: POLICY_TRACE_ABSENT,
    commandId: POLICY_TRACE_ABSENT,
  });
}

function choiceEvent(choiceId, field, rowValuePolicy, status) {
  return { choiceId, field, rowValuePolicy, status };
}

function savedEvent(snapshot, policy = POLICY_TRACE_ABSENT) {
  return {
    workspace: finiteClone(snapshot.projection, 'saved workspace projection'),
    draftVersion: snapshot.draftVersion,
    draftDigest: snapshot.draftDigest,
    group: finiteClone(snapshot.group, 'saved Group record'),
    policy,
  };
}

function emptyPreview() {
  return {
    outputId: POLICY_TRACE_ABSENT,
    receiptId: POLICY_TRACE_ABSENT,
    rowCount: POLICY_TRACE_ABSENT,
    memberIds: POLICY_TRACE_ABSENT,
    count: POLICY_TRACE_ABSENT,
  };
}

function policyObservation(request, response, choice, saved, editor, preview = emptyPreview()) {
  return {
    request: request ? response ? responseObservation(request, response) : requestObservation(request) : emptyRequestObservation(),
    choice,
    saved,
    editor,
    preview,
  };
}

function captureRequest(request, summary) {
  return {
    method: request.method,
    path: request.path,
    requestId: request.requestId ?? null,
    captureId: request.captureId,
    requestObjectId: summary.requestObjectId,
    transportIndex: request.transportIndex,
    commandId: summary.commandId === POLICY_TRACE_ABSENT ? null : summary.commandId,
    body: finiteClone(request.body, 'captured request body'),
  };
}

function captureResponse(response, summary) {
  return {
    status: response.status,
    requestId: response.requestId ?? null,
    captureId: response.captureId,
    requestObjectId: summary.requestObjectId,
    transportIndex: summary.transportIndex,
    commandId: response.commandId ?? null,
    errorCode: response.errorCode ?? null,
    ...(response.preview === undefined ? {} : { preview: finiteClone(response.preview, 'captured response preview') }),
    ...(response.body === undefined ? {} : { body: finiteClone(response.body, 'captured response body') }),
  };
}

function policyAction({
  label,
  event: eventType,
  requestCapture,
  requestSummary,
  responseCapture,
  responseSummary,
  choice,
  saved,
  editor,
  preview,
}) {
  return {
    label,
    event: eventType,
    ...(requestCapture ? { request: captureRequest(requestCapture, requestSummary) } : {}),
    ...(responseCapture ? { response: captureResponse(responseCapture, requestSummary) } : {}),
    ...(responseCapture ? { settlement: { kind: 'http-response', captureId: requestCapture.captureId } } : {}),
    observation: policyObservation(requestSummary ?? emptyRequestObservation(), responseSummary, choice, saved, editor, preview),
  };
}

function sourceActionLabelIndex(actions, label) {
  const matches = actions.flatMap((action, index) => action.label === label ? [index] : []);
  assert.equal(matches.length, 1, `workflow context must contain exactly one ${label} action`);
  return matches[0];
}

function responseObservation(request, response) {
  return requestObservation(request, {
    requestId: response.requestId,
    responseCommandId: response.commandId,
    status: response.status,
    code: response.code,
  });
}

function inspectAllPreview(response, expected, fixtureIDs) {
  record(response.preview, 'ALL preview');
  const preview = response.preview;
  nonEmptyString(preview.receiptId, 'ALL preview receiptId');
  assert.equal(preview.outputId, expected.outputId, 'ALL preview must target the expected output');
  assert.equal(preview.status, expected.all.previewStatus,
    `ALL preview status must be ${expected.all.previewStatus}`);
  assert.equal(preview.rowCount, expected.all.rowCount, 'ALL preview rowCount must match the expected count');
  assert(Array.isArray(preview.rows), 'ALL preview must include rows');
  assert.equal(preview.rows.length, preview.rowCount,
    'ALL preview row array length must match its reported rowCount');
  assert.equal(preview.rows.length, expected.all.rowCount,
    'ALL preview must contain the exact expected row count');
  const rowIndex = expected.all.rowIndex ?? 0;
  assert(Number.isInteger(rowIndex) && rowIndex >= 0 && rowIndex < preview.rows.length,
    'expected ALL rowIndex must select a reported grouped row');
  const row = record(preview.rows[rowIndex], 'ALL preview grouped row');
  const memberIds = row[expected.all.memberIdsField];
  assert(Array.isArray(memberIds), 'ALL preview row must expose the selected member ID array');
  sameIDSet(memberIds, fixtureIDs, 'ALL preview member IDs');
  assert.equal(row[expected.all.countField], expected.all.count,
    'ALL preview must preserve the expected grouped row count');
  return {
    outputId: preview.outputId,
    receiptId: preview.receiptId,
    rowCount: preview.rowCount,
    memberIds: [...memberIds].sort(),
    count: row[expected.all.countField],
  };
}

function traceActionRecords(context) {
  assert(Array.isArray(context.actions), 'workflow context actions must be an array');
  const actions = finiteClone(context.actions, 'workflow-owned actions');
  const groupAppliedIndex = sourceActionLabelIndex(actions, 'GroupApplied');
  const reloadAllIndex = sourceActionLabelIndex(actions, 'ReloadedAll');
  assert.equal(reloadAllIndex, groupAppliedIndex + 1,
    'workflow context must leave the policy slice between GroupApplied and ReloadedAll');
  return actions;
}

/** Validate captured choice evidence and insert its model observations into the canonical journey trace. */
export function buildPolicyOneTrace({ expected, requests, responses, saved, dom, fixtureIDs, context }) {
  validateExpected(expected, fixtureIDs);
  record(requests, 'request captures');
  record(responses, 'response captures');
  record(dom, 'DOM observations');
  record(context, 'workflow trace context');
  const formalExit = formalExitFromCaptures(context.transportCaptures, context.formalExit);

  const oneRequest = inspectRequest(requests.one, 'ONE', expected.proposalPath, expected, expected.one.policy);
  const allRequest = inspectRequest(requests.all, 'ALL', expected.proposalPath, expected, expected.all.policy);
  nonEmptyString(allRequest.commandId === POLICY_TRACE_ABSENT ? undefined : allRequest.commandId,
    'successful ALL request commandId');
  const oneResponse = inspectResponse(responses.one, oneRequest, 'ONE', expected.one.status);
  assert.equal(responses.one.errorCode, expected.one.errorCode,
    `ONE response must report ${expected.one.errorCode}`);
  const allResponse = inspectResponse(responses.all, allRequest, 'ALL', expected.all.status);
  assert.equal(allResponse.commandId, allRequest.commandId,
    'successful ALL response must echo its actual request commandId');
  const preview = inspectAllPreview(responses.all, expected, fixtureIDs);

  const beforeOneEditor = inspectEditor(dom.beforeOne, 'chooser before ONE submission', expected, expected.one.policy);
  const afterOneEditor = inspectEditor(dom.afterOne, 'chooser after ONE rejection', expected, expected.one.policy,
    { requireRefusalVisible: true });
  const selectedAllEditor = inspectEditor(dom.afterSelectAll, 'chooser after selecting ALL', expected, expected.all.policy,
    { requireRefusalVisible: true });
  assert.deepEqual(selectedAllEditor, afterOneEditor,
    'selecting ALL must retain the same mounted editor identity');
  const allSubmittedEditor = inspectEditor(dom.afterAllSubmitted, 'chooser after ALL submission', expected, expected.all.policy);
  const allReadyEditor = inspectEditor(dom.afterAllReady, 'chooser after ALL preview', expected, expected.all.policy);
  const applySubmittedEditor = inspectEditor(dom.afterApplySubmitted, 'chooser after Apply submission', expected,
    expected.all.policy);
  const appliedEditor = inspectClosedEditor(dom.afterApply, 'chooser after successful Apply', expected);
  const snapshots = inspectSavedSnapshots(saved, expected);
  const applyRequest = inspectRequest(requests.apply, 'Apply', expected.commandPath, expected, expected.apply.policy,
    { linkedCommandId: allResponse.commandId });
  assert.equal(applyRequest.selected.form, allRequest.selected.form,
    'Apply must retain the exact successful ALL choice form');
  const requestSummaries = [oneRequest, allRequest, applyRequest];
  assertUniqueIDs(requestSummaries.map(request => request.captureId), 'actual observer captureIds');
  assertUniqueIDs(requestSummaries.map(request => request.requestObjectId), 'actual requestObjectIds');
  assert.equal(new Set(requestSummaries.map(request => request.requestObject)).size, 3,
    'ONE, ALL, and Apply must be distinct actual requests');

  const applyResponse = inspectResponse(responses.apply, applyRequest, 'Apply', expected.apply.status);
  assert.equal(applyResponse.commandId, allResponse.commandId,
    'successful Apply response must echo the successful ALL commandId');
  const responseCaptures = [responses.one, responses.all, responses.apply];
  const responseObjects = responseCaptures.map(response => response.responseObject);
  assert.equal(new Set(responseObjects).size, 3, 'ONE, ALL, and Apply must have distinct actual responses');
  assert(responseObjects.every((responseObject, index) => responseObject !== requestSummaries[index].requestObject),
    'captured responses must be distinct from their actual request objects');
  record(responses.apply.body, 'Apply response body');
  finiteJSON(responses.apply.body, 'Apply response body');
  assert.equal(responses.apply.body.draftVersion, snapshots.afterApply.draftVersion,
    'Apply response draft version must match the independent saved Builder read');
  assert.equal(responses.apply.body.draftDigest, snapshots.afterApply.draftDigest,
    'Apply response draft digest must match the independent saved Builder read');
  if (responses.apply.body.workspace !== undefined) {
    assert(isDeepStrictEqual(responses.apply.body.workspace, snapshots.afterApply.workspace),
      'Apply response workspace must match the independent saved Builder read');
  }

  const oneSaved = savedEvent(snapshots.before);
  const afterOneSaved = savedEvent(snapshots.afterOne);
  const afterApplySaved = savedEvent(snapshots.afterApply, expected.apply.policy);
  const oneChoice = choiceEvent(expected.choiceId, expected.field, expected.one.policy, 'pending');
  const rejectedChoice = choiceEvent(expected.choiceId, expected.field, expected.one.policy, 'error');
  const selectedAllErrorChoice = choiceEvent(expected.choiceId, expected.field, expected.all.policy, 'error');
  const allPendingChoice = choiceEvent(expected.choiceId, expected.field, expected.all.policy, 'pending');
  const allReadyChoice = choiceEvent(expected.choiceId, expected.field, expected.all.policy, 'ready');
  const allAppliedChoice = choiceEvent(expected.choiceId, expected.field, expected.apply.policy, 'applied');
  const policyActions = [
    policyAction({
      label: 'OneSubmitted', event: 'choice-preview', requestCapture: requests.one, requestSummary: oneRequest,
      choice: oneChoice, saved: oneSaved, editor: beforeOneEditor,
    }),
    policyAction({
      label: 'OneRejected', event: 'choice-preview-response', requestCapture: requests.one, requestSummary: oneRequest,
      responseCapture: responses.one, responseSummary: oneResponse, choice: rejectedChoice,
      saved: afterOneSaved, editor: afterOneEditor,
    }),
    policyAction({
      label: 'SelectAll', event: 'choice-policy-selected', choice: selectedAllErrorChoice,
      saved: afterOneSaved, editor: selectedAllEditor,
    }),
    policyAction({
      label: 'AllSubmitted', event: 'choice-preview', requestCapture: requests.all, requestSummary: allRequest,
      choice: allPendingChoice, saved: afterOneSaved, editor: allSubmittedEditor,
    }),
    policyAction({
      label: 'AllReady', event: 'choice-preview-response', requestCapture: requests.all, requestSummary: allRequest,
      responseCapture: responses.all, responseSummary: allResponse, choice: allReadyChoice,
      saved: afterOneSaved, editor: allReadyEditor, preview,
    }),
    policyAction({
      label: 'AllApplySubmitted', event: 'apply-construction-choice',
      requestCapture: requests.apply, requestSummary: applyRequest, choice: allReadyChoice,
      saved: afterOneSaved, editor: applySubmittedEditor, preview,
    }),
    policyAction({
      label: 'AllApplied', event: 'apply-construction-choice-response', requestCapture: requests.apply,
      requestSummary: applyRequest, responseCapture: responses.apply, responseSummary: applyResponse,
      choice: allAppliedChoice, saved: afterApplySaved, editor: appliedEditor, preview,
    }),
  ];

  const actions = traceActionRecords(context);
  const groupAppliedIndex = sourceActionLabelIndex(actions, 'GroupApplied');
  actions.splice(groupAppliedIndex + 1, 0, ...policyActions);
  const traceName = nonEmptyString(context.name, 'journey trace name');
  assert.equal(context.model, 'PolicyOneDisagreement', 'journey trace must use the PolicyOneDisagreement model');
  record(context.initialObservation, 'SourceReady initial observation');
  record(context.initialPreview, 'initial source-grid observation');
  record(context.outcome, 'journey outcome projection');
  const transportLedger = canonicalTransportLedger(
    context.transportCaptures,
    modeledRequestReferences(requests, context),
    context.initialPreview,
    context.initialObservation,
    actions,
    context.formalEntry,
    formalExit,
  );
  return {
    journeyTrace: {
      name: traceName,
      model: context.model,
      initialObservation: finiteClone(context.initialObservation, 'SourceReady initial observation'),
      initialPreview: finiteClone(context.initialPreview, 'initial source-grid observation'),
      actions,
      transportLedger,
      formalEntry: finiteClone(context.formalEntry, 'formal entry boundary and initial saved snapshot'),
      formalExit: finiteClone(formalExit, 'formal exit boundary and pending support requests'),
      outcome: finiteClone(context.outcome, 'journey outcome projection'),
    },
  };
}
