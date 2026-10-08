import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { assertExactMultiset } from '../helpers/group-related-multiset.mjs';
import { classifyNativeBrowserApiRequest, isSameUiProxyResponse, nativeRequestsHaveOwnedTransportOutcomes } from '../helpers/native-browser-api-scope.mjs';
import { assertReopenedProposalAfterCancel } from '../helpers/proposal-reopen-binding.mjs';
import { selectSavedPreviewRequest } from '../helpers/saved-preview-binding.mjs';
import { relatedSourceProposalCandidate } from '../helpers/related-source-capture.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';
import { assertCdaNoAuthRuntime } from '../helpers/cda-no-auth-runtime.mjs';
import { classifyExpectedOwnedCancellation, nativeReadRequestMatchesExpectedScope } from '../helpers/native-request-ownership.mjs';
import { createNativeAbortProbeSource, nativeAbortProbeEvidenceForRequest } from '../helpers/native-abort-probe.mjs';
import { createdExplorerScope } from '../helpers/created-explorer-scope.mjs';
import { relatedChoiceStageContext } from '../helpers/related-choice-stage-context.mjs';

export const groupEditRawFieldsDisclosureSelector = '[data-testid="feature-catalog-raw-fields"]';
export const groupEditRawFieldsSummarySelector = `${groupEditRawFieldsDisclosureSelector} > summary`;

export async function runGroupEditBeforeRelatedColumnBrowserWorkflow({ page, cda }) {
const project = cda.project;
assert(project, 'CDA fixture must provide the isolated project');
const env = cda.env ?? {};
const target = cda.target ?? {};
let explorer = cda.explorer;
const requestedExplorerName = explorer;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const arangoContainer = target.arangoContainer ?? env.LOOM_ARANGO_CONTAINER;
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
assert.notEqual(explorer, protectedExplorer);


let explorerRoot;
let base;
const root = `/api/v1/projects/${project}/explorers`;
const click = (_page, ...args) => cda.click(...args);
const selectOption = (_page, ...args) => cda.selectOption(...args);
const fill = (_page, ...args) => cda.fill(...args);
const navigate = (_page, ...args) => cda.navigate(...args);
const browserEval = (_page, callback, args) => cda.inspect(callback, args);
const waitForBrowser = (_page, predicate, timeoutOrArgs = 5000, args) => {
  const timeout = Array.isArray(timeoutOrArgs) ? 5000 : timeoutOrArgs;
  const waitArgs = Array.isArray(timeoutOrArgs) ? timeoutOrArgs : args;
  return cda.wait(predicate, waitArgs, timeout);
};
const waitForControl = (_page, selector, { timeout = 5000, enabled = false, hidden = false } = {}) => cda.wait(
  ({ selector: targetSelector, enabled: requireEnabled, hidden: waitHidden }) => {
    const controls = [...document.querySelectorAll(targetSelector)];
    if (controls.length > 1) throw new Error(`Expected one control for ${targetSelector}, found ${controls.length}`);
    const control = controls[0];
    if (waitHidden) return !control || !control.getClientRects().length;
    return Boolean(control && control.getClientRects().length &&
      (!requireEnabled || (!control.disabled && control.getAttribute('aria-disabled') !== 'true')));
  }, { selector, enabled, hidden }, timeout,
);
const browserTransportScope = { uiOrigin, apiOrigin, project, explorer, protectedExplorer };
const report = {
  status: 'running', explorer, project, generation, protectedExplorer,
  scope: { apiOrigin, uiOrigin, browserApiTransport: 'same-origin UI /api proxy',
    rawOracle: 'project + dataset generation + explicit selected Specimen ID' },
  evidence, target, ownedApiRuntimeProof: null, oracleQueryAttempts: [], cases: [], errors: [], browserTransportViolations: [], requests: [], nativeRequests: [],
  nativeAbortProbeEvents: [], expectedOwnerCancellations: [], started: new Date().toISOString(),
};
let builder, outputId, selection, source, selectedPopulationRoute;
let activeRelatedChoiceContext;
let activePairedSemanticSourceState;
let activeCatalogChoiceSourceState;
let activeBrowserOwner = 'workspace setup';
let nativeDrainAttempted = false;
const nativeById = new Map();
const pendingNetworkReads = new Set();
const oracleQueries = [];
const expectedCancelReceipts = new Map();
const cdpRequestsById = new Map();
const cdpRequestsByCorrelationId = new Map();
const nativeByCorrelationId = new Map();
let nativeCdpSession;
const registerProposalCancelOwner = (proposalId, owner) => {
  assert(proposalId, `${owner} must bind to the active proposal receipt`);
  const evidence = {
    proposalId,
    owner: 'ConstructionProposalPanel.Cancel → constructionLifecycle.cancel → invalidateProposalRequest',
    transition: owner,
    registeredAt: new Date().toISOString(),
    requestIds: [],
  };
  expectedCancelReceipts.set(proposalId, evidence);
  for (const entry of report.nativeRequests) {
    if (entry.proposalId === proposalId || entry.body?.receiptId === proposalId) {
      entry.proposalCancelReceipt = evidence;
      if (!evidence.requestIds.includes(entry.requestId)) evidence.requestIds.push(entry.requestId);
    }
  }
  return evidence;
};
const pause = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const headerValue = (headers, name) => {
  const target = name.toLowerCase();
  for (const [key, value] of Object.entries(headers ?? {})) {
    if (key.toLowerCase() === target) return String(value);
  }
  return undefined;
};
const cdpInitiatorFrames = (stack) => {
  const frames = [];
  let current = stack;
  while (current && frames.length < 24) {
    for (const frame of current.callFrames ?? []) {
      if (frames.length >= 24) break;
      frames.push({ url: frame.url, functionName: frame.functionName });
    }
    current = current.parent;
  }
  return frames;
};
const cdpRequestsForCorrelationId = (requestCorrelationId) =>
  cdpRequestsByCorrelationId.get(requestCorrelationId) ?? [];
const syncCDPRequestEvidence = (entry) => {
  if (!entry.requestCorrelationId) return false;
  const matches = cdpRequestsForCorrelationId(entry.requestCorrelationId).filter((candidate) =>
    candidate.path === entry.path && candidate.method === entry.method && candidate.origin === entry.origin);
  entry.cdpRequestMatchCount = matches.length;
  if (matches.length !== 1) return false;
  const candidate = matches[0];
  entry.cdpRequestId = candidate.requestId;
  entry.requestTimestamp = candidate.requestTimestamp;
  entry.requestWallTime = candidate.requestWallTime;
  entry.resourceType = candidate.resourceType;
  entry.frameId = candidate.frameId;
  entry.loaderId = candidate.loaderId;
  entry.initiator = candidate.initiator;
  if (candidate.loadingFailed) entry.loadingFailed = candidate.loadingFailed;
  return true;
};
const recordCDPRequest = (params) => {
  let url;
  try { url = new URL(params.request?.url); } catch { return; }
  if (url.origin !== uiOrigin || !url.pathname.startsWith(`${base}/`)) return;
  const requestCorrelationId = headerValue(params.request?.headers, 'x-request-id');
  if (!requestCorrelationId) return;
  const candidate = {
    requestId: params.requestId,
    requestCorrelationId,
    origin: url.origin,
    path: url.pathname,
    method: String(params.request?.method ?? '').toUpperCase(),
    requestTimestamp: params.timestamp,
    requestWallTime: params.wallTime,
    resourceType: params.type,
    frameId: params.frameId,
    loaderId: params.loaderId,
    initiator: {
      type: params.initiator?.type,
      stack: cdpInitiatorFrames(params.initiator?.stack),
    },
  };
  cdpRequestsById.set(candidate.requestId, candidate);
  const requests = cdpRequestsByCorrelationId.get(requestCorrelationId) ?? [];
  requests.push(candidate);
  cdpRequestsByCorrelationId.set(requestCorrelationId, requests);
  for (const entry of nativeByCorrelationId.get(requestCorrelationId) ?? []) syncCDPRequestEvidence(entry);
};
const recordCDPFailure = (params) => {
  const candidate = cdpRequestsById.get(params.requestId);
  if (!candidate) return;
  candidate.loadingFailed = {
    errorText: params.errorText ?? 'unknown',
    canceled: params.canceled === true || params.errorText === 'net::ERR_ABORTED',
    timestamp: params.timestamp,
    blockedReason: params.blockedReason,
    corsErrorStatus: params.corsErrorStatus,
    at: Date.now(),
  };
  for (const entry of nativeByCorrelationId.get(candidate.requestCorrelationId) ?? []) syncCDPRequestEvidence(entry);
};
const expectedScopeForNativeRequest = (entry) => {
  const captured = entry.requestStateAtStart;
  if (!captured?.project || !captured?.explorer || !captured?.outputId || !captured?.snapshotToken) return undefined;
  const context = {
    project: captured.project,
    explorer: captured.explorer,
    origin: uiOrigin,
    outputId: captured.outputId,
    snapshotToken: captured.snapshotToken,
  };
  const endpoint = entry.path.startsWith(`${base}/`) ? entry.path.slice(`${base}/`.length) : '';
  if (endpoint === 'population-routes') {
    if (!captured.selectionRevisionId) return undefined;
    context.selectionRevisionId = captured.selectionRevisionId;
  }
  if (endpoint === 'related-expand-choices') {
    if (!captured.relatedExpansionContext || !Number.isInteger(captured.draftVersion) || !captured.draftDigest) return undefined;
    Object.assign(context, {
      expectedDraftVersion: captured.draftVersion,
      expectedDraftDigest: captured.draftDigest,
      ...captured.relatedExpansionContext,
    });
  }
  if (endpoint === 'construction-choices') {
    const sourceCandidates = captured.constructionChoiceSourceCandidates;
    if (!Array.isArray(sourceCandidates) || sourceCandidates.length === 0) return undefined;
    return sourceCandidates.map(({ source: expectedSource, evidence }) => ({
      ...context, source: expectedSource, sourceEvidence: evidence,
    }));
  }
  return [context];
};
const annotateExpectedOwnedCancellation = (entry) => {
  if (entry.expectedOwnerCancellation?.expected === true || !entry.cancelled ||
      entry.bodyReadStatus !== 'failed' || entry.loadingFailed?.errorText !== 'net::ERR_ABORTED' ||
      entry.loadingFailed?.canceled !== true || entry.status !== undefined || entry.responseHeadersAt !== undefined) return false;
  const expectedContexts = expectedScopeForNativeRequest(entry);
  const expectedContext = expectedContexts?.find((candidate) =>
    nativeReadRequestMatchesExpectedScope(entry, candidate));
  if (!expectedContext) {
    const endpoint = entry.path.startsWith(`${base}/`) ? entry.path.slice(`${base}/`.length) : '';
    entry.expectedOwnerCancellationDecision = {
      expected: false,
      reason: endpoint === 'construction-choices' && !entry.requestStateAtStart?.constructionChoiceSourceCandidates?.length
        ? 'independent current-owner construction-choice source was not captured before fetch'
        : expectedContexts ? 'native request did not match captured current-scope and source context' : 'independent current request context unavailable',
    };
    return false;
  }
  if (!syncCDPRequestEvidence(entry)) {
    entry.expectedOwnerCancellationDecision = { expected: false, reason: 'unique CDP request correlation unavailable' };
    return false;
  }
  entry.abortControllerProbeEvidence = nativeAbortProbeEvidenceForRequest(entry, report.nativeAbortProbeEvents);
  const classification = classifyExpectedOwnedCancellation(entry);
  entry.expectedOwnerCancellationDecision = {
    expected: classification.expected,
    reason: classification.reason,
    scopeMatched: true,
    cdpRequestId: entry.cdpRequestId,
  };
  if (!classification.expected) return false;
  entry.expectedOwnerCancellation = classification;
  entry.cancelContext = {
    owner: classification.ownerRetirement.owner,
    endpoint: classification.ownerRetirement.endpoint,
    requestId: entry.requestId,
    requestCorrelationId: entry.requestCorrelationId,
    transition: classification.ownerRetirement.componentRetirementAction,
    trustedActionAt: classification.ownerRetirement.trustedActionAt,
    componentAnchorId: classification.ownerRetirement.componentAnchorId,
  };
  report.expectedOwnerCancellations.push({
    requestId: entry.requestId,
    requestCorrelationId: entry.requestCorrelationId,
    cdpRequestId: entry.cdpRequestId,
    path: entry.path,
    owner: classification.ownerRetirement.owner,
      scope: { project: entry.scopeProject, explorer: entry.scopeExplorer, outputId: entry.body.outputId,
        snapshotToken: entry.body.snapshotToken, selectionRevisionId: entry.body.selectionRevisionId,
        expectedDraftVersion: entry.body.expectedDraftVersion, expectedDraftDigest: entry.body.expectedDraftDigest,
      stageId: entry.body.stageId, anchorColumnId: entry.body.anchorColumnId,
      targetResourceType: entry.body.targetResourceType,
      source: expectedContext.source,
      sourceEvidence: expectedContext.sourceEvidence },
    ownerRetirement: classification.ownerRetirement,
    probeEvidence: entry.abortControllerProbeEvidence,
  });
  const errorIndex = report.errors.findIndex((error) => error.kind === 'native-request' && error.requestId === entry.requestId);
  if (errorIndex >= 0) report.errors.splice(errorIndex, 1);
  return true;
};
const recordLifecycleCheck = (dimension, name, passed, checkEvidence = {}) =>
  cda.check(dimension, name, passed, checkEvidence);

const api = async (path, body) => {
  assert(!path.includes(protectedExplorer), `Refusing to address protected Explorer ${protectedExplorer}`);
  const requestId = `group-edit-column-${randomUUID()}`;
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, requestId, body, status: response.status, response: value });
  assert(response.ok, `${response.status} ${JSON.stringify(value)}`);
  return value;
};
const identity = (state) => ({ snapshotToken: state.catalog.snapshotToken,
  expectedDraftVersion: state.draftVersion, expectedDraftDigest: state.draftDigest });
