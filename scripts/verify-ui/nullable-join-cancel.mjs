const apiPath = (scope, suffix) =>
  `/api/v1/projects/${scope.project}/explorers/${scope.explorer}${suffix}`;

const same = (left, right) => JSON.stringify(left) === JSON.stringify(right);
const present = value => typeof value === 'string' && value.length > 0;
const isNullableJoinCancellationPath = (method, pathname, selectionId) =>
  (method === 'POST' && pathname.endsWith('/construction-capabilities'))
  || (method === 'GET' && pathname.endsWith(`/selections/${selectionId}`));

const reject = reason => ({ ok: false, reason });

const selectionIsBound = (report, scope, owner) =>
  report.assertions?.some(assertion => assertion.status === 'passed'
    && assertion.evidence?.project === scope.project
    && assertion.evidence?.generation === scope.generation
    && assertion.evidence?.outputId === owner.outputId
    && assertion.evidence?.selectionId === owner.selectionId
    && Array.isArray(assertion.evidence?.selectionRefs)
    && assertion.evidence.selectionRefs.length > 0
    && assertion.evidence.selectionRefs.every(ref => typeof ref === 'string'
      && ref.startsWith(`${scope.project}/${scope.generation}/`))) === true;

const proofIsBound = (report, { scope, action, outgoing, next }, failedAt) => {
  if (next.proof.kind === 'visible-rows') {
    if (!Array.isArray(next.proof.expectedRows) || next.proof.expectedRows.length === 0) return false;
    const expected = next.proof.expectedRows.map(row => JSON.stringify(row)).sort();
    return report.assertions?.some(assertion => assertion.status === 'passed'
      && assertion.evidence?.outputId === next.outputId
      && assertion.evidence?.selectionId === next.selectionId
      && Array.isArray(assertion.evidence?.expectedRows)
      && Array.isArray(assertion.evidence?.rows)
      && same(assertion.evidence.expectedRows.map(row => JSON.stringify(row)).sort(), expected)
      && same(assertion.evidence.rows.map(row => JSON.stringify(row)).sort(), expected)) === true;
  }

  const nativeTargetBinding = report.assertions?.some(assertion => {
    const evidence = assertion.evidence;
    const binding = evidence?.targetBinding;
    if (assertion.status !== 'passed'
      || assertion.name !== 'Native Combine creates a scoped empty Observation target for the current-draft Join'
      || evidence?.project !== scope.project
      || evidence?.generation !== scope.generation
      || evidence?.snapshotToken !== next.draft.snapshotToken
      || binding?.ok !== true
      || binding.commandBound !== true
      || binding.resultBound !== true
      || binding.workspaceBound !== true
      || binding.editorBound !== true
      || binding.responseStatus !== 200
      || binding.outputId !== next.outputId
      || binding.mountedOutputId !== next.outputId
      || binding.workspaceTargetCount !== 1
      || binding.targetRootResourceType !== 'Observation'
      || binding.targetColumnCount !== 0
      || binding.targetConstructionSteps !== 0
      || !Array.isArray(binding.previousOutputIDs)
      || binding.previousOutputIDs.includes(next.outputId)) return false;
    return report.nativeRequests?.some(request => {
      const command = request.body?.commands;
      const created = request.response?.results?.filter(result =>
        result?.type === 'TABLE_CREATED' && present(result.outputId));
      const documents = request.response?.workspace?.documents?.filter(document =>
        document?.output?.id === next.outputId);
      const document = documents?.[0];
      return request.method === 'POST'
        && request.path === apiPath(scope, '/authoring/v2/commands')
        && request.triggerAction === action.label
        && request.status === 200
        && request.failure == null
        && request.body?.snapshotToken === outgoing.draft.snapshotToken
        && request.body?.expectedDraftVersion === outgoing.draft.version
        && request.body?.expectedDraftDigest === outgoing.draft.digest
        && request.body?.commandId === binding.commandId
        && Array.isArray(command) && command.length === 1
        && command[0]?.type === 'CREATE_TABLE'
        && command[0].rootNodeId === binding.rootNodeId
        && binding.expectedRootNodeIds?.includes(command[0].rootNodeId)
        && request.response?.commandId === binding.commandId
        && request.response?.draftVersion === next.draft.version
        && request.response?.draftDigest === next.draft.digest
        && Number.isFinite(request.responseReceivedAt)
        && request.completedAt >= request.responseReceivedAt
        && request.completedAt >= failedAt
        && created?.length === 1
        && created[0].outputId === next.outputId
        && documents?.length === 1
        && document.rootResourceType === 'Observation'
        && document.columns?.length === 0
        && (document.construction?.steps?.length ?? 0) === 0;
    }) === true;
  }) === true;
  if (nativeTargetBinding) return true;
  return false;
};

