const maxSourceOptionsDiagnosticBytes = 512 * 1024;
import { isDeepStrictEqual } from 'node:util';

const maxSourceOptionsDiagnosticEntries = 50;
const nonempty = value => typeof value === 'string' && value.trim().length > 0;
const normalizedSourceLabel = value => String(value ?? '').replace(/\s+/g, ' ').trim();

export const codedPivotProposalRequestMatches = (request, {
  outputId, snapshotToken, sourceChoiceId, missingCellPolicy, categories, stepId,
}) => {
  if (![outputId, snapshotToken, sourceChoiceId, missingCellPolicy].every(nonempty) || !Array.isArray(categories) || categories.length === 0) return false;
  const candidate = request?.candidateConstruction;
  if (request?.outputId !== outputId || request?.snapshotToken !== snapshotToken ||
      !candidate || candidate.version !== 1 || !Array.isArray(candidate.steps) || candidate.steps.length !== 1) return false;
  const step = candidate.steps[0];
  const codedPivot = step?.operation?.kind === 'CODED_PIVOT' ? step.operation.codedPivot : undefined;
  if (!codedPivot || !nonempty(step.id) || request.changedStepId !== step.id ||
      (stepId !== undefined && step.id !== stepId) || codedPivot.constructionId !== step.id ||
      !Array.isArray(step.inputs) || !isDeepStrictEqual(step.inputs, [{ kind: 'SOURCE_PROJECTION' }]) ||
      codedPivot.sourceChoiceId !== sourceChoiceId || codedPivot.missingCellPolicy !== missingCellPolicy ||
      codedPivot.duplicatePolicy !== 'ERROR') return false;

  const actualCategories = codedPivot.categories;
  const outputs = step.outputs;
  if (!Array.isArray(actualCategories) || actualCategories.length !== categories.length ||
      !Array.isArray(outputs) || outputs.length !== categories.length) return false;
  const choiceForm = actualCategories.every(category => nonempty(category?.choiceId));
  const canonicalForm = actualCategories.every(category => !category?.choiceId && nonempty(category?.system) && nonempty(category?.code));
  if (!choiceForm && !canonicalForm) return false;

  const matched = new Set();
  const outputIds = new Set();
  for (const category of actualCategories) {
    const matches = categories.filter(expected => choiceForm
      ? expected.choiceId === category.choiceId
      : expected.system === category.system && expected.code === category.code);
    if (matches.length !== 1) return false;
    const expected = matches[0];
    if (matched.has(expected) || (category.system !== undefined && category.system !== expected.system) ||
        (category.code !== undefined && category.code !== expected.code) ||
        !nonempty(category.outputColumnId) || outputIds.has(category.outputColumnId) ||
        (expected.outputColumnId !== undefined && category.outputColumnId !== expected.outputColumnId)) return false;
    const outputMatches = outputs.filter(output => output?.id === category.outputColumnId);
    if (outputMatches.length !== 1 || outputMatches[0].name !== expected.code || outputMatches[0].label !== expected.label) return false;
    matched.add(expected);
    outputIds.add(category.outputColumnId);
  }
  return matched.size === categories.length && outputIds.size === outputs.length;
};