const doc = (state = builder) => state.workspace.documents.find((item) => item.output.id === outputId);
const semanticConceptLabel = (item) => {
  const label = String(item?.display ?? '').trim() || String(item?.code ?? '').trim() || String(item?.sourcePath ?? '');
  return /^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$/.test(label)
    ? label.replaceAll('_', ' ').replace(/^./, (first) => first.toUpperCase())
    : label;
};
// Mirrors PairedColumnSuggestions' eligible-entry, dedupe, authored-label, and route-limit query.
const pairedColumnRouteSearchLimit = 8;
const sameSemanticLabel = (left, right) =>
  String(left).trim().normalize('NFKC').replace(/[_-]+/g, ' ').replace(/\s+/g, ' ').toLocaleLowerCase() ===
  String(right).trim().normalize('NFKC').replace(/[_-]+/g, ' ').replace(/\s+/g, ' ').toLocaleLowerCase();
const currentPairedSuggestionAuthoredLabels = () => {
  if (!builder || !outputId || !builder.catalog?.snapshotToken) return undefined;
  const currentDocument = doc(builder);
  if (!currentDocument?.rootResourceType) return undefined;
  const capabilitiesEntry = report.nativeRequests.findLast((entry) =>
    entry.path === `${base}/construction-capabilities` && entry.method === 'POST' &&
    entry.origin === uiOrigin && entry.scopeProject === project && entry.scopeExplorer === explorer &&
    entry.status === 200 && entry.bodyReadStatus === 'decoded' &&
    entry.body?.outputId === outputId && entry.body?.snapshotToken === builder.catalog.snapshotToken &&
    entry.body?.expectedDraftVersion === builder.draftVersion &&
    entry.body?.expectedDraftDigest === builder.draftDigest &&
    entry.response?.stages?.at(-1)?.id === entry.body?.stageId &&
    Array.isArray(entry.response?.stages?.at(-1)?.columns));
  if (!capabilitiesEntry) return undefined;
  const stageColumns = capabilitiesEntry.response.stages.at(-1).columns;
  return [...stageColumns, ...(currentDocument.columns ?? [])]
    .map((column) => column?.label)
    .filter((label) => typeof label === 'string' && label.length > 0);
};
const pairedSuggestionSourcesFromInventory = (entry) => {
  if (!entry || entry.path !== `${base}/semantic-inventory` || entry.method !== 'POST' ||
      entry.origin !== uiOrigin || entry.scopeProject !== project || entry.scopeExplorer !== explorer ||
      !entry.requestCorrelationId?.startsWith('paired-column-inventory-') ||
      entry.status !== 200 || entry.bodyReadStatus !== 'decoded' ||
      entry.body?.snapshotToken !== builder?.catalog?.snapshotToken ||
      entry.body?.rowRoot !== doc(builder)?.rootResourceType ||
      entry.requestStateAtStart?.project !== project || entry.requestStateAtStart?.explorer !== explorer ||
      entry.requestStateAtStart?.outputId !== outputId ||
      entry.requestStateAtStart?.snapshotToken !== builder?.catalog?.snapshotToken ||
      entry.requestStateAtStart?.draftVersion !== builder?.draftVersion ||
      entry.requestStateAtStart?.draftDigest !== builder?.draftDigest ||
      entry.response?.state !== 'complete' ||
      typeof entry.response?.contextToken !== 'string' || !entry.response.contextToken ||
      typeof entry.response?.buildId !== 'string' || !entry.response.buildId ||
      !Array.isArray(entry.response?.entries)) return undefined;
  const authoredLabels = currentPairedSuggestionAuthoredLabels();
  if (!authoredLabels) return undefined;
  const seen = new Set();
  const candidates = [];
  for (const item of entry.response.entries) {
    if (!String(item?.code ?? '').trim() || !semanticConceptLabel(item) ||
        !['READY', 'READY_WITH_WARNING'].includes(item?.readiness?.status) ||
        typeof item.conceptId !== 'string' || !item.conceptId ||
        typeof item.bindingId !== 'string' || !item.bindingId) continue;
    const identityKey = `${item.conceptId}\u0000${item.bindingId}`;
    if (seen.has(identityKey)) continue;
    seen.add(identityKey);
    if (authoredLabels.some((label) => sameSemanticLabel(label, semanticConceptLabel(item)))) continue;
    candidates.push({
      source: {
        kind: 'SEMANTIC', contextToken: entry.response.contextToken, buildId: entry.response.buildId,
        conceptId: item.conceptId, bindingId: item.bindingId,
      },
      evidence: 'successful-current-semantic-inventory-entry-before-paired-choice-fetch',
    });
  }
  return {
    project, explorer, origin: uiOrigin, outputId,
    snapshotToken: builder.catalog.snapshotToken,
    draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
    inventoryRequestId: entry.requestCorrelationId,
    capturedAt: Date.now(),
    sources: candidates.slice(0, pairedColumnRouteSearchLimit),
  };
};
const sourceCandidatesAtRequestStart = (requestCorrelationId) => {
  const currentMatches = (state) => state && state.project === project && state.explorer === explorer &&
    state.origin === uiOrigin && state.outputId === outputId &&
    state.snapshotToken === builder?.catalog?.snapshotToken &&
    state.draftVersion === builder?.draftVersion && state.draftDigest === builder?.draftDigest &&
    Array.isArray(state.sources);
  const expectedOwnerSources = requestCorrelationId?.startsWith('paired-column-choices-')
    ? activePairedSemanticSourceState
    : requestCorrelationId?.startsWith('construction-choices-')
      ? activeCatalogChoiceSourceState
      : undefined;
  return currentMatches(expectedOwnerSources)
    ? expectedOwnerSources.sources.map((candidate) => structuredClone(candidate))
    : [];
};
const recordAction = (name, startedAt, details = {}) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms (limit 5000 ms)`);
  report.cases.push({ name, durationMs, ...details });
  return durationMs;
};
const command = async (commands) => {
  await api(base + '/commands', {
    ...identity(builder), commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10, commands,
  });
  builder = await api(base + '/builder');
};
const rawQuery = (query) => {
  assert(query.includes(JSON.stringify(project)), 'Raw oracle query must scope to the CDA project');
  assert(query.includes(JSON.stringify(generation)), 'Raw oracle query must scope to the CDA generation');
  oracleQueries.push(query);
  const timeoutMs = 30000;
  const querySha256 = createHash('sha256').update(query).digest('hex');
  const invocation = buildArangoShellInvocation({
    container: arangoContainer,
    script: `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
    database: 'loom_dev',
  });
  const startedAt = Date.now();
  const result = spawnSync(invocation.command, invocation.args, { encoding: 'utf8', timeout: timeoutMs });
  const attempt = {
    querySha256,
    project,
    generation,
    database: 'loom_dev',
    timeoutMs,
    elapsedMs: Date.now() - startedAt,
    status: Number.isInteger(result.status) ? result.status : null,
    signal: typeof result.signal === 'string' ? result.signal : null,
    errorCode: typeof result.error?.code === 'string' ? result.error.code : null,
    stdoutBytes: Buffer.byteLength(result.stdout ?? ''),
    stderrBytes: Buffer.byteLength(result.stderr ?? ''),
  };
  report.oracleQueryAttempts.push(attempt);
  assert.equal(result.status, 0, `Raw CDA oracle process failed: ${JSON.stringify(attempt)}`);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf('[')));
};
const waitNetwork = async () => {
  const timeoutMs = 5000;
  const reads = [...pendingNetworkReads];
  if (reads.length === 0) return;
  const completed = await Promise.race([
    Promise.allSettled(reads).then(() => true),
    pause(timeoutMs).then(() => false),
  ]);
  assert(completed, `Native response-body reads exceeded the ${timeoutMs} ms drain: ${JSON.stringify(report.nativeRequests.filter((entry) => entry.bodyReadStatus === 'pending' || entry.bodyReadStatus === 'reading').map(({ requestId, path, status, bodyReadStatus }) => ({ requestId, path, status, bodyReadStatus })))}`);
};
const drainNativeRequests = async () => {
  const timeoutMs = 5000;
  assert.equal(nativeDrainAttempted, false, 'Strict native request drain is bounded to one attempt');
  nativeDrainAttempted = true;
  const deadline = Date.now() + timeoutMs;
  let quietSince;
  let quietRequestCount = -1;
  while (Date.now() < deadline) {
    const remainingMs = deadline - Date.now();
    await Promise.race([
      Promise.allSettled([...pendingNetworkReads]),
      pause(Math.max(1, Math.min(25, remainingMs))),
    ]);
    const pending = report.nativeRequests.filter((entry) => !entry.networkTerminal
      || entry.bodyReadStatus === 'pending' || entry.bodyReadStatus === 'reading');
    if (pending.length > 0) {
      quietSince = undefined;
      quietRequestCount = -1;
      continue;
    }
    if (quietRequestCount !== report.nativeRequests.length) {
      quietRequestCount = report.nativeRequests.length;
      quietSince = Date.now();
    } else if (Date.now() - quietSince >= 250) {
      break;
    }
    await pause(25);
  }
  const pending = report.nativeRequests.filter((entry) => !entry.networkTerminal
    || entry.bodyReadStatus === 'pending' || entry.bodyReadStatus === 'reading');
  for (const entry of report.nativeRequests) annotateExpectedOwnedCancellation(entry);
  const invalid = report.nativeRequests.filter((entry) => entry.bodyReadStatus !== 'decoded'
    && entry.expectedOwnerCancellation?.expected !== true);
  const unclassifiedAborts = report.nativeRequests.filter((entry) => entry.cancelled
    && entry.expectedOwnerCancellation?.expected !== true);
  report.nativeRequestDrain = {
    status: 'checking',
    terminalRequests: report.nativeRequests.length,
    decodedResponses: report.nativeRequests.filter((entry) => entry.bodyReadStatus === 'decoded').length,
    expectedProposalCancelOwners: [...expectedCancelReceipts.values()],
    classifiedOwnedCancellations: report.expectedOwnerCancellations,
    terminalFailures: invalid.map(({ requestId, path, status, owner, bodyReadStatus, bodyError, loadingFailure, cancelContext }) => ({
      requestId, path, status, owner, bodyReadStatus, bodyError, loadingFailure,
      cancelContext: cancelContext && { proposalId: cancelContext.proposalId, owner: cancelContext.owner, transition: cancelContext.transition },
    })),
    unclassifiedAborts: unclassifiedAborts.map(({ requestId, requestCorrelationId, cdpRequestId, path, method, owner,
      startedAt, loadingFailure, loadingFailed, cdpRequestMatchCount, expectedOwnerCancellationDecision,
      proposalCancelOwnerCandidate }) => ({ requestId, requestCorrelationId, cdpRequestId, path, method, owner,
      startedAt, loadingFailure, loadingFailed, cdpRequestMatchCount, expectedOwnerCancellationDecision,
      proposalCancelReceiptId: proposalCancelOwnerCandidate?.proposalId })),
  };
  assert.deepEqual(pending, [], `Native API drain left requests without a terminal event/body result: ${JSON.stringify(pending.map(({ requestId, path, method, owner, status, bodyReadStatus, startedAt }) => ({ requestId, path, method, owner, status, bodyReadStatus, startedAt })))}`);
  assert.deepEqual(unclassifiedAborts, [], `Native API aborts lack exact AbortSignal, scope/CAS, and owner-retirement proof: ${JSON.stringify(report.nativeRequestDrain.unclassifiedAborts)}`);
  assert.deepEqual(invalid, [], `Native API requests failed or had undecoded bodies (terminal aborts remain failures until an authoritative owner/retirement diagnostic proves them): ${JSON.stringify(report.nativeRequestDrain.terminalFailures)}`);
  assert(quietSince && Date.now() - quietSince >= 250,
    `Native API recorder did not reach 250 ms of bounded quiescence before the ${timeoutMs} ms deadline`);
  report.nativeRequestDrain.status = 'complete';
};
const assertSourceBinding = (state, route) => {
  assert.equal(state.catalog.generation, source.generation);
  assert.equal(state.catalog.authorizationScopeDigest, selection.scopeDigest);
  const document = doc(state);
  assert.equal(document.population.selectionRevisionId, selection.id);
  assert.deepEqual(document.population.route, route);
  assert.equal(document.rootResourceType, 'Specimen');
  return document;
};
const assertPreviewValues = (preview, expectedRows, label) => {
  assert(expectedRows.length <= 25, `${label} must have a complete bounded witness population of at most 25 rows`);
  assert.equal(preview.outputId, outputId, `${label} receipt must belong to the selected output`);
  assert.equal(preview.rowCount, expectedRows.length, `${label} row count differs from raw witnesses`);
  const displayed = preview.rows.map((row) => preview.columns.map((column) => row[column.column]));
  assert.equal(displayed.length, expectedRows.length, `${label} must return every bounded witness row`);
  assertExactMultiset(displayed, expectedRows, label);
};
const expectedPreview = async (expectedRows, { requestStart, expectedBuilder, label, startedAt }) => {
  const deadline = startedAt + 5000;
  await waitForBrowser(page, ({ outputId: expectedOutput, version, digest }) => {
    const p = document.querySelector('[data-testid="construction-preview"]');
    return p?.dataset.previewStatus === 'ready' && p?.dataset.previewOutputId === expectedOutput &&
      p?.dataset.currentDraftVersion === version && p?.dataset.currentDraftDigest === digest && Boolean(p?.dataset.previewReceiptId);
  }, Math.max(1, deadline - Date.now()), { outputId, version: String(expectedBuilder.draftVersion), digest: expectedBuilder.draftDigest });
  const active = await browserEval(page, () => {
    const p = document.querySelector('[data-testid="construction-preview"]');
    return { status: p?.dataset.previewStatus, receiptId: p?.dataset.previewReceiptId, outputId: p?.dataset.previewOutputId,
      draftVersion: p?.dataset.currentDraftVersion, draftDigest: p?.dataset.currentDraftDigest };
  });
  assert.equal(active.status, 'ready');
  assert.equal(active.outputId, outputId);
  assert.equal(active.draftVersion, String(expectedBuilder.draftVersion));
  assert.equal(active.draftDigest, expectedBuilder.draftDigest);
  assert(active.receiptId, `${label} must expose the current saved receipt`);
  await waitNetwork();
  const request = selectSavedPreviewRequest(report.nativeRequests, {
    startIndex: requestStart, path: base + '/preview', receiptId: active.receiptId, outputId,
  });
  assert(request, `${label} needs a native receipt-bound Preview request and response`);
  assert.equal(request.response.receiptId, active.receiptId);
  assert.equal(request.response.outputId, outputId);
  assertPreviewValues(request.response, expectedRows, label);
  assert(Date.now() <= deadline, `${label} exceeded its five-second action budget`);
  return { active, response: request.response, requestIndex: report.nativeRequests.indexOf(request) };
};
const open = async (expectedRows, columnCount = expectedRows[0]?.length ?? 2, label = 'reload') => {
  activeBrowserOwner = label;
  const startedAt = Date.now();
  const requestStart = report.nativeRequests.length;
  await navigate(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForControl(page, `[data-testid="construction-table-${outputId}"]`);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForControl(page, '[data-testid="construction-rows-settings-trigger"]', { enabled: true });
  await waitForBrowser(page, ({ columnCount: count }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-colcount') === count && !document.body.innerText.includes('Loading your table…');
  }, 5000, { columnCount: String(columnCount) });
  builder = await api(base + '/builder');
  const binding = assertSourceBinding(builder, selectedPopulationRoute);
  assert.equal(binding.output.id, outputId);
  await expectedPreview(expectedRows, { requestStart, expectedBuilder: builder, label, startedAt });
  recordAction(label, startedAt, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
  return builder;
};
const mountedRows = async (expectedRows, columnCount = expectedRows[0]?.length ?? 2) => {
  assert(expectedRows.length <= 25, 'Rendered witness population must be complete within the 25-row preview bound');
  await waitForBrowser(page, ({ rows: rowCount, columns: columnTotal }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === rowCount && table?.getAttribute('aria-colcount') === columnTotal &&
      !document.body.innerText.includes('Loading your table…');
  }, 5000, { rows: String(Math.min(25, expectedRows.length) + 1), columns: String(columnCount) });
  const rows = await browserEval(page, () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
    .slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));
  assert.equal(rows.length, expectedRows.length, 'Rendered preview must contain the complete bounded witness population');
  const normalizeRendered = (value) => value.split('; ').sort().join('; ');
  const renderedActual = rows.map((row) => row.map(normalizeRendered));
  const renderedExpected = expectedRows.map((row) => row.map((value) => Array.isArray(value)
    ? [...value].sort().join('; ')
    : normalizeRendered(String(value))));
  assertExactMultiset(renderedActual, renderedExpected, 'rendered preview');
};
const nativeByRequest = new WeakMap();
let nextBrowserRequestId = 1;
const installBrowserCapture = async () => {
  await page.context().exposeBinding('__loomNativeAbortProbeBinding', (_source, payload) => {
    let event;
    try { event = JSON.parse(payload); }
    catch { report.nativeAbortProbeEvents.push({ kind: 'probe-payload-invalid', payloadLength: String(payload).length }); return; }
    report.nativeAbortProbeEvents.push(event);
  });
  await page.context().addInitScript(createNativeAbortProbeSource({ project, explorer }));
  nativeCdpSession = await page.context().newCDPSession(page);
  nativeCdpSession.on('Network.requestWillBeSent', recordCDPRequest);
  nativeCdpSession.on('Network.loadingFailed', recordCDPFailure);
  await nativeCdpSession.send('Network.enable');
  page.on('pageerror', error => report.errors.push({ kind: 'runtime', text: error.message }));
  page.on('console', message => {
    if (message.type() !== 'error') return;
    const location = message.location().url;
    if (location === `${uiOrigin}/favicon.ico` && /404 \(Not Found\)/.test(message.text())) {
      (report.assetFailures ??= []).push({ url: location, status: 404, kind: 'console' });
      return;
    }
    report.errors.push({ kind: 'console', text: message.text() });
  });
  page.on('request', request => {
    const transport = classifyNativeBrowserApiRequest(request.url(), browserTransportScope);
    if (transport.kind === 'ignore') return;
    if (transport.kind !== 'capture') {
      const violation = { reason: transport.reason, url: request.url(), owner: activeBrowserOwner };
      report.browserTransportViolations.push(violation);
      report.errors.push({ kind: 'browser-api-transport', ...violation });
      return;
    }
    const url = transport.url;
    let body;
    let requestBodyParseError;
    try { body = request.postData() ? JSON.parse(request.postData()) : undefined; }
    catch (error) { body = request.postData(); requestBodyParseError = String(error); }
    const headers = request.headers();
    const requestCorrelationId = headers['x-request-id'];
    if (url.pathname === `${base}/semantic-inventory` && requestCorrelationId?.startsWith('paired-column-inventory-')) {
      activePairedSemanticSourceState = undefined;
    }
    const requestId = requestCorrelationId ?? `playwright-${nextBrowserRequestId++}`;
    const entry = {
      requestId, path: url.pathname, origin: url.origin, method: request.method(),
      url: request.url(), owner: activeBrowserOwner,
      transportScope: transport.scope, body, requestBodyParseError,
      authorizationHeaderPresent: Object.keys(headers).some(key => key.toLowerCase() === 'authorization'),
      requestCorrelationId,
      scopeProject: project, scopeExplorer: explorer,
      requestStateAtStart: { project, explorer, outputId, snapshotToken: builder?.catalog?.snapshotToken,
        draftVersion: builder?.draftVersion, draftDigest: builder?.draftDigest,
        selectionRevisionId: selection?.id,
        relatedExpansionContext: activeRelatedChoiceContext ? { ...activeRelatedChoiceContext } : undefined,
        constructionChoiceSourceCandidates: url.pathname === `${base}/construction-choices`
          ? sourceCandidatesAtRequestStart(requestCorrelationId) : undefined },
      request: body,
      startedAt: Date.now(), status: undefined, completedAt: null, networkTerminal: false,
      bodyReadStatus: 'pending', terminalState: 'pending', cancelled: false,
    };
    report.nativeRequests.push(entry);
    nativeById.set(requestId, entry);
    nativeByRequest.set(request, entry);
    if (requestCorrelationId) {
      const entries = nativeByCorrelationId.get(requestCorrelationId) ?? [];
      entries.push(entry);
      nativeByCorrelationId.set(requestCorrelationId, entries);
      syncCDPRequestEvidence(entry);
    }
  });
  page.on('response', response => {
    const request = response.request();
    const entry = nativeByRequest.get(request);
    if (!entry) return;
    entry.status = response.status();
    const responseURL = new URL(response.url());
    entry.responseBinding = {
      url: response.url(), origin: responseURL.origin, path: responseURL.pathname,
      matchesCapturedUiProxyRequest: isSameUiProxyResponse(entry.url, response.url(), browserTransportScope),
    };
    if (!entry.responseBinding.matchesCapturedUiProxyRequest) {
      const violation = { reason: 'response-url-does-not-match-ui-proxy-request', requestId: entry.requestId, requestUrl: entry.url, responseUrl: response.url() };
      report.browserTransportViolations.push(violation);
      report.errors.push({ kind: 'browser-api-transport', ...violation });
    }
    entry.responseHeadersAt = Date.now();
    if (response.status() >= 400) report.errors.push({ kind: 'http', path: entry.path, status: response.status() });
    entry.bodyReadStatus = 'reading';
    let read;
    read = response.text().then(text => {
      try { entry.response = JSON.parse(text); } catch { entry.response = text; }
      entry.proposalId = entry.response?.proposalId ?? entry.body?.receiptId;
      entry.bodyReadStatus = 'decoded';
      if (entry.path === `${base}/semantic-inventory` &&
          entry.requestCorrelationId?.startsWith('paired-column-inventory-')) {
        activePairedSemanticSourceState = pairedSuggestionSourcesFromInventory(entry);
      }
    }).catch(error => {
      entry.responseReadError = String(error);
      entry.bodyReadStatus = 'failed';
      entry.bodyError = entry.responseReadError;
      report.errors.push({ kind: 'native-response-body', requestId: entry.requestId, path: entry.path, message: entry.bodyError });
    }).finally(() => {
      entry.completedAt = Date.now();
      pendingNetworkReads.delete(read);
    });
    pendingNetworkReads.add(read);
  });
  page.on('requestfinished', request => {
    const entry = nativeByRequest.get(request);
    if (!entry) return;
    entry.networkTerminal = true;
    entry.terminalState = 'finished';
    entry.completedAt ??= Date.now();
  });
  page.on('requestfailed', request => {
    const entry = nativeByRequest.get(request);
    if (!entry) return;
    entry.networkTerminal = true;
    entry.terminalState = 'failed';
    const failure = request.failure();
    entry.cancelled = /aborted|cancelled/i.test(failure?.errorText ?? '');
    entry.loadingFailure = { errorText: failure?.errorText ?? 'unknown', canceled: entry.cancelled };
    const proposalId = entry.proposalId ?? entry.body?.receiptId;
    const expectedOwner = proposalId ? expectedCancelReceipts.get(proposalId) : undefined;
    if (expectedOwner) entry.proposalCancelOwnerCandidate = expectedOwner;
    entry.bodyReadStatus = 'failed';
    entry.bodyError = `requestfailed: ${failure?.errorText ?? 'unknown'}`;
    entry.completedAt = Date.now();
    report.errors.push({ kind: 'native-request', requestId: entry.requestId, path: entry.path,
      errorText: failure?.errorText ?? 'unknown', canceled: entry.cancelled, owner: entry.owner });
  });
};
const assertDraftRequest = (entry, state, label) => {
  assert(entry, `${label} must use the native Browser request`);
  assert.equal(entry.origin, uiOrigin, `${label} must use the configured local UI proxy`);
  assert(entry.path.startsWith(`${base}/`), `${label} must target the current project and owned Explorer`);
  assert.equal(entry.responseBinding?.origin, uiOrigin, `${label} response must remain bound to the UI proxy origin`);
  assert.equal(entry.responseBinding?.path, entry.path, `${label} response must remain bound to the captured API path`);
  assert.equal(entry.responseBinding?.matchesCapturedUiProxyRequest, true,
    `${label} response URL must bind to the exact captured same-origin UI proxy request`);
  assert.equal(entry.authorizationHeaderPresent, false);
  assert.equal(entry.body.snapshotToken, state.catalog.snapshotToken);
  assert.equal(entry.body.expectedDraftVersion ?? entry.body.draftVersion, state.draftVersion);
  assert.equal(entry.body.expectedDraftDigest ?? entry.body.draftDigest, state.draftDigest);
  assert.equal(entry.status, 200);
  assert(entry.response, `${label} response body must be captured`);
  if (entry.path.endsWith('/commands')) {
    assert(entry.body.commands?.some((command) => command.outputId === outputId || command.output === outputId),
      `${label} native command must target the selected output`);
    assert(entry.response.workspace, `${label} must return the canonical applied workspace`);
    assert.equal(entry.response.draftVersion, state.draftVersion + 1, `${label} must advance exactly one draft revision`);
  } else {
    assert.equal(entry.body.outputId, outputId);
    assert.equal(entry.response.outputId, outputId);
    assert.equal(entry.response.snapshotToken, state.catalog.snapshotToken);
    assert.equal(entry.response.draftVersion, state.draftVersion);
    assert.equal(entry.response.draftDigest, state.draftDigest);
  }
  return entry.response;
};
const browserProposal = async ({ requestStart, path, expectedRows, state, label, startedAt }) => {
  assert(path.endsWith('/construction-proposals'), `${label} must use the current native construction-proposals endpoint`);
  const panelSelector = '[data-testid="construction-proposal-panel"]';
  await waitForBrowser(page, ({ selector }) => ['ready', 'error', 'needs-repair'].includes(document.querySelector(selector)?.dataset.proposalStatus),
    Math.max(1, startedAt + 5000 - Date.now()), { selector: panelSelector });
  const panel = await browserEval(page, ({ selector }) => {
    const p = document.querySelector(selector);
    return { status: p?.dataset.proposalStatus, id: p?.dataset.proposalId, text: p?.innerText,
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => ({ text: cell.innerText, title: cell.title }))) };
  }, { selector: panelSelector });
  assert.equal(panel.status, 'ready', panel.text);
  const matching = () => report.nativeRequests.slice(requestStart).findLast((entry) => entry.path === path && entry.completedAt && entry.status === 200);
  await waitNetwork();
  const request = matching();
  const response = assertDraftRequest(request, state, label);
  assert.equal(response.previewStatus, 'READY', `${label} proposal must compile a ready automatic preview`);
  assert.equal(response.preview?.receiptId, response.proposalId, `${label} candidate receipt must bind to its proposal`);
  assert.equal(response.preview?.outputId, outputId);
  assert.equal(panel.id, response.proposalId);
  assertPreviewValues(response.preview, expectedRows, label);
  const normalizeDisplayedCell = (value) => value.split('; ').sort().join('; ');
  const displayedRows = panel.rows.map((row) => row.map((cell) => normalizeDisplayedCell(cell.text)));
  const expectedDisplayedRows = expectedRows.map((row) => row.map((value) => Array.isArray(value)
    ? [...value].sort().join('; ')
    : normalizeDisplayedCell(String(value))));
  assert.equal(displayedRows.length, expectedRows.length, `${label} proposal panel must render the complete witness population`);
  assertExactMultiset(displayedRows, expectedDisplayedRows, `${label} proposal panel DOM`);
  recordAction(label, startedAt, { receiptId: response.proposalId, candidateWorkspaceDigest: response.candidateWorkspaceDigest });
  return { request, requestIndex: report.nativeRequests.indexOf(request), response, panel };
};
const findNative = (startIndex, path, predicate = () => true) => report.nativeRequests.slice(startIndex).findLast((entry) => entry.path === path && predicate(entry));
const applyGroupProposal = async (proposal, savedBefore, expectedRows, label) => {
  activeBrowserOwner = label;
  const startedAt = Date.now();
  const requestStart = report.nativeRequests.length;
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  await mountedRows(expectedRows, expectedRows[0]?.length ?? 2);
  await waitNetwork();
  const applyRequest = findNative(requestStart, base + '/commands', (entry) => entry.body?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_PROPOSAL'));
  assertDraftRequest(applyRequest, savedBefore, label);
  assert(applyRequest.body.commands.some((item) => item.proposalId === proposal.proposalId));
  builder = await api(base + '/builder');
  assert.equal(builder.draftVersion, proposal.draftVersion + 1);
  assert.equal(builder.draftDigest, proposal.candidateWorkspaceDigest);
  recordAction(label, startedAt, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
  return builder;
};
const editGroupAggregate = async (operation, expectedRows, state, label) => {
  activeBrowserOwner = label;
  const groupStep = doc(state).construction.steps.find((step) => step.operation.kind === 'GROUP');
  assert(groupStep, 'Saved construction must contain the authored Group');
  const stepId = groupStep.id;
  const startIndex = report.nativeRequests.length;
  await click(page, `[data-testid="construction-history-step-${stepId}"]`);
  await click(page, `[data-testid="construction-edit-step-${stepId}"]`);
  await waitForControl(page, 'select[aria-label="Summary 1"]', { enabled: true });
  const startedAt = Date.now();
  await selectOption(page, 'select[aria-label="Summary 1"]', operation);
  let selectedField;
  if (operation === 'COUNT_DISTINCT') {
    await waitForControl(page, 'select[aria-label="Summary field 1"]', { enabled: true });
    const options = await browserEval(page, () => [...document.querySelector('select[aria-label="Summary field 1"]').options]
      .map(option => ({ value: option.value, label: option.textContent })));
    selectedField = options.find((field) => field.label.startsWith('Observation FHIR resource ID'));
    assert(selectedField, `Active related Observation identity must be available to Group before a downstream projection: ${JSON.stringify(options)}`);
    await selectOption(page, 'select[aria-label="Summary field 1"]', selectedField.value);
  }
  const proposal = await browserProposal({ requestStart: startIndex,
    path: base + '/construction-proposals', expectedRows, state, label, startedAt });
  const group = proposal.response.candidateConstruction.steps.find((step) => step.id === stepId);
  assert(group?.operation.kind === 'GROUP');
  assert.equal(group.operation.group.aggregates[0].operation, operation);
  if (selectedField) assert.equal(group.operation.group.aggregates[0].inputColumnId, selectedField.value);
  return { ...proposal, groupStepId: stepId };
};

try {
  report.ownedApiRuntimeProof = assertCdaNoAuthRuntime({ apiContainer: target.apiContainer });
  const sourceQuery = `
FOR s IN (
  FOR candidate IN Specimen
    FILTER candidate.project == ${JSON.stringify(project)} AND candidate.dataset_generation == ${JSON.stringify(generation)}
      AND candidate.resourceType == "Specimen" AND candidate.payload.resourceType == "Specimen"
    SORT candidate.id
    LIMIT 2000
    RETURN {id:candidate.id,_id:candidate._id,generation:candidate.dataset_generation,resourceType:candidate.resourceType}
)
  LET patients = (
    FOR e IN fhir_edge
      FILTER e._from == s._id AND e._to != null AND e.label == "subject_Patient"
        AND e.from_type == "Specimen" AND e.to_type == "Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      LET p = DOCUMENT(e._to)
      FILTER p != null AND p.project == ${JSON.stringify(project)} AND p.dataset_generation == ${JSON.stringify(generation)}
        AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient"
      RETURN DISTINCT {id:p.id,_id:p._id,resourceType:p.resourceType,generation:p.dataset_generation}
  )
  FILTER LENGTH(patients) == 1
  LET patient = patients[0]
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == patient._id
        AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)}
        AND e.from_type == "Observation" AND e.to_type == "Patient"
        AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o != null AND o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
        AND o.resourceType == "Observation" AND o.payload.resourceType == "Observation"
      SORT o.id
      RETURN {id:o.id,_id:o._id,status:o.payload.status,hasScalarStatus:IS_STRING(o.payload.status),resourceType:o.resourceType,generation:o.dataset_generation}
  )
  LET scalarObservations = (FOR observation IN observations FILTER observation.hasScalarStatus RETURN observation)
  LET statuses = UNIQUE(scalarObservations[*].status)
  FILTER LENGTH(observations) >= 2 AND LENGTH(observations) <= 24
    AND LENGTH(scalarObservations) == LENGTH(observations)
  SORT s.id
  LIMIT 1
  RETURN {specimen:s,patient,observations,statuses}
`;
  const [witness] = rawQuery(sourceQuery);
  assert(witness?.specimen?.id && witness?.specimen?._id && witness?.patient?.id && witness?.observations?.length >= 2,
    'Bounded raw CDA scan must find a Specimen→Patient witness with 2–24 distinct Observations, all carrying scalar status');
  source = witness.specimen;
  const patient = witness.patient;
  const observations = witness.observations;
  const observationIDs = observations.map(({ id }) => id);
  const observationStatuses = observations.map(({ status }) => status).sort();
  assert(observations.every(({ hasScalarStatus }) => hasScalarStatus), 'Every selected Observation must carry scalar status');
  assert.equal(source.generation, generation);
  assert.equal(source.resourceType, 'Specimen');
  assert.equal(patient.resourceType, 'Patient');
  assert.equal(patient.generation, generation);
  assert.equal(new Set(observationIDs).size, observations.length, 'Raw witness Observation IDs must be unique');
  assert(observationIDs.length >= 2, 'Raw witness must contain at least two distinct Observation IDs');
  assert(observations.length <= 24, 'Raw witness must fit completely in the 25-row preview bound');
  assert(observations.every(({ resourceType, generation: observationGeneration }) =>
    resourceType === 'Observation' && observationGeneration === generation),
  'Every selected Observation must retain exact resource type and generation identity');
  recordLifecycleCheck('correctness',
    'bounded real CDA oracle selects exact Specimen→Patient→Observation status witness',
    source.generation === generation && observationIDs.length === observationStatuses.length &&
      observationIDs.length >= 2 && observationIDs.length <= 24 && observations.every(({ hasScalarStatus }) => hasScalarStatus),
    { project, generation, sourceID: source.id, patientID: patient.id, observationIDs,
      statusValues: observationStatuses });
  report.oracle = { kind: 'bounded real CDA source fixture and exact project/generation-scoped raw fhir_edge witness',
    sourceQuery, searchBounds: { sortedSpecimens: 2000, observationsPerPatientMin: 2, observationsPerPatientMax: 24 },
    source, patient, observations, statusValuesPresent: [...new Set(observationStatuses)].sort(),
    statusMultiplicityPreserved: true, chain: [] };

  const createdExplorer = await api(root, { name: requestedExplorerName, title: 'Group edit before related column QA' });
  const creationRequest = report.requests.at(-1);
  assert.equal(creationRequest?.path, root);
  assert.equal(creationRequest?.status, 201);
  assert.equal(creationRequest?.body?.name, requestedExplorerName);
  const createdScope = createdExplorerScope(project, createdExplorer);
  explorer = createdScope.explorerId;
  assert.notEqual(explorer, protectedExplorer, 'The server-assigned Explorer must remain isolated from the protected shared Explorer');
  explorerRoot = createdScope.explorerRoot;
  base = createdScope.authoringBase;
  browserTransportScope.explorer = explorer;
  report.explorer = explorer;
  report.target = { ...report.target, explorer };
  report.explorerProvisioning = {
    requestedName: requestedExplorerName,
    createRequestId: creationRequest.requestId,
    createStatus: creationRequest.status,
    returnedProject: createdExplorer.project,
    returnedExplorerId: explorer,
  };
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, generation);
  const node = builder.catalog.nodes.find((item) => item.resourceType === 'Specimen' && item.rowRootEligible);
  assert(node, 'The real CDA Specimen collection must be loaded');
  await command([{ type: 'CREATE_TABLE', title: 'Group edit before related column', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find((candidate) => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(idField, 'The CDA catalog must advertise Specimen.id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selectionPath = base.replace('/authoring/v2', '/selections');
  selection = await api(selectionPath, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation, resourceType: 'Specimen', id: source.id }] } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Specimen');
  assert.equal(selection.memberCount, 1);
  assert(selection.scopeDigest);
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken,
    outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find((choice) => choice.route.length === 0);
  assert(direct, 'The raw Specimen source must attach directly to the table');
  selectedPopulationRoute = direct.route;
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  assertSourceBinding(builder, selectedPopulationRoute);

  cda.captureRequests(explorerRoot);
  await installBrowserCapture();
  const sourceRows = [[source.id]];
  await open(sourceRows, 1, 'selected-raw-Specimen-reload');
  let witnesses = [{ anchor: source._id, values: [source.id] }];
  const chain = [
    { from: 'Specimen', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND',
      expectedStage: 'source_projection' },
    { from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND',
      expectedStage: 'authored_construction' },
  ];
  let expectedExpandedRows = sourceRows;
  for (const hop of chain) {
    activeBrowserOwner = `expand-${hop.from}-${hop.to}`;
    activeRelatedChoiceContext = { ...relatedChoiceStageContext(doc(builder), hop.expectedStage), targetResourceType: hop.to };
    assert(activeRelatedChoiceContext.anchorColumnId,
      `Current ${hop.from} related stage must expose a stable anchor column before opening its editor`);
    const next = [];
    for (const witness of witnesses) {
      const endpoint = hop.direction === 'OUTBOUND' ? '_from' : '_to';
      const target = hop.direction === 'OUTBOUND' ? '_to' : '_from';
      const query = `FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)} FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to + '/')}) LET d=DOCUMENT(e.${target}) FILTER d.project==${JSON.stringify(project)} AND d.dataset_generation==${JSON.stringify(generation)} RETURN DISTINCT {id:d.id,_id:d._id}`;
      const matches = witness.anchor ? rawQuery(query) : [];
      if (matches.length) for (const match of matches) next.push({ anchor: match._id, values: [...witness.values, match.id] });
      else next.push({ anchor: null, values: [...witness.values, '—'] });
    }
    assert(next.length <= 1000, 'CDA source chain must remain bounded to at most 1000 witnesses');
    witnesses = next;
    expectedExpandedRows = witnesses.map((witness) => witness.values);
    report.oracle.chain.push({ hop, witnesses });
    const startIndex = report.nativeRequests.length;
    await click(page, '[data-testid="construction-rows-settings-trigger"]');
    await waitForControl(page, '[data-testid="construction-action-related-rows"]', { enabled: true });
    await click(page, '[data-testid="construction-action-related-rows"]');
    const editor = '[data-testid="construction-related-expand-editor"]';
    await waitForControl(page, editor + ' select[aria-label="Related record type"]', { enabled: true });
    const startedAt = Date.now();
    await selectOption(page, editor + ' select[aria-label="Related record type"]', hop.to);
    const routeLabel = hop.from + (hop.direction === 'INBOUND' ? ` <-[${hop.field}]- ` : ` -[${hop.field}]-> `) + hop.to;
    await waitForControl(page, `${editor} input[aria-label="${routeLabel}"]`, { timeout: 5000 });
    await click(page, editor + ` input[aria-label="${routeLabel}"]`);
    const expandedProposal = await browserProposal({ requestStart: startIndex, path: base + '/construction-proposals',
      expectedRows: expectedExpandedRows, state: builder, label: `expand-${hop.from}-${hop.to}`, startedAt });
    const actionStart = Date.now();
    const proposalResponse = expandedProposal.response;
    await click(page, '[data-testid="construction-apply-proposal"]');
    await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
    await mountedRows(expectedExpandedRows, expectedExpandedRows[0]?.length ?? 2);
    await waitNetwork();
    const applied = findNative(startIndex, base + '/commands', (entry) => entry.body?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === proposalResponse.proposalId));
    assertDraftRequest(applied, builder, `apply expand-${hop.from}-${hop.to}`);
    builder = await api(base + '/builder');
    assert.equal(builder.draftDigest, proposalResponse.candidateWorkspaceDigest);
    recordAction(`apply-expand-${hop.from}-${hop.to}`, actionStart, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
    assertSourceBinding(builder, selectedPopulationRoute);
  }
  activeRelatedChoiceContext = undefined;
  assert(witnesses.length > 1, 'The chosen CDA Specimen must reach multiple Observations');
  const contributorIDs = [...new Set(witnesses.map((witness) => witness.values.at(-1)))].sort();
  assert(contributorIDs.length > 1);
  const pipelineObservationIDs = new Set(witnesses.map((witness) => witness.values.at(-1)));
  assert(!pipelineObservationIDs.has('—'), 'The selected Specimen must have concrete related Observations');
  assert.deepEqual([...pipelineObservationIDs].sort(), [...observationIDs].sort(),
    'The bounded source finder and exact pipeline edge oracle must identify the same Observation set');
  const rawRouteRows = observations.map(({ id: observationID }) => {
    const matching = witnesses.find(({ values }) => values.at(-1) === observationID);
    assert(matching, `Raw Observation ${observationID} must have one exact native-route witness`);
    return matching.values;
  });
  assertExactMultiset(expectedExpandedRows, rawRouteRows, 'native related rows against raw route oracle');
  recordLifecycleCheck('correctness',
    'native Specimen→Patient and Patient→Observation choices preserve exact raw edges',
    expectedExpandedRows.length === observations.length && !pipelineObservationIDs.has('—'),
    { chain, expectedRows: expectedExpandedRows, rawObservationIDs: [...pipelineObservationIDs].sort() });
  const groupedRows = [[patient.id, witnesses.length]];
  const distinctRows = [[patient.id, contributorIDs.length]];

  const groupStartIndex = report.nativeRequests.length;
  activeBrowserOwner = 'group-by-Patient-FHIR-resource-ID-count-rows';
  await click(page, '[data-testid="construction-rows-settings-trigger"]');
  await click(page, '[data-testid="construction-action-group-rows"]');
  await waitForControl(page, 'input[aria-label="Group by Patient FHIR resource ID"]', { enabled: true });
  const groupStartedAt = Date.now();
  await click(page, 'input[aria-label="Group by Patient FHIR resource ID"]');
  const initialGroupProposal = await browserProposal({ requestStart: groupStartIndex, path: base + '/construction-proposals',
    expectedRows: groupedRows, state: builder, label: 'group-by-Specimen-ID-count-rows', startedAt: groupStartedAt });
  builder = await applyGroupProposal(initialGroupProposal.response, builder, groupedRows, 'apply-initial-Group');
  const groupBaseline = structuredClone(builder);
  const groupDocumentBaseline = structuredClone(doc(builder));
  assertSourceBinding(builder, selectedPopulationRoute);
  const groupedReload = await open(groupedRows, 2, 'reload-initial-Group');
  assert.deepEqual(doc(groupedReload).construction, groupDocumentBaseline.construction);
  recordLifecycleCheck('correctness',
    'initial Group aggregates exact related Observation rows',
    doc(groupedReload).construction.steps.some((step) => step.operation.kind === 'GROUP') &&
      JSON.stringify(groupedReload.workspace) === JSON.stringify(groupBaseline.workspace),
    { outputId, groupStepIDs: doc(groupedReload).construction.steps.filter((step) => step.operation.kind === 'GROUP').map((step) => step.id),
      groupedRows, draftVersion: groupedReload.draftVersion, draftDigest: groupedReload.draftDigest });

  // Distinct uncovered transition: commit the upstream Group edit while the
  // downstream Observation.status column does not exist yet.
  const beforeFirstEdit = structuredClone(builder);
  const countDistinctProposal = await editGroupAggregate('COUNT_DISTINCT', distinctRows, builder,
    'edit-Group-to-count-distinct-before-column');
  assert.deepEqual(countDistinctProposal.panel.rows[0].slice(0, 2).map((cell) => cell.text), distinctRows[0].map(String));
  recordLifecycleCheck('correctness',
    'COUNT_DISTINCT Group proposal previews the exact bounded raw Observation identity count',
    countDistinctProposal.response.preview?.rowCount === 1 &&
      countDistinctProposal.panel.rows.length === 1 &&
      countDistinctProposal.panel.rows[0].slice(0, 2).map((cell) => cell.text).join('\u0000') === distinctRows[0].map(String).join('\u0000'),
    { rows: countDistinctProposal.panel.rows, expectedRows: distinctRows,
      candidateGroup: countDistinctProposal.group, proposalId: countDistinctProposal.response.proposalId });
  const cancelStartedAt = Date.now();
  activeBrowserOwner = 'cancel-Group-edit-before-column';
  registerProposalCancelOwner(countDistinctProposal.response.proposalId, 'cancel the first upstream Group aggregate candidate');
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { hidden: true });
  const afterEditCancel = await api(base + '/builder');
  assert.equal(afterEditCancel.draftVersion, beforeFirstEdit.draftVersion);
  assert.equal(afterEditCancel.draftDigest, beforeFirstEdit.draftDigest);
  assert.deepEqual(afterEditCancel.workspace, beforeFirstEdit.workspace, 'Group edit Cancel must preserve the exact source and draft bindings');
  recordAction('cancel-Group-edit-before-column', cancelStartedAt);
  recordLifecycleCheck('persistence',
    'Group COUNT_DISTINCT edit Cancel preserves exact prior draft before adding related column',
    afterEditCancel.draftVersion === beforeFirstEdit.draftVersion &&
      afterEditCancel.draftDigest === beforeFirstEdit.draftDigest &&
      JSON.stringify(afterEditCancel.workspace) === JSON.stringify(beforeFirstEdit.workspace),
    { draftVersion: afterEditCancel.draftVersion, draftDigest: afterEditCancel.draftDigest,
      workspace: afterEditCancel.workspace });

  const appliedDistinctProposal = await editGroupAggregate('COUNT_DISTINCT', distinctRows, afterEditCancel,
    'reapply-Group-count-distinct-before-column');
  builder = await applyGroupProposal(appliedDistinctProposal.response, afterEditCancel, distinctRows,
    'apply-Group-edit-before-column');
  const distinctDocument = structuredClone(doc(builder));
  const reloadedDistinct = await open(distinctRows, 2, 'reload-Group-edit-before-column');
  assert.deepEqual(doc(reloadedDistinct).construction, distinctDocument.construction);
  const savedGroupStep = doc(reloadedDistinct).construction.steps.find((step) => step.operation.kind === 'GROUP');
  assert.equal(savedGroupStep.operation.group.aggregates[0].operation, 'COUNT_DISTINCT');
  assert.equal(doc(reloadedDistinct).construction.steps.some((step) => step.operation.kind === 'RELATED_SOURCE'), false,
    'The downstream related source must still be absent after the upstream edit reload');
  recordLifecycleCheck('persistence',
    'Group COUNT_DISTINCT Apply and reload persist exact pre-column Group without RELATED_SOURCE',
    doc(reloadedDistinct).construction.steps.find((step) => step.operation.kind === 'GROUP')?.operation.group.aggregates[0]?.operation === 'COUNT_DISTINCT' &&
      !doc(reloadedDistinct).construction.steps.some((step) => step.operation.kind === 'RELATED_SOURCE') &&
      JSON.stringify(doc(reloadedDistinct).construction) === JSON.stringify(distinctDocument.construction),
    { outputId, construction: doc(reloadedDistinct).construction, rows: distinctRows,
      draftVersion: reloadedDistinct.draftVersion, draftDigest: reloadedDistinct.draftDigest });

  // Add the related field only after the upstream Group edit has been saved and reloaded.
  const beforeFieldAdd = structuredClone(builder);
  const observationNodeIds = new Set(beforeFieldAdd.catalog.nodes
    .filter((node) => node.resourceType === 'Observation').map((node) => node.nodeId));
  const statusCandidates = beforeFieldAdd.catalog.candidates.filter((candidate) =>
    observationNodeIds.has(candidate.nodeId) && candidate.fieldPath === 'status');
  assert(statusCandidates.length > 0, 'The current CDA catalog must expose Observation.status candidates');
  for (const candidate of statusCandidates) {
    assert.equal(candidate.logicalType, 'string');
    assert.equal(candidate.cardinality, 'optional_one');
    assert(candidate.projectionModes.includes('VALUE'));
  }
  const statusCandidateIds = new Set(statusCandidates.map((candidate) => candidate.candidateId));
  const withFieldTyped = [[patient.id, contributorIDs.length, observationStatuses]];
  const relatedRouteLabel = 'Status: Specimen -[subject]-> Patient <-[subject]- Observation';
  const prepareRelatedFieldChooser = async () => {
    const alreadyOpen = await browserEval(page, () => Boolean(document.querySelector('[aria-label="Add columns editor"]')));
    if (!alreadyOpen) {
      await click(page, '[data-testid="construction-action-add-columns"]');
      await click(page, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
    }
    await waitForControl(page, '[data-testid="construction-add-columns-source"]');
    const relatedOpen = await browserEval(page, () => document.querySelector('[aria-label="Related resources"]')?.open === true);
    if (!relatedOpen) await click(page, '[aria-label="Related resources"] summary');
    await click(page, '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
    const rawFieldsOpen = await browserEval(page, () => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true);
    if (!rawFieldsOpen) await click(page, groupEditRawFieldsSummarySelector);
    const fieldSelector = 'input[aria-label="Select Observation.status"]';
    await waitForControl(page, `${fieldSelector}:not(:disabled)`);
    const fieldChecked = await browserEval(page, ({ selector }) => document.querySelector(selector)?.checked === true, { selector: fieldSelector });
    if (!fieldChecked) await click(page, fieldSelector);
    const selectedFieldEvidence = await browserEval(page, ({ selector }) => {
      const selectedPanel = [...document.querySelectorAll('aside')]
        .find((panel) => panel.querySelector('h3')?.innerText.trim() === 'Selected features');
      const heading = selectedPanel?.querySelector('h3');
      return {
        checked: document.querySelector(selector)?.checked === true,
        count: heading?.nextElementSibling?.textContent?.trim(),
        removeActionCount: selectedPanel?.querySelectorAll('button[aria-label^="Remove "]').length ?? 0,
      };
    }, { selector: fieldSelector });
    const uniqueSelectedField = statusCandidates.length === 1 &&
      selectedFieldEvidence.checked === true && selectedFieldEvidence.count === '1' &&
      selectedFieldEvidence.removeActionCount === 1
      ? statusCandidates[0] : undefined;
    activeCatalogChoiceSourceState = uniqueSelectedField ? {
      project, explorer, origin: uiOrigin, outputId,
      snapshotToken: beforeFieldAdd.catalog.snapshotToken,
      draftVersion: beforeFieldAdd.draftVersion, draftDigest: beforeFieldAdd.draftDigest,
      capturedAt: Date.now(),
      sources: [{
        source: { kind: 'FIELD', candidateId: uniqueSelectedField.candidateId },
        evidence: 'unique-current-builder-catalog-candidate-matched-to-checked-Observation.status-and-single-selected-feature',
      }],
    } : undefined;
    await click(page, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
    await waitForControl(page, '[role="dialog"]');
    const otherPaths = await browserEval(page, () => [...document.querySelectorAll('[role="dialog"] summary')]
      .some(item => item.innerText.includes('Other relationship paths')));
    if (otherPaths) await click(page, '[role="dialog"] summary', { includes: 'Other relationship paths' });
    const routeSelector = `[role="dialog"] input[aria-label=${JSON.stringify(relatedRouteLabel)}]`;
    await waitForControl(page, routeSelector);
    await click(page, routeSelector);
    const formSelector = '[role="dialog"] input[aria-label="Status: Keep all matching values"]';
    await waitForControl(page, formSelector);
    await click(page, formSelector);
    await selectOption(page, '[role="dialog"] select[aria-label="Values per grouped row"]', 'ALL');
    const selected = await browserEval(page, ({ routeSelector: route, formSelector: form }) => {
      const dialog = document.querySelector('[role="dialog"]');
      return { route: dialog?.querySelector(route)?.checked, form: dialog?.querySelector(form)?.checked,
        policy: dialog?.querySelector('select[aria-label="Values per grouped row"]')?.value };
    }, { routeSelector, formSelector });
    assert.deepEqual(selected, { route: true, form: true, policy: 'ALL' }, 'Chooser must bind exact source route, ALL matches, and grouped-row ALL policy');
  };
  const proposeRelatedField = async (state, label) => {
    activeBrowserOwner = label;
    const startedAt = Date.now();
    await prepareRelatedFieldChooser();
    const requestStart = report.nativeRequests.length;
    await click(page, '[role="dialog"] button', { name: 'Add 1 column' });
    const proposal = await browserProposal({ requestStart, path: base + '/construction-proposals',
      expectedRows: withFieldTyped, state, label, startedAt });
    const match = relatedSourceProposalCandidate({ path: proposal.request.path, request: proposal.request.body }, {
      resourceType: 'Observation', path: 'status',
    });
    assert(match, `${label} must use the current native RELATED_SOURCE construction proposal`);
    assert(statusCandidateIds.has(match.related.source.candidateId),
      'The native related proposal must bind one of the current Observation.status catalog candidates');
    assert.equal(match.related.form, 'ALL');
    assert.equal(match.related.contributorRule?.policy, 'ALL_MATCHES');
    assert.equal(match.rowValuePolicy, 'ALL');
    assert.equal(match.related.source.logicalType, 'string');
    assert.equal(match.related.source.cardinality, 'optional_one');
    assert.equal(match.related.route.length, 2);
    assert.deepEqual(match.related.route.map(({ fromResourceType, toResourceType }) => [fromResourceType, toResourceType]),
      [['Specimen', 'Patient'], ['Patient', 'Observation']]);
    const candidateGroup = proposal.request.body.candidateConstruction.steps.find((step) => step.id === savedGroupStep.id);
    assert(candidateGroup?.operation.kind === 'GROUP');
    assert.deepEqual(match.step.inputs, [{ kind: 'STEP_OUTPUT', stepId: savedGroupStep.id }],
      'The downstream RELATED_SOURCE must consume the edited Group step output');
    assert(match.related.outputColumnId, 'RELATED_SOURCE must expose a stable output column ID');
    assert(match.step.outputs.some((output) => output.id === match.related.outputColumnId),
      'RELATED_SOURCE step must own its proposed output identity');
    assert(match.related.choiceId, 'The current signed route choice identity must be captured');
    const cells = await browserEval(page, () => [...document.querySelector('[data-testid="construction-proposal-preview-row"]').querySelectorAll('td')]
      .map(cell => ({ text: cell.innerText, raw: cell.title })));
    assert.equal(cells.length, 3);
    assert.deepEqual(cells.slice(0, 2).map((cell) => cell.text), withFieldTyped[0].slice(0, 2).map(String));
    const previewStatuses = JSON.parse(cells[2].raw);
    assert(Array.isArray(previewStatuses) && previewStatuses.every((value) => typeof value === 'string'));
    assert.deepEqual([...previewStatuses].sort(), observationStatuses,
      'Typed automatic preview must equal the scoped raw Observation.status multiset');
    return { ...proposal, match };
  };
  const fieldProposal = await proposeRelatedField(beforeFieldAdd, 'automatic-Observation-status-RELATED_SOURCE-preview-after-Group-edit');
  const statusCandidateId = fieldProposal.match.related.source.candidateId;
  recordLifecycleCheck('correctness',
    'Observation.status RELATED_SOURCE proposal matches the exact raw status multiset and edited Group input',
    fieldProposal.response.preview?.rowCount === 1 &&
      JSON.stringify(fieldProposal.match.step.inputs) === JSON.stringify([{ kind: 'STEP_OUTPUT', stepId: savedGroupStep.id }]) &&
      fieldProposal.match.related.source.path === 'status',
    { candidateId: statusCandidateId, relatedSource: fieldProposal.match.related,
      preview: fieldProposal.response.preview, expectedStatuses: observationStatuses });

  const fieldCancelStartedAt = Date.now();
  activeBrowserOwner = 'cancel-related-column-after-Group-edit';
  registerProposalCancelOwner(fieldProposal.response.proposalId, 'cancel the first downstream RELATED_SOURCE candidate');
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  await mountedRows(distinctRows, 2);
  const afterFieldCancel = await api(base + '/builder');
  assert.equal(afterFieldCancel.draftVersion, beforeFieldAdd.draftVersion);
  assert.equal(afterFieldCancel.draftDigest, beforeFieldAdd.draftDigest);
  assert.deepEqual(afterFieldCancel.workspace, beforeFieldAdd.workspace, 'Related-column Cancel must preserve exact Group edit and source bindings');
  recordAction('cancel-related-column-after-Group-edit', fieldCancelStartedAt);
  recordLifecycleCheck('persistence',
    'Related-column Cancel preserves exact COUNT_DISTINCT Group workspace and draft CAS',
    afterFieldCancel.draftVersion === beforeFieldAdd.draftVersion &&
      afterFieldCancel.draftDigest === beforeFieldAdd.draftDigest &&
      JSON.stringify(afterFieldCancel.workspace) === JSON.stringify(beforeFieldAdd.workspace),
    { draftVersion: afterFieldCancel.draftVersion, draftDigest: afterFieldCancel.draftDigest,
      workspace: afterFieldCancel.workspace });

  // Cancellation clears the proposed candidate. Reopen the same native chooser
  // and capture a new proposal before Apply so receipt and draft bindings are fresh.
  const applyProposal = await proposeRelatedField(afterFieldCancel, 'reopened-Observation-status-RELATED_SOURCE-preview-before-Apply');
  assert.notEqual(applyProposal.response.proposalId, fieldProposal.response.proposalId,
    'The reopened chooser must produce a fresh proposal receipt after Cancel');
  assert.equal(applyProposal.match.related.choiceId, fieldProposal.match.related.choiceId,
    'The reopened chooser must retain the same signed source route choice');
  assert.equal(applyProposal.match.related.source.candidateId, statusCandidateId,
    'The reopened chooser must retain the exact selected catalog candidate');
  const fieldApplyStartedAt = Date.now();
  activeBrowserOwner = 'apply-related-column-after-Group-edit';
  const applyRequestStart = report.nativeRequests.length;
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  await mountedRows(withFieldTyped, 3);
  await waitNetwork();
  const choiceApply = findNative(applyRequestStart, base + '/commands', (entry) => entry.body?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_PROPOSAL'));
  assertDraftRequest(choiceApply, afterFieldCancel, 'apply related Observation.status column');
  assert(choiceApply.body.commands.some((item) => item.proposalId === applyProposal.response.proposalId));
  builder = await api(base + '/builder');
  assert.equal(choiceApply.response.draftVersion, afterFieldCancel.draftVersion + 1);
  assert.equal(choiceApply.response.draftDigest, applyProposal.response.candidateWorkspaceDigest);
  assert.equal(builder.draftVersion, applyProposal.response.draftVersion + 1);
  assert.equal(builder.draftDigest, applyProposal.response.candidateWorkspaceDigest);
  recordAction('apply-related-column-after-Group-edit', fieldApplyStartedAt,
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
  const withFieldDocument = structuredClone(doc(builder));
  const savedGroupAfterRelated = withFieldDocument.construction.steps.find((step) => step.id === savedGroupStep.id);
  assert.deepEqual(savedGroupAfterRelated, distinctDocument.construction.steps.find((step) => step.id === savedGroupStep.id),
    'Adding the related column must retain the exact previously edited Group step and output identities');
  assertSourceBinding(builder, selectedPopulationRoute);
  const relatedStep = withFieldDocument.construction.steps.find((step) => step.operation.kind === 'RELATED_SOURCE'
    && step.operation.relatedSource?.source?.candidateId === statusCandidateId);
  assert(relatedStep, 'Saved construction must bind Observation.status through a native RELATED_SOURCE step');
  assert.equal(relatedStep.id, applyProposal.match.step.id, 'Applying the proposal must preserve the stable related step identity');
  assert.deepEqual(relatedStep.inputs, [{ kind: 'STEP_OUTPUT', stepId: savedGroupStep.id }]);
  assert.deepEqual(relatedStep.operation.relatedSource, applyProposal.match.related);
  assert.equal(relatedStep.operation.relatedSource.rowValuePolicy ?? 'ALL', 'ALL');
  const relatedOutput = relatedStep.outputs.find((output) => output.id === relatedStep.operation.relatedSource.outputColumnId);
  assert(relatedOutput, 'Saved RELATED_SOURCE must own its stable output column identity');
  const relatedColumn = { column: relatedOutput.name, columnId: relatedOutput.id, label: relatedOutput.label, logicalType: relatedOutput.type };
  assert.equal(relatedOutput.type, 'string');
  const proposalOutput = applyProposal.response.preview.columns.find((column) => column.column === relatedColumn.column);
  assert(proposalOutput, 'The accepted preview must bind the exact saved RELATED_SOURCE output identity');
  assert.equal(proposalOutput.label, relatedColumn.label);
  assert.equal(withFieldDocument.population.selectionRevisionId, selection.id);
  assert.deepEqual(withFieldDocument.population.route, selectedPopulationRoute);
  recordLifecycleCheck('persistence',
    'Applying Observation.status RELATED_SOURCE retains the edited Group and exact source binding',
    JSON.stringify(savedGroupAfterRelated) === JSON.stringify(distinctDocument.construction.steps.find((step) => step.id === savedGroupStep.id)) &&
      relatedStep.id === applyProposal.match.step.id &&
      relatedStep.operation.relatedSource.source.candidateId === statusCandidateId &&
      JSON.stringify(relatedStep.inputs) === JSON.stringify([{ kind: 'STEP_OUTPUT', stepId: savedGroupStep.id }]),
    { groupStep: savedGroupAfterRelated, relatedStep, selectionRevisionId: selection.id,
      route: withFieldDocument.population.route });

  const reloadedField = await open(withFieldTyped, 3, 'reload-related-column-after-Group-edit');
  assert.deepEqual(doc(reloadedField), withFieldDocument, 'Reload must preserve the exact field occurrence, ALL policy, and Group edit');
  recordLifecycleCheck('persistence',
    'Reload preserves the exact Group and related Observation.status field construction',
    JSON.stringify(doc(reloadedField)) === JSON.stringify(withFieldDocument),
    { construction: doc(reloadedField).construction, columns: doc(reloadedField).columns });

  // Round-trip the upstream aggregate with the related source present, then remove
  // its owning native step through an automatic proposal and prove restoration.
  const beforeRestoreEdit = structuredClone(builder);
  const countRowsProposal = await editGroupAggregate('COUNT_ROWS', withFieldTyped, builder,
    'edit-Group-back-to-count-rows-with-related-column');
  const restoreEditCancelStartedAt = Date.now();
  activeBrowserOwner = 'cancel-Group-round-trip-edit';
  registerProposalCancelOwner(countRowsProposal.response.proposalId, 'cancel the Group round-trip candidate while RELATED_SOURCE is saved');
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { hidden: true });
  const afterRestoreCancel = await api(base + '/builder');
  assert.deepEqual(afterRestoreCancel.workspace, beforeRestoreEdit.workspace);
  assert.equal(afterRestoreCancel.draftDigest, beforeRestoreEdit.draftDigest);
  recordAction('cancel-Group-round-trip-edit', restoreEditCancelStartedAt);
  recordLifecycleCheck('persistence',
    'Group round-trip Cancel preserves downstream RELATED_SOURCE and exact saved draft',
    afterRestoreCancel.draftVersion === beforeRestoreEdit.draftVersion &&
      afterRestoreCancel.draftDigest === beforeRestoreEdit.draftDigest &&
      JSON.stringify(afterRestoreCancel.workspace) === JSON.stringify(beforeRestoreEdit.workspace),
    { draftVersion: afterRestoreCancel.draftVersion, draftDigest: afterRestoreCancel.draftDigest,
      workspace: afterRestoreCancel.workspace });

  const restoreProposal = await editGroupAggregate('COUNT_ROWS', withFieldTyped, afterRestoreCancel,
    'reapply-Group-count-rows-with-related-column');
  builder = await applyGroupProposal(restoreProposal.response, afterRestoreCancel, withFieldTyped,
    'apply-Group-round-trip-with-related-column');
  assert.deepEqual(doc(builder).columns, withFieldDocument.columns,
    'Editing Group must preserve the exact downstream field occurrence and ALL source binding');
  assert.equal(doc(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_SOURCE')?.id, relatedStep.id,
    'Editing Group must preserve the downstream RELATED_SOURCE step identity');
  assertSourceBinding(builder, selectedPopulationRoute);
  const countRowsWithFieldDocument = structuredClone(doc(builder));
  const reloadedCountRows = await open(withFieldTyped, 3, 'reload-Group-round-trip-with-related-column');
  assert.deepEqual(doc(reloadedCountRows), countRowsWithFieldDocument);
  assert.equal(doc(reloadedCountRows).construction.steps.find((step) => step.operation.kind === 'GROUP').operation.group.aggregates[0].operation, 'COUNT_ROWS');
  recordLifecycleCheck('persistence',
    'Group round-trip Apply and reload preserve exact downstream related field bindings',
    JSON.stringify(doc(reloadedCountRows)) === JSON.stringify(countRowsWithFieldDocument) &&
      doc(reloadedCountRows).construction.steps.find((step) => step.operation.kind === 'GROUP')
        .operation.group.aggregates[0].operation === 'COUNT_ROWS',
    { construction: doc(reloadedCountRows).construction, columns: doc(reloadedCountRows).columns });

  const proposeRelatedStepRemoval = async (state, label) => {
    activeBrowserOwner = label;
    const step = doc(state).construction.steps.find((candidate) => candidate.id === relatedStep.id);
    assert(step?.operation.kind === 'RELATED_SOURCE');
    await click(page, `[data-testid="construction-history-step-${step.id}"]`);
    const removeSelector = `[data-testid="construction-remove-step-${step.id}"]:not(:disabled)`;
    await waitForControl(page, removeSelector, { timeout: 5000 });
    const requestStart = report.nativeRequests.length;
    const startedAt = Date.now();
    await click(page, `[data-testid="construction-remove-step-${step.id}"]`);
    const proposal = await browserProposal({ requestStart, path: base + '/construction-proposals',
      expectedRows: groupedRows, state, label, startedAt });
    assert.deepEqual(proposal.request.body.removeStepIds, [relatedStep.id],
      'Removal must target only the newly added downstream RELATED_SOURCE step');
    assert.deepEqual(proposal.response.candidateConstruction.steps, groupDocumentBaseline.construction.steps,
      'The removal candidate must restore the exact original Group construction');
    return proposal;
  };
  const beforeRemoveCancel = structuredClone(builder);
  const removePreview = await proposeRelatedStepRemoval(beforeRemoveCancel, 'automatic-remove-RELATED_SOURCE-preview-cancel-target');
  const removeCancelStartedAt = Date.now();
  activeBrowserOwner = 'cancel-remove-RELATED_SOURCE';
  registerProposalCancelOwner(removePreview.response.proposalId, 'cancel the downstream RELATED_SOURCE removal candidate');
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  const afterRemoveCancel = await api(base + '/builder');
  assert.equal(afterRemoveCancel.draftVersion, beforeRemoveCancel.draftVersion);
  assert.equal(afterRemoveCancel.draftDigest, beforeRemoveCancel.draftDigest);
  assert.deepEqual(afterRemoveCancel.workspace, beforeRemoveCancel.workspace,
    'Canceling related-source removal must preserve its exact step, Group, and source bindings');
  recordAction('cancel-remove-RELATED_SOURCE', removeCancelStartedAt);
  recordLifecycleCheck('persistence',
    'Related-source removal Cancel preserves the exact saved Group and related field workspace',
    afterRemoveCancel.draftVersion === beforeRemoveCancel.draftVersion &&
      afterRemoveCancel.draftDigest === beforeRemoveCancel.draftDigest &&
      JSON.stringify(afterRemoveCancel.workspace) === JSON.stringify(beforeRemoveCancel.workspace),
    { draftVersion: afterRemoveCancel.draftVersion, draftDigest: afterRemoveCancel.draftDigest,
      workspace: afterRemoveCancel.workspace });
  const removeCancelledAt = Date.now();

  const appliedRemovePreview = await proposeRelatedStepRemoval(afterRemoveCancel,
    'reopened-remove-RELATED_SOURCE-preview-before-Apply');
  // Proposal IDs are content identities; unchanged candidate content may yield
  // the same ID. Require a new completed request after Cancel and exact binding.
  assertReopenedProposalAfterCancel({
    previous: removePreview,
    cancelledAt: removeCancelledAt,
    current: appliedRemovePreview,
    state: afterRemoveCancel,
    outputId,
    removeStepIds: [relatedStep.id],
  });
  builder = await applyGroupProposal(appliedRemovePreview.response, afterRemoveCancel, groupedRows,
    'apply-remove-RELATED_SOURCE-to-restore-Group');
  assert.deepEqual(doc(builder).columns, groupDocumentBaseline.columns,
    'Removing the related source must restore the original authored column bindings');
  assert.deepEqual(doc(builder).construction, groupDocumentBaseline.construction,
    'Removing the related source must restore the original Group construction exactly');
  assertSourceBinding(builder, selectedPopulationRoute);
  assert.equal(doc(builder).construction.steps.some((step) => step.operation.kind === 'RELATED_SOURCE'), false,
    'The removed related source must no longer be present in the saved construction');
  const finalReload = await open(groupedRows, 2, 'reload-final-Group-source-restoration');
  assert.deepEqual(finalReload.workspace, groupBaseline.workspace,
    'Final reload must restore the exact Group workspace, source membership, output identity, and column bindings');
  recordLifecycleCheck('persistence',
    'Applying related-source removal and reloading restores the exact original Group workspace',
    JSON.stringify(finalReload.workspace) === JSON.stringify(groupBaseline.workspace) &&
      JSON.stringify(doc(finalReload).construction) === JSON.stringify(groupDocumentBaseline.construction),
    { outputId, workspace: finalReload.workspace, construction: doc(finalReload).construction });

  await drainNativeRequests();
  const abortedReads = report.nativeRequests.filter((entry) => entry.cancelled);
  const cancellationCoverage = abortedReads.length === 0
    ? 'not-exercised-no-cancellation-observed'
    : 'observed-and-classified';
  recordLifecycleCheck('correctness',
    'Strict drain classifies every observed cancellation with exact current scope, CAS, trusted owner action, and detached DOM proof; zero observed cancellations are marked not exercised',
    abortedReads.every((entry) => entry.expectedOwnerCancellation?.expected === true) &&
      report.nativeRequestDrain.unclassifiedAborts.length === 0,
    { abortedReadCount: abortedReads.length, cancellationCoverage, classified: report.expectedOwnerCancellations,
      unclassified: report.nativeRequestDrain.unclassifiedAborts });
  assert.deepEqual(report.browserTransportViolations, [], 'Browser API requests and responses must remain on the scoped local UI proxy');
  const nativeTransportOutcomesBound = nativeRequestsHaveOwnedTransportOutcomes(report.nativeRequests, uiOrigin);
  assert(nativeTransportOutcomesBound,
    'Every captured native request must have a response bound to the local no-auth UI proxy or an exact owned pre-response cancellation');
  assert(!report.nativeRequests.some((entry) => entry.path.includes(protectedExplorer)), 'Protected shared Explorer must remain untouched');
  assert.deepEqual(report.errors, [], 'The native lifecycle must have no HTTP, runtime, or console errors');
  recordLifecycleCheck('correctness',
    'No unexpected browser, authoring, transport, or native HTTP errors occur',
    report.errors.length === 0 && report.browserTransportViolations.length === 0 && nativeTransportOutcomesBound,
    { errors: report.errors, browserTransportViolations: report.browserTransportViolations,
      nativeRequestCount: report.nativeRequests.length });
  report.rawOracleQueries = oracleQueries;
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.failureUI = await cda.inspect(() => document.body.innerText).catch(String);
  throw error;
} finally {
  if (!nativeDrainAttempted) {
    try { await drainNativeRequests(); }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'failed';
      report.error = [report.error, `Strict native request drain failed: ${String(error.stack ?? error)}`].filter(Boolean).join('\n');
    }
  }
  if (nativeCdpSession) {
    try { await nativeCdpSession.detach(); }
    catch (error) {
      report.status = 'failed';
      report.error = [report.error, `Native CDP session detach failed: ${String(error.stack ?? error)}`].filter(Boolean).join('\n');
    }
  }
  report.finished = new Date().toISOString();
  await cda.attachReport('group-edit-before-related-column', report);
}
if (report.status !== 'passed') throw new Error(report.error ?? `Workflow ended with ${report.status}`);
return report;
}