/**
 * Checks report evidence after withExpectedCancellations has observed the native
 * request failures. Context is supplied by the active workflow; no run IDs are
 * baked into this validator.
 */
const validateSwitchAbortEvidence = (report, expected, record) => {
  const { scope, action, outgoing, next } = expected ?? {};
  if (![scope?.project, scope?.generation, scope?.explorer, action?.id, action?.label, action?.locator,
    outgoing?.outputId, outgoing?.selectionId, outgoing?.draft?.snapshotToken,
    outgoing?.draft?.digest, next?.outputId, next?.draft?.snapshotToken,
    next?.draft?.digest].every(present)
    || !Number.isInteger(outgoing?.draft?.version)
    || !Number.isInteger(next?.draft?.version)
    || !(next?.selectionId === null || present(next?.selectionId))
    || !['visible-rows', 'empty-target'].includes(next?.proof?.kind)
    || (next.proof.kind === 'visible-rows'
      && (next.selectionId !== next.proof.selectionId || !Array.isArray(next.proof.expectedRows)))
    || (next.proof.kind === 'empty-target' && next.selectionId !== null)) {
    return reject('switch context must bind scope, action, outgoing and next owner CAS, and a concrete successor proof');
  }

  const capabilityPath = apiPath(scope, '/authoring/v2/construction-capabilities');
  const selectionPath = apiPath(scope, `/selections/${outgoing.selectionId}`);
  const actionRecords = (report?.actions ?? []).filter(entry => entry.id === action.id);
  const actionRecord = actionRecords[0];
  if (report?.scenario !== 'cda-workspace-combine'
    || report?.caseName !== 'nullable-code-join'
    || report?.scope?.project !== scope.project
    || report.scope.generation !== scope.generation
    || report.target?.project !== scope.project
    || report.target?.generation !== scope.generation
    || report.target?.explorer !== scope.explorer
    || report.target?.kind !== 'owned-cda'
    || actionRecords.length !== 1
    || actionRecord?.status !== 'passed'
    || record?.failureAction?.id !== action.id
    || actionRecord.locator !== action.locator) {
    return reject('report or successful UI action does not match the active scoped switch');
  }

  if (!record || !(report.network ?? []).includes(record)) {
    return reject('abort must be the exact raw entry retained in this report');
  }
  let requestURL;
  let origin;
  try {
    requestURL = new URL(record.url);
    origin = new URL(report.target.uiUrl).origin;
  } catch {
    return reject('raw abort URL is invalid');
  }
  if (record.kind !== 'network'
    || !record.playwrightRequestId?.startsWith('cda-request-')
    || record.errorText !== 'net::ERR_ABORTED'
    || requestURL.origin !== origin
    || record.requestScope?.expectedProject !== scope.project
    || record.requestScope?.generation !== scope.generation
    || record.requestScope?.configuredExplorer !== scope.explorer
    || record.requestScope?.requestProject !== scope.project
    || record.requestScope?.requestExplorer !== scope.explorer
    || record.triggerAction !== action.label
    || record.failureAction?.id !== actionRecord.id
    || record.failureAction?.label !== action.label) {
    return reject('raw abort is not bound to the active project, generation, explorer, and UI switch');
  }

  let nativeFailure;
  let browserRequestId = null;
  if (record.method === 'POST' && requestURL.pathname === capabilityPath) {
    const details = record.requestDetails;
    if (details?.outputId !== outgoing.outputId
      || details?.stageId !== 'source_projection'
      || details?.draftVersion !== outgoing.draft.version
      || details?.draftDigest !== outgoing.draft.digest) {
      return reject('capabilities abort does not match the outgoing owner and draft CAS');
    }
    browserRequestId = record.browserRequestId;
    const nativeMatches = (report.nativeRequests ?? []).filter(entry =>
      entry.browserRequestId === browserRequestId);
    nativeFailure = nativeMatches[0];
    const body = nativeFailure?.body;
    if (!present(browserRequestId) || nativeMatches.length !== 1
      || nativeFailure.method !== 'POST'
      || nativeFailure.path !== capabilityPath
      || nativeFailure.failure !== 'net::ERR_ABORTED'
      || body?.outputId !== outgoing.outputId
      || body?.stageId !== 'source_projection'
      || body?.snapshotToken !== outgoing.draft.snapshotToken
      || body?.expectedDraftVersion !== outgoing.draft.version
      || body?.expectedDraftDigest !== outgoing.draft.digest) {
      return reject('capabilities abort does not correlate to one native request with the captured owner CAS');
    }
  } else if (record.method === 'GET' && requestURL.pathname === selectionPath) {
    const details = record.requestDetails;
    if (details?.requestId !== null || details?.outputId !== null
      || details?.draftVersion !== null || details?.draftDigest !== null
      || details?.stageId !== null) {
      return reject('selection abort does not retain the expected null request detail shape');
    }
  } else {
    return reject('raw abort path does not belong to the outgoing capabilities or selection request');
  }
  if (!selectionIsBound(report, scope, outgoing)) {
    return reject('outgoing output and selection are not bound by a passed scoped source assertion');
  }
  if (next.proof.kind === 'visible-rows' && !selectionIsBound(report, scope, next)) {
    return reject('successor output and selection are not bound by a passed scoped source assertion');
  }

  let reportStartedAt;
  try {
    reportStartedAt = Date.parse(report.startedAt);
  } catch {
    return reject('report start time is invalid');
  }
  const requestAt = reportStartedAt + record.requestTimeline?.requestStartedMs;
  const failedAt = reportStartedAt + record.requestTimeline?.failedAtMs;
  if (!Number.isFinite(requestAt) || !Number.isFinite(failedAt)) {
    return reject('outgoing abort is missing request or failure timing');
  }

  const later = entry => entry.status === 200
    && entry.failure == null
    && Number.isFinite(entry.startedAt)
    && entry.startedAt >= requestAt
    && Number.isFinite(entry.responseReceivedAt)
    && entry.responseReceivedAt > failedAt
    && Number.isFinite(entry.completedAt)
    && entry.completedAt >= entry.responseReceivedAt;
  const nextCapability = (report.nativeRequests ?? []).find(entry =>
    entry.method === 'POST'
    && entry.path === capabilityPath
    && entry.body?.outputId === next.outputId
    && entry.body?.stageId === 'source_projection'
    && entry.body?.snapshotToken === next.draft.snapshotToken
    && entry.body?.expectedDraftVersion === next.draft.version
    && entry.body?.expectedDraftDigest === next.draft.digest
    && entry.triggerAction === action.label
    && later(entry));
  const previewPaths = [
    apiPath(scope, '/authoring/v2/preview'),
    apiPath(scope, '/authoring/v2/construction-proposals'),
  ];
  const preview = (report.nativeRequests ?? []).find(entry =>
    entry.method === 'POST'
    && previewPaths.includes(entry.path)
    && entry.body?.outputId === next.outputId
    && entry.triggerAction === action.label
    && later(entry)
    && (entry.path.endsWith('/preview')
      ? present(entry.body?.receiptId)
      : entry.response?.previewStatus === 'READY' && present(entry.response?.proposalId)));
  const successorProof = proofIsBound(report, expected, failedAt);
  if (!nextCapability || !successorProof || (next.proof.kind === 'visible-rows' && !preview)) {
    return reject('later next-owner capability and visible-row or empty-target proof are not all present');
  }

  return {
    ok: true,
    evidence: {
      actionId: actionRecord.id,
      outgoingOutputId: outgoing.outputId,
      outgoingSelectionId: outgoing.selectionId,
      abortRequestId: record.playwrightRequestId,
      method: record.method,
      browserRequestId,
      nextOutputId: next.outputId,
      nextSelectionId: next.selectionId,
      capabilityRequestId: nextCapability.requestId,
      previewRequestId: preview?.requestId ?? null,
      proofKind: next.proof.kind,
      rawNetworkEntries: 1,
    },
  };
};