export const codedPivotPolicyReplacementCancellationEvidenceFor = ({
  requests,
  expectedCancellations,
  replacementRequest,
  policyAction,
  expected,
}) => {
  const invalid = reason => ({ status: 'invalid', reason });
  if (!Array.isArray(requests) || !Array.isArray(expectedCancellations) ||
      !replacementRequest || !policyAction || !expected) return invalid('Coded Pivot policy-replacement evidence is incomplete.');
  const required = ['origin', 'path', 'project', 'generation', 'explorerId', 'mode', 'outputId', 'snapshotToken',
    'draftDigest', 'stepId', 'sourceChoiceId', 'fromPolicy', 'toPolicy', 'reason', 'actionLabel'];
  if (required.some(key => !nonempty(expected[key])) || !['integer', 'string'].includes(expected.mode) ||
      !Array.isArray(expected.categories) || expected.categories.length === 0 ||
      !Number.isInteger(expected.draftVersion) || expected.draftVersion < 0 ||
      expected.fromPolicy !== 'NULL' || expected.toPolicy !== 'ERROR' || policyAction.label !== expected.actionLabel ||
      !Number.isFinite(policyAction.startedAt) || !Number.isFinite(policyAction.completedAt) ||
      policyAction.completedAt < policyAction.startedAt) {
    return invalid('Coded Pivot policy replacement must bind the exact NULL-to-ERROR action and request identity.');
  }

  const proposalRequests = requests.filter(entry => entry?.path === expected.path && entry.method === 'POST' &&
    typeof entry.requestId === 'string' && entry.requestId.startsWith('construction-proposal-'));
  const failedRequests = proposalRequests.filter(entry => typeof entry.failure === 'string' && entry.failure.length > 0);
  const unexpectedFailures = failedRequests.filter(entry => entry.failure !== 'net::ERR_ABORTED');
  if (unexpectedFailures.length) return invalid('The policy-change window contains a non-abort proposal failure.');

  const replacementMatches = proposalRequests.filter(entry => codedPivotProposalRequestMatches(entry.body, {
    outputId: expected.outputId,
    snapshotToken: expected.snapshotToken,
    sourceChoiceId: expected.sourceChoiceId,
    missingCellPolicy: expected.toPolicy,
    categories: expected.categories,
    stepId: expected.stepId,
  }));
  if (replacementMatches.length !== 1 || replacementMatches[0] !== replacementRequest) {
    return invalid('The policy-change window must contain exactly one captured matching ERROR proposal.');
  }
  const replacement = replacementMatches[0];
  if (replacement.origin !== expected.origin || replacement.body?.expectedDraftVersion !== expected.draftVersion ||
      replacement.body?.expectedDraftDigest !== expected.draftDigest || replacement.status !== 200 || !Number.isFinite(replacement.startedAt) ||
      !Number.isFinite(replacement.completedAt) || replacement.completedAt < replacement.startedAt ||
      replacement.response?.outputId !== expected.outputId || replacement.response?.snapshotToken !== expected.snapshotToken ||
      typeof replacement.response?.proposalId !== 'string' || !replacement.response.proposalId ||
      replacement.response?.previewStatus !== 'READY') {
    return invalid('The exact ERROR replacement proposal did not reach its successful native response.');
  }

  const nullCandidates = proposalRequests.filter(entry => codedPivotProposalRequestMatches(entry.body, {
    outputId: expected.outputId,
    snapshotToken: expected.snapshotToken,
    sourceChoiceId: expected.sourceChoiceId,
    missingCellPolicy: expected.fromPolicy,
    categories: expected.categories,
    stepId: expected.stepId,
  }));
  const abortedRequests = failedRequests.filter(entry => entry.failure === 'net::ERR_ABORTED');
  const cancellationRecords = expectedCancellations.filter(entry =>
    entry?.proof?.contract === 'coded-pivot-policy-replacement' && entry.proof.actionLabel === expected.actionLabel);
  if (abortedRequests.length === 0) {
    if (cancellationRecords.length !== 0) return invalid('A cancellation record exists without an observed NULL candidate abort.');
    return {
      status: 'no-cancellation',
      replacementRequestId: replacement.requestId,
      actionLabel: policyAction.label,
      actionStartedAt: policyAction.startedAt,
      actionCompletedAt: policyAction.completedAt,
    };
  }
  if (abortedRequests.length !== 1 || nullCandidates.length !== 1 || abortedRequests[0] !== nullCandidates[0]) {
    return invalid('Only one exact NULL candidate may be aborted during the policy replacement.');
  }

  const canceled = abortedRequests[0];
  if (canceled.origin !== expected.origin || canceled.body?.expectedDraftVersion !== expected.draftVersion ||
      canceled.body?.expectedDraftDigest !== expected.draftDigest || !Number.isFinite(canceled.startedAt) || !Number.isFinite(canceled.completedAt) ||
      canceled.completedAt < policyAction.startedAt || canceled.completedAt >= replacement.startedAt) {
    return invalid('The NULL candidate abort did not occur after the policy action and before its ERROR replacement started.');
  }
  if (replacement.startedAt < policyAction.startedAt || policyAction.completedAt > replacement.completedAt) {
    return invalid('The ERROR replacement is outside the recorded policy-change action window.');
  }
  if (cancellationRecords.length !== 1) return invalid('The exact NULL candidate abort needs one matching fixture cancellation record.');
  const cancellation = cancellationRecords[0];
  const expectedProof = {
    contract: 'coded-pivot-policy-replacement',
    actionLabel: expected.actionLabel,
    mode: expected.mode,
    project: expected.project,
    generation: expected.generation,
    explorerId: expected.explorerId,
    outputId: expected.outputId,
    snapshotToken: expected.snapshotToken,
    draftVersion: expected.draftVersion,
    draftDigest: expected.draftDigest,
    stepId: expected.stepId,
    sourceChoiceId: expected.sourceChoiceId,
    categories: expected.categories,
    fromPolicy: expected.fromPolicy,
    toPolicy: expected.toPolicy,
  };
  const proof = cancellation.proof;
  const { scopeAction, scopeRequest, ...actualProof } = proof ?? {};
  if (cancellation.requestId !== canceled.requestId || cancellation.method !== 'POST' ||
      cancellation.url !== `${expected.origin}${expected.path}` || cancellation.reason !== expected.reason ||
      scopeAction !== expected.actionLabel || !isDeepStrictEqual(actualProof, expectedProof) ||
      scopeRequest?.requestId !== canceled.requestId || scopeRequest?.draftVersion !== expected.draftVersion ||
      scopeRequest?.draftDigest !== expected.draftDigest || scopeRequest?.outputId !== expected.outputId) {
    return invalid('The fixture cancellation record does not prove this exact coded Pivot NULL-to-ERROR replacement.');
  }

  return {
    status: 'matched-cancellation',
    canceledRequestId: canceled.requestId,
    replacementRequestId: replacement.requestId,
    reason: cancellation.reason,
    cancellation,
    actionLabel: policyAction.label,
    actionStartedAt: policyAction.startedAt,
    actionCompletedAt: policyAction.completedAt,
    canceledAt: canceled.completedAt,
    replacementStartedAt: replacement.startedAt,
    replacementCompletedAt: replacement.completedAt,
  };
};

