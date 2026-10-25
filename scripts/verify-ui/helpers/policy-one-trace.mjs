import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';

export const POLICY_TRACE_ABSENT = 'Absent';

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
  const requestId = optionalString(request.requestId, `${label} requestId`);
  finiteJSON(request.body, `${label} full request body`);
  const input = choiceProposalInput(request.body, label);
  for (const [field, value] of Object.entries({
    outputId: expected.outputId,
    snapshotToken: expected.snapshotToken,
    expectedDraftVersion: expected.draftVersion,
    expectedDraftDigest: expected.draftDigest,
  })) {
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
    requestId: responseRequestId === POLICY_TRACE_ABSENT ? request.requestId : responseRequestId,
    captureId: response.captureId,
    requestObjectId: request.requestObjectId,
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
  assert.equal(dom.fieldChecked, true, `${label} must retain the selected field checkbox`);
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
  assert(isDeepStrictEqual(before.group, afterApply.group),
    'Apply must preserve the saved Group record');
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

function emptyEditor() {
  return {
    documentId: POLICY_TRACE_ABSENT,
    editorId: POLICY_TRACE_ABSENT,
    documentTimeOrigin: POLICY_TRACE_ABSENT,
    mounted: POLICY_TRACE_ABSENT,
    allAvailable: POLICY_TRACE_ABSENT,
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
      choice: allAppliedChoice, saved: afterApplySaved, editor: emptyEditor(), preview,
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
  const unmodeledEvents = finiteClone(context.unmodeledEvents ?? [], 'unmodeled events');
  const backendCalls = finiteClone(context.backendCalls ?? [], 'backend calls');
  assert(Array.isArray(unmodeledEvents), 'unmodeled events must be an array');
  assert(Array.isArray(backendCalls), 'backend calls must be an array');
  return {
    journeyTrace: {
      name: traceName,
      model: context.model,
      initialObservation: finiteClone(context.initialObservation, 'SourceReady initial observation'),
      initialPreview: finiteClone(context.initialPreview, 'initial source-grid observation'),
      actions,
      unmodeledEvents,
      backendCalls,
      outcome: finiteClone(context.outcome, 'journey outcome projection'),
    },
  };
}