export const validateNullableJoinSwitch = (report, expected) => {
  const scope = expected?.scope;
  const actionId = expected?.action?.id;
  const actionLabel = expected?.action?.label;
  const outgoingSelection = expected?.outgoing?.selectionId;
  if (!present(scope?.project) || !present(scope?.generation) || !present(scope?.explorer)
    || !present(actionId)
    || !present(actionLabel) || !present(outgoingSelection)) {
    return reject('switch context must identify its scoped action ID and outgoing selection');
  }
  const capabilityPath = apiPath(scope, '/authoring/v2/construction-capabilities');
  const selectionPath = apiPath(scope, `/selections/${outgoingSelection}`);
  const network = report?.network ?? [];
  const expectedCancellations = report?.expectedCancellations ?? [];
  const scopedCancellations = expectedCancellations.filter(cancellation => {
    const linked = network.filter(record => record.playwrightRequestId === cancellation.playwrightRequestId);
    if (linked.length !== 0) return linked.some(record => record.failureAction?.id === actionId);
    const proof = cancellation.proof;
    return proof?.scopeAction === actionLabel
      && proof.outgoingOutputId === expected.outgoing.outputId
      && proof.outgoingSelectionRevisionId === expected.outgoing.selectionId
      && same(proof.outgoingDraft, expected.outgoing.draft);
  });
  const cancellationIDs = scopedCancellations.map(entry => entry.playwrightRequestId);
  if (cancellationIDs.some(id => !present(id)) || new Set(cancellationIDs).size !== cancellationIDs.length) {
    return reject('expected cancellation ledger contains a missing or duplicate raw request identity');
  }
  const consumedCancellationIds = new Set();
  for (const cancellation of scopedCancellations) {
    const cancellationID = cancellation.playwrightRequestId;
    consumedCancellationIds.add(cancellationID);
    const rawMatches = network.filter(record => record.playwrightRequestId === cancellationID);
    if (rawMatches.length !== 1) {
      return reject('each scoped expected cancellation must correlate to exactly one raw network entry');
    }
    const record = rawMatches[0];
    let cancellationPath;
    let recordPath;
    try {
      cancellationPath = new URL(cancellation.url).pathname;
      recordPath = new URL(record.url).pathname;
    } catch {
      return reject('expected cancellation ledger or raw request URL is invalid');
    }
    const proof = cancellation.proof;
    const sameBrowserRequest = present(cancellation.browserRequestId)
      ? record.browserRequestId === cancellation.browserRequestId
      : !present(record.browserRequestId);
    const requestProof = proof?.scopeRequest;
    const requestOwnerMatches = record.method === 'POST'
      ? (requestProof?.requestId ?? null) === (record.requestId ?? null)
        && requestProof.outputId === expected.outgoing.outputId
        && requestProof?.stageId === 'source_projection'
        && requestProof?.draftVersion === expected.outgoing.draft.version
        && requestProof?.draftDigest === expected.outgoing.draft.digest
      : (requestProof?.requestId ?? null) === (record.requestId ?? null)
        && requestProof?.draftVersion === null
        && requestProof?.draftDigest === null
        && requestProof?.outputId === null
        && requestProof?.stageId === null;
    if (!present(cancellation.reason)
      || cancellation.method !== record.method
      || cancellation.url !== record.url
      || cancellationPath !== recordPath
      || !isNullableJoinCancellationPath(record.method, recordPath, outgoingSelection)
      || cancellation.playwrightRequestId !== record.playwrightRequestId
      || (cancellation.requestId !== record.requestId && cancellation.requestId !== record.playwrightRequestId)
      || !sameBrowserRequest
      || proof?.scopeAction !== actionLabel
      || proof?.project !== scope.project
      || proof?.generation !== scope.generation
      || proof?.explorer !== scope.explorer
      || proof?.outgoingOutputId !== expected.outgoing.outputId
      || proof?.outgoingSelectionRevisionId !== expected.outgoing.selectionId
      || !same(proof?.outgoingDraft, expected.outgoing.draft)
      || !requestOwnerMatches) {
      return reject('expected cancellation ledger does not bind this action, outgoing owner, and captured draft CAS');
    }
    if (present(record.browserRequestId)
      && (record.expected !== true || !same(record.expectedCancellation, cancellation))) {
      return reject('captured capability cancellation is not marked with its exact ledger evidence');
    }
    const proofResult = validateSwitchAbortEvidence(report, expected, record);
    if (!proofResult.ok) return proofResult;
  }

  const candidates = (report?.network ?? []).filter(record => {
    if (record.triggerAction !== actionLabel && record.failureAction?.label !== actionLabel) return false;
    if (record.failureAction?.id !== actionId
      && expectedCancellations.some(cancellation => cancellation.playwrightRequestId === record.playwrightRequestId
        && cancellation.proof?.scopeAction === actionLabel)) return false;
    try {
      const path = new URL(record.url).pathname;
      if (path === selectionPath) return record.method === 'GET';
      return path === capabilityPath
        && record.method === 'POST'
        && record.requestDetails?.outputId === expected.outgoing.outputId;
    } catch {
      return false;
    }
  });
  if (candidates.length === 0) {
    if (scopedCancellations.length !== 0) {
      return reject('zero-abort switch cannot retain a scoped expected cancellation without a consumed abort');
    }
    const actions = (report?.actions ?? []).filter(entry => entry.id === actionId);
    const actionRecord = actions[0];
    if (actions.length !== 1 || actionRecord.status !== 'passed'
      || actionRecord.label !== actionLabel || actionRecord.locator !== expected.action.locator
      || report?.scenario !== 'cda-workspace-combine'
      || report?.caseName !== 'nullable-code-join'
      || report?.scope?.project !== scope.project
      || report.scope.generation !== scope.generation
      || report.target?.kind !== 'owned-cda'
      || report.target?.project !== scope.project
      || report.target?.generation !== scope.generation
      || report.target?.explorer !== scope.explorer
      || !selectionIsBound(report, scope, expected.outgoing)
      || (expected.next.proof.kind === 'visible-rows'
        && !selectionIsBound(report, scope, expected.next))) {
      return reject('zero-abort switch lacks one exact passed scoped action or bound owner selections');
    }
    const path = apiPath(scope, '/authoring/v2/construction-capabilities');
    const successful = entry => entry.method === 'POST'
      && entry.path === path
      && entry.triggerAction === actionLabel
      && entry.body?.outputId === expected.next.outputId
      && entry.body?.stageId === 'source_projection'
      && entry.body?.snapshotToken === expected.next.draft.snapshotToken
      && entry.body?.expectedDraftVersion === expected.next.draft.version
      && entry.body?.expectedDraftDigest === expected.next.draft.digest
      && entry.status === 200
      && entry.failure == null
      && Number.isFinite(entry.startedAt)
      && Number.isFinite(entry.responseReceivedAt)
      && entry.responseReceivedAt >= entry.startedAt
      && Number.isFinite(entry.completedAt)
      && entry.completedAt >= entry.responseReceivedAt;
    const capability = (report.nativeRequests ?? []).find(successful);
    const previewPath = apiPath(scope, '/authoring/v2/preview');
    const preview = (report.nativeRequests ?? []).find(entry =>
      entry.method === 'POST' && entry.path === previewPath
      && entry.triggerAction === actionLabel
      && entry.body?.outputId === expected.next.outputId
      && present(entry.body?.receiptId)
      && entry.status === 200 && entry.failure == null
      && Number.isFinite(entry.startedAt)
      && Number.isFinite(entry.responseReceivedAt)
      && entry.responseReceivedAt >= entry.startedAt
      && Number.isFinite(entry.completedAt)
      && entry.completedAt >= entry.responseReceivedAt);
    if (!capability
      || !proofIsBound(report, expected, -Infinity)
      || (expected.next.proof.kind === 'visible-rows' && !preview)) {
      return reject('zero-abort switch lacks a successful next-owner response and action-bound visible-row or target proof');
    }
    return {
      ok: true,
      evidence: {
        abortCount: 0,
        classifiedCancellationIds: [],
        actionId,
        outgoingOutputId: expected.outgoing.outputId,
        outgoingSelectionId: expected.outgoing.selectionId,
        nextOutputId: expected.next.outputId,
        nextSelectionId: expected.next.selectionId,
        capabilityRequestId: capability.requestId,
        previewRequestId: preview?.requestId ?? null,
        proofKind: expected.next.proof.kind,
      },
    };
  }
  if (candidates.length > 2 || new Set(candidates.map(record => record.method)).size !== candidates.length) {
    return reject('report contains duplicate outgoing capability or selection entries for this switch');
  }
  const results = candidates.map(record => validateSwitchAbortEvidence(report, expected, record));
  const invalid = results.find(result => !result.ok);
  if (invalid) return invalid;
  return {
    ok: true,
    evidence: {
      abortCount: results.length,
      classifiedCancellationIds: scopedCancellations.map(entry => entry.playwrightRequestId),
      aborts: results.map(result => result.evidence),
    },
  };
};