const codedPivotEditorRetiredBodyReadError = 'response.text: Protocol error (Network.getResponseBody): No data found for resource with given identifier\nResponse body is not available for a response that was navigated away from. Read response.body() before triggering any navigation.';

export const codedPivotEditorDisposalCancellationEvidenceFor = ({
  requests,
  expectedCancellations,
  fixtureNetworkFailures,
  request,
  action,
  expected,
  finalized = false,
}) => {
  const invalid = reason => ({ status: 'invalid', reason });
  if (!Array.isArray(requests) || !Array.isArray(expectedCancellations) ||
      !Array.isArray(fixtureNetworkFailures) || !action || !expected) {
    return invalid('Coded Pivot editor-disposal evidence is incomplete.');
  }
  const required = ['origin', 'path', 'project', 'generation', 'explorerId', 'selectionId', 'mode',
    'outputId', 'snapshotToken', 'sourceChoiceId', 'actionLabel', 'reason'];
  if (required.some(key => !nonempty(expected[key])) || expected.actionLabel !== 'Back to table' ||
      !['integer', 'string'].includes(expected.mode) || expected.rowRoot !== 'Observation' || expected.limit !== 50 ||
      !Number.isFinite(action.startedAt) || !Number.isFinite(action.completedAt) ||
      action.completedAt < action.startedAt || action.label !== expected.actionLabel) {
    return invalid('Coded Pivot editor disposal must bind the exact Back to table action, project, Explorer, output, source, and row scope.');
  }

  const explorerPath = `/api/v1/projects/${encodeURIComponent(expected.project)}/explorers/${encodeURIComponent(expected.explorerId)}/authoring/v2/semantic-inventory`;
  if (expected.path !== explorerPath || new URL(expected.origin).origin !== expected.origin) {
    return invalid('Coded Pivot editor disposal must use the exact owned semantic-inventory endpoint.');
  }
  const scopedRequests = requests.filter(entry => entry?.path === expected.path && entry?.method === 'POST');
  const abortedRequests = scopedRequests.filter(entry => entry?.failure === 'net::ERR_ABORTED');
  if (scopedRequests.some(entry => typeof entry?.failure === 'string' && entry.failure.length > 0 &&
      entry.failure !== 'net::ERR_ABORTED')) {
    return invalid('A non-abort semantic-inventory failure remains fatal during editor disposal.');
  }
  const cancellationRecords = expectedCancellations.filter(entry => entry?.proof?.contract === 'coded-pivot-editor-disposal');
  if (abortedRequests.length === 0) {
    if (cancellationRecords.length !== 0) return invalid('An editor-disposal cancellation record exists without an observed native abort.');
    return { status: 'no-cancellation' };
  }
  if (abortedRequests.length !== 1 || abortedRequests[0] !== request || scopedRequests.filter(entry => entry === request).length !== 1) {
    return invalid('Only one captured semantic-inventory request may be retired by this editor action.');
  }

  const entry = request;
  const rawBody = entry.body;
  const expectedBody = {
    snapshotToken: expected.snapshotToken,
    rowRoot: expected.rowRoot,
    sourceChoiceId: expected.sourceChoiceId,
    outputId: expected.outputId,
    limit: expected.limit,
  };
  let ownerURL;
  let rawURL;
  try {
    ownerURL = new URL(entry.ownerPageUrlAtRequest);
    rawURL = new URL(entry.rawURL);
  } catch {
    return invalid('The retired semantic-inventory request must retain its owning Builder page and request URL.');
  }
  const selections = ownerURL.searchParams.getAll('selection');
  if (entry.origin !== expected.origin || entry.requestId !== entry.browserRequestId ||
      !nonempty(entry.browserRequestId) || !nonempty(entry.ownerPageId) || entry.status !== 200 ||
      entry.failure !== 'net::ERR_ABORTED' || !Number.isFinite(entry.startedAt) ||
      !Number.isFinite(entry.responseReceivedAt) || !Number.isFinite(entry.completedAt) ||
      entry.completedAt < entry.responseReceivedAt || !isDeepStrictEqual(rawBody, expectedBody) ||
      rawURL.origin !== expected.origin || rawURL.pathname !== expected.path || rawURL.search || rawURL.hash ||
      ownerURL.origin !== expected.origin || ownerURL.pathname !== '/' || ownerURL.searchParams.get('project') !== expected.project ||
      ownerURL.searchParams.get('explorer') !== expected.explorerId || ownerURL.searchParams.get('mode') !== 'builder' ||
      selections.length !== 1 || selections[0] !== expected.selectionId ||
      entry.query === undefined || Object.keys(entry.query).length !== 0) {
    return invalid('The retired request did not match the exact owned Builder route and semantic-inventory payload.');
  }

  const chronology = entry.nativeEventChronology;
  if (!Array.isArray(chronology) || chronology.length !== 3 ||
      !isDeepStrictEqual(chronology.map(event => event.event), ['request', 'response', 'requestfailed']) ||
      chronology.some(event => event.browserRequestId !== entry.browserRequestId || event.objectMatch !== true || !Number.isFinite(event.observedAt)) ||
      chronology.some((event, index) => index > 0 && event.observedAt < chronology[index - 1].observedAt) ||
      chronology[2].observedAt < action.startedAt || chronology[2].observedAt > action.completedAt ||
      chronology[0].observedAt < entry.startedAt || chronology[0].observedAt - entry.startedAt > 1_000 ||
      chronology[1].observedAt > entry.responseReceivedAt || entry.responseReceivedAt - chronology[1].observedAt > 1_000 ||
      chronology[2].observedAt > entry.completedAt || entry.completedAt - chronology[2].observedAt > 1_000) {
    return invalid('The native response and abort must belong to this exact Back to table action window.');
  }

  const fixtureFailures = fixtureNetworkFailures.filter(failure => failure?.browserRequestId === entry.browserRequestId);
  if (fixtureFailures.length !== 1) return invalid('The editor-disposal proof must match one cloned fixture network failure.');
  const fixtureFailure = fixtureFailures[0];
  if (fixtureFailure.errorText !== 'net::ERR_ABORTED' || fixtureFailure.expected !== true ||
      fixtureFailure.cancellationAction !== expected.actionLabel ||
      (fixtureFailure.triggerAction != null && fixtureFailure.triggerAction !== expected.actionLabel) ||
      (fixtureFailure.failureAction?.label != null && fixtureFailure.failureAction.label !== expected.actionLabel) ||
      !isDeepStrictEqual(fixtureFailure.requestScope, {
        expectedProject: expected.project,
        generation: expected.generation,
        configuredExplorer: expected.explorerId,
        requestProject: expected.project,
        requestExplorer: expected.explorerId,
      })) {
    return invalid('The fixture failure was not observed under the exact owned Explorer and editor-disposal action.');
  }

  if (cancellationRecords.length !== 1) return invalid('The exact editor-disposal abort needs one fixture cancellation record.');
  const cancellation = cancellationRecords[0];
  const expectedProof = {
    contract: 'coded-pivot-editor-disposal',
    actionLabel: expected.actionLabel,
    mode: expected.mode,
    project: expected.project,
    generation: expected.generation,
    explorerId: expected.explorerId,
    selectionId: expected.selectionId,
    outputId: expected.outputId,
    snapshotToken: expected.snapshotToken,
    sourceChoiceId: expected.sourceChoiceId,
    rowRoot: expected.rowRoot,
    limit: expected.limit,
  };
  const { scopeAction, scopeRequest, ...actualProof } = cancellation.proof ?? {};
  if (cancellation.browserRequestId !== entry.browserRequestId ||
      cancellation.requestId !== fixtureFailure.playwrightRequestId ||
      cancellation.playwrightRequestId !== fixtureFailure.playwrightRequestId ||
      cancellation.method !== 'POST' || cancellation.url !== `${expected.origin}${expected.path}` ||
      cancellation.reason !== expected.reason || scopeAction !== expected.actionLabel ||
      !isDeepStrictEqual(actualProof, expectedProof) ||
      !isDeepStrictEqual(scopeRequest, {
        requestId: null,
        draftVersion: null,
        draftDigest: null,
        outputId: expected.outputId,
        stageId: null,
      }) || !isDeepStrictEqual(fixtureFailure.expectedCancellation, cancellation)) {
    return invalid('The cloned fixture cancellation does not prove this exact semantic-inventory request and action.');
  }

  if (entry.responseReadError !== codedPivotEditorRetiredBodyReadError || entry.response !== undefined) {
    return invalid('Only the exact browser navigation-away response-body diagnostic can be consumed as editor disposal.');
  }

  return {
    status: finalized ? 'matched-cancellation' : 'verified-retirement-candidate',
    requestId: entry.requestId,
    browserRequestId: entry.browserRequestId,
    reason: cancellation.reason,
    cancellation,
    actionStartedAt: action.startedAt,
    actionCompletedAt: action.completedAt,
    responseReceivedAt: entry.responseReceivedAt,
    failedAt: chronology[2].observedAt,
    responseReadError: entry.responseReadError,
  };
};

export const codedPivotRemovalRequestMatches = (request, {
  outputId, snapshotToken, removedStepId, candidateConstruction,
}) => Boolean(
  nonempty(outputId) && nonempty(snapshotToken) && nonempty(removedStepId) &&
  request?.outputId === outputId && request?.snapshotToken === snapshotToken &&
  !Object.hasOwn(request, 'changedStepId') &&
  isDeepStrictEqual(request?.removeStepIds, [removedStepId]) &&
  isDeepStrictEqual(request?.candidateConstruction, candidateConstruction) &&
  Array.isArray(candidateConstruction?.steps) &&
  candidateConstruction.steps.every(step => step?.operation?.kind !== 'CODED_PIVOT')
);

export const codedPivotPersistedSourceBindingsFor = step => {
  const source = step?.operation?.codedPivot?.source;
  return source && typeof source === 'object' && source.family && typeof source.family === 'object' ? source : null;
};

export const codedPivotPersistedSourceBindingsEqual = (leftStep, rightStep) => {
  const left = codedPivotPersistedSourceBindingsFor(leftStep);
  const right = codedPivotPersistedSourceBindingsFor(rightStep);
  return Boolean(left && right && isDeepStrictEqual(left, right));
};

export const codedPivotPersistedSourceMatchesOption = (step, option) => {
  const source = codedPivotPersistedSourceBindingsFor(step);
  const family = source?.family;
  if (!source || !family || !option || !Array.isArray(option.route)) return false;
  const selectedFields = ['bindingId', 'resourceType', 'sourcePath', 'sourceCanonical', 'owningScope', 'keyPath', 'valuePath', 'logicalType'];
  if (selectedFields.some(field => !nonempty(option[field]) || family[field] !== option[field])) return false;
  return nonempty(source.candidateId) && nonempty(source.nodeId) &&
    source.fieldPath === `${option.sourcePath}.${option.valuePath}` &&
    Array.isArray(family.choiceArms) && family.choiceArms.length === 1 && family.choiceArms[0] === option.valuePath &&
    JSON.stringify(source.route) === JSON.stringify(option.route);
};

export const codedPivotSourceOptionsDiagnosticFor = (entry, request, response, {
  origin, project, generation, explorerId, outputId, snapshotToken, mode, domSnapshot,
}) => {
  if (![origin, project, generation, explorerId, outputId, snapshotToken].every(nonempty) || !['integer', 'string'].includes(mode)) {
    throw new TypeError('Coded Pivot source-options evidence needs the exact project, generation, Explorer, output, snapshot, and mode.');
  }
  const endpointPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/frame-source-options`;
  if (entry?.origin !== new URL(origin).origin || entry?.path !== endpointPath || entry?.method !== 'POST' ||
      !Number.isInteger(entry?.status) || entry.status < 200 || entry.status >= 300 || !Number.isFinite(entry?.completedAt)) {
    throw new Error('Coded Pivot source-options evidence must be a completed successful POST from the exact owned endpoint.');
  }
  const expectedRequestKeys = ['limit', 'outputId', 'resourceType', 'snapshotToken'];
  if (!request || typeof request !== 'object' || Array.isArray(request) ||
      JSON.stringify(Object.keys(request).sort()) !== JSON.stringify(expectedRequestKeys) ||
      request.resourceType !== 'Observation' || request.snapshotToken !== snapshotToken || request.outputId !== outputId ||
      request.limit !== 50 || JSON.stringify(entry.body) !== JSON.stringify(request)) {
    throw new Error('Coded Pivot source-options request must bind the exact owned output and catalog snapshot.');
  }
  if (response?.snapshotToken !== snapshotToken || response?.outputId !== outputId ||
      typeof response.complete !== 'boolean' || typeof response.truncated !== 'boolean' || !Array.isArray(response.sources)) {
    throw new Error('Coded Pivot source-options response must echo the exact output and snapshot with its page envelope.');
  }
  if (domSnapshot && (domSnapshot.mode !== mode || !Array.isArray(domSnapshot.sourceControls))) {
    throw new Error('Coded Pivot failure DOM must retain its mode and rendered source controls.');
  }

  const expectedValuePath = mode === 'integer' ? /valueInteger$/i : /valueString$/i;
  const sourceControls = domSnapshot?.sourceControls ?? [];
  const modeLabels = sourceControls.filter(control => control.name === 'coded-pivot-source' &&
    normalizedSourceLabel(control.labelText).toLowerCase().includes('component') &&
    normalizedSourceLabel(control.labelText).toLowerCase().includes(mode));
  const sourceOptions = response.sources;
  const candidateIndexes = sourceOptions.flatMap((option, index) =>
    Array.isArray(option.route) && option.route.length === 0 && option.resourceType === 'Observation' &&
      expectedValuePath.test(option.valuePath ?? '') &&
      normalizedSourceLabel(`${option.title} ${option.description}`).toLowerCase().includes('component') &&
      normalizedSourceLabel(`${option.title} ${option.description}`).toLowerCase().includes(mode) ? [index] : []);
  const matchedIndexes = candidateIndexes.filter(index => modeLabels.some(control =>
    normalizedSourceLabel(control.labelText) === normalizedSourceLabel(`${sourceOptions[index].title} ${sourceOptions[index].description}`)));
  const priorityCandidateIndexes = [...new Set([...matchedIndexes, ...candidateIndexes])].slice(0, maxSourceOptionsDiagnosticEntries);
  const candidateChoices = priorityCandidateIndexes.map(index => sourceOptions[index]);
  const sourceBytes = Buffer.byteLength(JSON.stringify(response), 'utf8');
  const diagnostic = {
    target: {
      project, generation, explorerId, outputId, snapshotToken, mode,
      identitySource: 'created Explorer scope and BuilderV2 catalog generation/snapshot',
      outputSource: 'saved table document output id before coded Pivot chooser',
    },
    endpoint: { origin: new URL(origin).origin, path: endpointPath, method: entry.method, status: entry.status,
      browserRequestId: entry.browserRequestId },
    request: { resourceType: request.resourceType, outputId: request.outputId, snapshotToken: request.snapshotToken, limit: request.limit },
    envelope: {
      snapshotToken: response.snapshotToken,
      outputId: response.outputId,
      complete: response.complete,
      truncated: response.truncated,
      nextCursor: response.nextCursor ?? null,
      requestedLimit: request.limit,
      sourceCount: sourceOptions.length,
      sourceCountExceedsLimit: sourceOptions.length > request.limit,
      sourceCountExceedsDiagnosticEntryCap: sourceOptions.length > maxSourceOptionsDiagnosticEntries,
      responseBytes: sourceBytes,
    },
    domSnapshotCaptured: Boolean(domSnapshot),
    frameOwnership: 'direct Observation frame: empty route, Observation resourceType, mode-specific FHIR valuePath',
    domSourceControlCount: domSnapshot?.sourceControlCount ?? sourceControls.length,
    domSourceControlsTruncated: domSnapshot?.sourceControlsTruncated ?? false,
    domSourceLabels: modeLabels.map(control => normalizedSourceLabel(control.labelText)),
    candidateChoiceCount: candidateIndexes.length,
    matchedChoiceCount: matchedIndexes.length,
    matchedChoiceIndexes: matchedIndexes.flatMap(index => priorityCandidateIndexes.indexOf(index) >= 0 ? [priorityCandidateIndexes.indexOf(index)] : []),
    candidateChoices,
    sourcePreview: [],
    diagnosticTruncated: sourceOptions.length > priorityCandidateIndexes.length,
    diagnosticEntryCap: maxSourceOptionsDiagnosticEntries,
    diagnosticByteCap: maxSourceOptionsDiagnosticBytes,
  };

  const candidateSet = new Set(priorityCandidateIndexes);
  const previewCandidates = sourceOptions.flatMap((option, index) => candidateSet.has(index) ? [] : [{ index, option }]);
  const savedIndexes = new Set(priorityCandidateIndexes);
  for (const { index, option } of previewCandidates) {
    if (savedIndexes.size >= maxSourceOptionsDiagnosticEntries) break;
    diagnostic.sourcePreview.push(option);
    savedIndexes.add(index);
    if (Buffer.byteLength(JSON.stringify(diagnostic), 'utf8') > maxSourceOptionsDiagnosticBytes) {
      diagnostic.sourcePreview.pop();
      savedIndexes.delete(index);
      diagnostic.diagnosticTruncated = true;
      break;
    }
  }
  if (savedIndexes.size < sourceOptions.length) diagnostic.diagnosticTruncated = true;
  diagnostic.diagnosticBytes = 0;
  for (;;) {
    const serializedBytes = Buffer.byteLength(JSON.stringify(diagnostic), 'utf8');
    if (serializedBytes === diagnostic.diagnosticBytes) break;
    diagnostic.diagnosticBytes = serializedBytes;
  }
  const diagnosticBytes = diagnostic.diagnosticBytes;
  if (diagnosticBytes > maxSourceOptionsDiagnosticBytes) {
    throw new RangeError(`Coded Pivot matched source-options evidence alone exceeds ${maxSourceOptionsDiagnosticBytes} bytes.`);
  }
  return diagnostic;
};

export async function codedPivotFirstFailureEvidenceFor({ mode, action, captureDom, captureSourceOptions }) {
  const evidence = { capturedAt: new Date().toISOString(), action };
  if (captureDom) {
    try {
      evidence.dom = await captureDom();
    } catch (error) {
      evidence.domCaptureError = String(error?.stack ?? error);
    }
  } else {
    evidence.domCaptureStatus = 'unavailable';
  }
  if (captureSourceOptions) {
    try {
      evidence.sourceOptions = await captureSourceOptions(evidence.dom);
    } catch (error) {
      evidence.sourceOptionsCaptureError = String(error?.stack ?? error);
    }
  } else {
    evidence.sourceOptionsCaptureStatus = 'unavailable';
  }
  if (evidence.dom && evidence.dom.mode !== mode) evidence.domModeMismatch = { expected: mode, actual: evidence.dom.mode };
  return evidence;
}

export const summarizeCodedPivotNativeRequests = (requests, {
  acceptedExpectedCancellationRequestIds = [],
  acceptedEditorDisposalRequestIds = [],
} = {}) => {
  if (!Array.isArray(requests)) throw new TypeError('Coded Pivot request evidence must be an array.');
  if (!Array.isArray(acceptedExpectedCancellationRequestIds) ||
      !Array.isArray(acceptedEditorDisposalRequestIds) ||
      [...acceptedExpectedCancellationRequestIds, ...acceptedEditorDisposalRequestIds]
        .some(id => typeof id !== 'string' || !id) ||
      new Set([...acceptedExpectedCancellationRequestIds, ...acceptedEditorDisposalRequestIds]).size !==
        acceptedExpectedCancellationRequestIds.length + acceptedEditorDisposalRequestIds.length) {
    throw new TypeError('Accepted coded Pivot cancellation IDs must be unique native request IDs.');
  }
  const acceptedCancellationIds = new Set(acceptedExpectedCancellationRequestIds);
  for (const id of acceptedCancellationIds) {
    if (requests.filter(request => request.requestId === id).length !== 1) {
      throw new Error(`Accepted coded Pivot cancellation request ${id} must identify one captured request.`);
    }
    const request = requests.find(candidate => candidate.requestId === id);
    if (request.failure !== 'net::ERR_ABORTED' || request.expectedCancellation?.contract !== 'coded-pivot-policy-replacement' ||
        request.expectedCancellation?.requestId !== id) {
      throw new Error(`Accepted coded Pivot cancellation request ${id} lacks the validated policy-replacement marker.`);
    }
  }
  const acceptedEditorDisposalIds = new Set(acceptedEditorDisposalRequestIds);
  for (const id of acceptedEditorDisposalIds) {
    if (requests.filter(request => request.requestId === id).length !== 1) {
      throw new Error(`Accepted coded Pivot editor-disposal request ${id} must identify one captured request.`);
    }
    const request = requests.find(candidate => candidate.requestId === id);
    const cancellation = request.expectedCancellation;
    if (request.failure !== 'net::ERR_ABORTED' || cancellation?.contract !== 'coded-pivot-editor-disposal' ||
        request.expected !== true || request.canceled !== true || request.status !== 200 ||
        request.responseReadError !== codedPivotEditorRetiredBodyReadError || request.response !== undefined ||
        cancellation?.requestId !== id || cancellation?.browserRequestId !== request.browserRequestId ||
        cancellation?.method !== 'POST' || cancellation?.url !== `${request.origin}${request.path}` ||
        !nonempty(cancellation?.reason) ||
        cancellation?.fixtureCancellation?.browserRequestId !== request.browserRequestId ||
        cancellation?.fixtureCancellation?.reason !== cancellation.reason ||
        cancellation?.fixtureCancellation?.requestId !== cancellation?.fixtureCancellation?.playwrightRequestId ||
        cancellation?.fixtureCancellation?.proof?.contract !== 'coded-pivot-editor-disposal' ||
        cancellation?.proof?.contract !== 'coded-pivot-editor-disposal' ||
        cancellation?.proof?.actionLabel !== 'Back to table' ||
        cancellation?.proof?.scopeAction !== 'Back to table') {
      throw new Error(`Accepted coded Pivot editor-disposal request ${id} lacks the validated exact editor-disposal marker.`);
    }
  }
  const details = requests.map(request => {
    const explicitFailure = typeof request.failure === 'string' && request.failure.length > 0
      ? request.failure
      : typeof request.responseReadError === 'string' && request.responseReadError.length > 0
        ? request.responseReadError
        : undefined;
    const terminal = Number.isFinite(request.completedAt) || Boolean(explicitFailure);
    return {
      browserRequestId: request.browserRequestId,
      requestId: request.requestId,
      path: request.path,
      method: request.method,
      status: request.status,
      terminal,
      ...(explicitFailure ? { failure: explicitFailure } : {}),
      ...(acceptedCancellationIds.has(request.requestId) ? { expectedPolicyReplacementCancellation: true } : {}),
      ...(acceptedEditorDisposalIds.has(request.requestId) ? { expectedEditorDisposalCancellation: true } : {}),
      ...(request.expectedCancellation ? { expectedCancellation: request.expectedCancellation } : {}),
    };
  });
  const pending = details.filter(request => !request.terminal);
  const terminalFailures = details.filter(request => request.failure && !request.expectedPolicyReplacementCancellation &&
    !request.expectedEditorDisposalCancellation);
  const invalidStatuses = details.filter(request => !request.expectedPolicyReplacementCancellation &&
    !request.expectedEditorDisposalCancellation &&
    (!Number.isFinite(request.status) || request.status >= 400));
  return {
    total: details.length,
    pending,
    terminalFailures,
    invalidStatuses,
    expectedPolicyReplacementCancellations: details.filter(request => request.expectedPolicyReplacementCancellation),
    expectedEditorDisposalCancellations: details.filter(request => request.expectedEditorDisposalCancellation),
    passed: details.length > 0 && pending.length === 0 && terminalFailures.length === 0 && invalidStatuses.length === 0,
  };
};
