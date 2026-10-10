import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { recordCheck, registerValidatedObsoleteNetworkRead } from '../helpers/report.mjs';

const unique = async locator => {
  const count = await locator.count();
  assert.equal(count, 1, `Expected one native UI target for ${locator.toString()}, found ${count}`);
  return locator;
};

const requestBody = request => {
  try { return request.postDataJSON(); } catch { return undefined; }
};

const commandHas = (request, type) => requestBody(request)?.commands?.some(command => command.type === type) === true;

const waitForPreview = async (page, expectedRows, expectedColumns) => {
  await page.waitForFunction(({ expectedRows, expectedColumns }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === String(expectedRows + 1) &&
      table?.getAttribute('aria-colcount') === String(expectedColumns) &&
      !document.body.innerText.includes('Loading your table…') &&
      !document.body.innerText.includes('Preview failed:');
  }, { expectedRows, expectedColumns });
};

const previewSnapshot = async page => page.locator('[data-testid="preview-table-scroll"] [role="table"]').evaluate(table => ({
  rowCount: Number(table.getAttribute('aria-rowcount')) - 1,
  columnCount: Number(table.getAttribute('aria-colcount')),
  headers: [...table.querySelectorAll('[role="columnheader"]')].map(header => header.innerText.trim()),
  rows: [...table.querySelectorAll('[role="row"]')].slice(1).map(row =>
    [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
}));

const baseHeader = label => label.replace(/\s+\([^)]*\)$/, '').trim();

const normalizePreviewHeader = label => baseHeader(label).normalize('NFKC').replace(/\s+/g, ' ').trim().toLocaleLowerCase('en-US');

export const previewHeaderMatches = (actual, expected) =>
  normalizePreviewHeader(actual) === normalizePreviewHeader(expected);

const rowsByObservationID = (snapshot, expectedLabel) => {
  const idIndex = snapshot.headers.findIndex(label => previewHeaderMatches(label, 'Observation ID'));
  const codedIndex = snapshot.headers.findIndex(label => previewHeaderMatches(label, expectedLabel));
  assert.notEqual(idIndex, -1, `The preview has no Observation ID column: ${JSON.stringify(snapshot.headers)}`);
  assert.notEqual(codedIndex, -1, `The preview has no ${expectedLabel} column: ${JSON.stringify(snapshot.headers)}`);
  assert(snapshot.rows.every(row => row.length === snapshot.headers.length),
    `The preview has an incomplete row: ${JSON.stringify(snapshot)}`);
  return snapshot.rows.map(row => ({
    id: row[idIndex],
    value: row[codedIndex] === '—' ? null : row[codedIndex],
  })).sort((left, right) => left.id.localeCompare(right.id));
};

const recordAt = (records, id) => records.find(record => record.id === id);

const routeOwner = (url, endpoint) => {
  try {
    const parsed = new URL(url);
    const match = parsed.pathname.match(/^\/api\/v1\/projects\/([^/]+)\/explorers\/([^/]+)\/authoring\/v2\/(.+)$/);
    if (!match || match[3] !== endpoint) return undefined;
    return { origin: parsed.origin, project: decodeURIComponent(match[1]), explorerId: decodeURIComponent(match[2]), route: parsed.origin + parsed.pathname };
  } catch {
    return undefined;
  }
};

export const normalizeFrameSourceRequest = (body, headerRequestId) => {
  if (!body || typeof body !== 'object' ||
      (body.query !== undefined && typeof body.query !== 'string')) return undefined;
  const requestId = body.requestId ?? body.requestID ?? headerRequestId;
  const requestIdSource = typeof body.requestId === 'string' || typeof body.requestID === 'string'
    ? 'body' : typeof headerRequestId === 'string' ? 'header' : undefined;
  if (typeof requestId !== 'string' || !requestId || !requestIdSource ||
      typeof body.outputId !== 'string' || !body.outputId ||
      typeof body.snapshotToken !== 'string' || !body.snapshotToken) return undefined;
  return {
    requestId,
    requestIdSource,
    query: typeof body.query === 'string' ? body.query : '',
    queryFieldPresent: Object.hasOwn(body, 'query') && typeof body.query === 'string',
    outputId: body.outputId,
    snapshotToken: body.snapshotToken,
  };
};

export const frameSourceFailureCaptureRecord = (capture, reportRecord) => {
  const owner = routeOwner(capture?.url, 'frame-source-options');
  const body = capture?.body;
  if (capture?.kind !== 'frame-source-options' || capture.method !== 'POST' || !owner ||
      owner.project !== capture.project || owner.explorerId !== capture.explorerId ||
      typeof body?.requestId !== 'string' || !body.requestId || typeof body.query !== 'string' ||
      !['body', 'header'].includes(body.requestIdSource) || typeof body.queryFieldPresent !== 'boolean' ||
      typeof body.outputId !== 'string' || !body.outputId ||
      typeof body.snapshotToken !== 'string' || !body.snapshotToken ||
      !Number.isFinite(capture.startedAtMs) || !Number.isFinite(capture.failedAtMs) ||
      !Number.isFinite(capture.durationMs)) return undefined;
  return {
    playwrightRequestId: typeof reportRecord?.playwrightRequestId === 'string'
      ? reportRecord.playwrightRequestId : null,
    method: capture.method,
    route: owner.route,
    project: owner.project,
    explorerId: owner.explorerId,
    requestId: body.requestId,
    requestIdSource: body.requestIdSource,
    query: body.query,
    queryFieldPresent: body.queryFieldPresent,
    outputId: body.outputId,
    snapshotToken: body.snapshotToken,
    errorText: typeof capture.errorText === 'string' ? capture.errorText : null,
    requestStartedMs: Number.isFinite(reportRecord?.requestTimeline?.requestStartedMs)
      ? reportRecord.requestTimeline.requestStartedMs : null,
    failedAtMs: Number.isFinite(reportRecord?.requestTimeline?.failedAtMs)
      ? reportRecord.requestTimeline.failedAtMs : null,
    localRequestStartedMs: capture.startedAtMs,
    localFailedAtMs: capture.failedAtMs,
    durationMs: capture.durationMs,
  };
};

const sameJson = (left, right) => JSON.stringify(left) === JSON.stringify(right);
const isNonemptyIDArray = value => Array.isArray(value) && value.length > 0 &&
  value.every(id => typeof id === 'string' && id.length > 0);
const hasMatchingHeaders = (evidence, label, expectedPresent) => {
  const headers = evidence?.headers;
  if (!Array.isArray(headers) || headers.length === 0 ||
      !headers.every(header => typeof header === 'string' && header.trim().length > 0)) return false;
  const found = headers.some(header => previewHeaderMatches(header, label));
  return expectedPresent ? found : !found;
};
const hasEqualNonemptyHeightRows = (expected, actual) =>
  Array.isArray(expected) && expected.length > 0 && Array.isArray(actual) &&
  actual.length === expected.length && expected.every(row =>
    typeof row?.id === 'string' && row.id.length > 0 &&
    (row.value === null || typeof row.value === 'string')) && sameJson(expected, actual);
const hasEqualNonemptyIDs = (expected, actual) =>
  isNonemptyIDArray(expected) && isNonemptyIDArray(actual) &&
  actual.length === expected.length && sameJson(expected, actual);
const hasCompleteFixtureOracle = report => {
  const oracle = report.target?.fixtureOracle;
  return oracle?.observationCount === 6 && isNonemptyIDArray(oracle.observationIDs) &&
    oracle.observationIDs.length === oracle.observationCount &&
    new Set(oracle.observationIDs).size === oracle.observationIDs.length &&
    Array.isArray(oracle.heightRows) && oracle.heightRows.length === oracle.observationCount &&
    oracle.heightRows.every(row => typeof row?.id === 'string' && row.id.length > 0 &&
      (row.value === null || typeof row.value === 'string')) &&
    sameJson(oracle.heightRows.map(row => row.id), oracle.observationIDs);
};
const hasExactFixtureHeightRows = (report, expected, actual) =>
  hasCompleteFixtureOracle(report) && hasEqualNonemptyHeightRows(expected, actual) &&
  sameJson(expected, report.target.fixtureOracle.heightRows) &&
  sameJson(actual, report.target.fixtureOracle.heightRows);
const hasExactFixtureIDs = (report, expected, actual) =>
  hasCompleteFixtureOracle(report) && hasEqualNonemptyIDs(expected, actual) &&
  sameJson(expected, report.target.fixtureOracle.observationIDs) &&
  sameJson(actual, report.target.fixtureOracle.observationIDs);

const hasPassedAction = (report, proof) => {
  const action = recordAt(report.actions ?? [], proof?.action?.id);
  return Boolean(action && action.status === 'passed' && action.label === proof.action.label &&
    action.startedAtMs === proof.action.startedAtMs && action.endedAtMs === proof.action.endedAtMs);
};

export const appendCapabilityCommandInvalidationProof = (report, {
  networkFailure, commandType, editedLabel, mutation, action, assertion, reloadAssertion,
}) => {
  const proof = {
    kind: 'capability-command-invalidation',
    failedRequest: {
      playwrightRequestId: networkFailure.playwrightRequestId,
      requestStartedAtMs: networkFailure.requestTimeline?.requestStartedMs,
      failedAtMs: networkFailure.requestTimeline?.failedAtMs,
      binding: networkFailure.binding,
    },
    mutation: {
      commandType,
      editedLabel,
      request: mutation.request,
      response: mutation.response,
    },
    action,
    assertion: {
      name: assertion.name,
      ...(reloadAssertion ? { reloadName: reloadAssertion.name } : {}),
    },
  };
  report.expectedObsolete ??= [];
  report.expectedObsolete.push(proof);
  return proof;
};

export const appendFrameSearchInvalidationProof = (report, {
  networkFailure, failedRequestCapture, replacementRequestCapture, replacementResponse,
  action, selection, assertion,
}) => {
  const localToReportOffsetMs = networkFailure.requestTimeline?.requestStartedMs - failedRequestCapture.startedAtMs;
  const proof = {
    kind: 'frame-source-search',
    failedRequest: {
      playwrightRequestId: networkFailure.playwrightRequestId,
      requestId: failedRequestCapture.body.requestId,
      url: failedRequestCapture.url,
      localStartedAtMs: failedRequestCapture.startedAtMs,
      requestStartedMs: networkFailure.requestTimeline?.requestStartedMs,
      failedAtMs: networkFailure.requestTimeline?.failedAtMs,
      request: {
        method: failedRequestCapture.method,
        query: failedRequestCapture.body.query,
        outputId: failedRequestCapture.body.outputId,
        snapshotToken: failedRequestCapture.body.snapshotToken,
      },
    },
    replacement: {
      request: {
        url: replacementRequestCapture.url,
        project: replacementRequestCapture.project,
        explorerId: replacementRequestCapture.explorerId,
        body: replacementRequestCapture.body,
        localStartedAtMs: replacementRequestCapture.startedAtMs,
        requestStartedMs: replacementRequestCapture.startedAtMs + localToReportOffsetMs,
      },
      response: replacementResponse,
    },
    clockAlignment: { localToReportOffsetMs },
    action,
    selection,
    assertion: { name: assertion.name },
  };
  report.expectedObsolete ??= [];
  report.expectedObsolete.push(proof);
  return proof;
};

const hasPassedAssertion = (report, proof) => {
  const assertion = (report.assertions ?? []).find(item => item.name === proof?.assertion?.name && item.status === 'passed');
  if (!assertion) return false;
  const evidence = assertion.evidence ?? {};
  if (proof.mutation?.commandType === 'UPDATE_COLUMN') {
    return proof.assertion.name === 'edited Height column and exact values survive Builder reload' &&
      evidence.editedLabel === proof.mutation.editedLabel &&
      hasExactFixtureHeightRows(report, evidence.expectedHeightRows, evidence.actualHeightRows) &&
      hasMatchingHeaders(evidence, proof.mutation.editedLabel, true);
  }
  if (proof.mutation?.commandType === 'REMOVE_COLUMN') {
    const persistedAssertion = (report.assertions ?? []).find(item =>
      item.name === proof.assertion?.reloadName && item.status === 'passed');
    return proof.assertion.name === 'native Remove column restores the six Observation ID rows' &&
      hasExactFixtureIDs(report, evidence.expectedIDs, evidence.removedIDs) &&
      hasMatchingHeaders(evidence, proof.mutation.editedLabel, false) &&
      persistedAssertion?.name === 'removed coded column stays absent after Builder reload' &&
      hasExactFixtureIDs(report, persistedAssertion.evidence?.expectedIDs, persistedAssertion.evidence?.finalIDs) &&
      hasMatchingHeaders(persistedAssertion.evidence, proof.mutation.editedLabel, false);
  }
  return false;
};

const hasExactActionBoundCapabilityInvalidation = (report, item, proof) => {
  const failed = proof?.failedRequest;
  const mutation = proof?.mutation;
  const binding = failed?.binding;
  const failedOwner = routeOwner(binding?.route, 'construction-capabilities');
  const itemOwner = routeOwner(item.rawURL ?? item.url, 'construction-capabilities');
  const mutationOwner = routeOwner(mutation?.request?.url, 'commands');
  const command = mutation?.request?.body?.commands;
  const receipt = mutation?.response?.body;
  const targetCommand = Array.isArray(command) && command.length === 1 ? command[0] : undefined;
  const action = recordAt(report.actions ?? [], proof?.action?.id);
  const sharedIdentityMatches = (report.network ?? []).filter(record =>
    record.kind === 'network' && record.playwrightRequestId === failed?.playwrightRequestId);
  const requestTime = item.requestTimeline?.requestStartedMs;
  const failureTime = item.requestTimeline?.failedAtMs;
  const comparedTimes = [
    requestTime,
    failureTime,
    failed?.requestStartedAtMs,
    failed?.failedAtMs,
    action?.startedAtMs,
    action?.endedAtMs,
    action?.startedAtEpochMs,
    action?.finishedAtEpochMs,
    proof.action?.startedAtMs,
    proof.action?.endedAtMs,
    mutation?.response?.completedAtEpochMs,
  ];
  if (item.kind !== 'network' || item.method !== 'POST' || item.errorText !== 'net::ERR_ABORTED' ||
      itemOwner?.route !== binding?.route || failedOwner?.project !== itemOwner?.project ||
      failedOwner?.explorerId !== itemOwner?.explorerId ||
      typeof failed?.playwrightRequestId !== 'string' || !failed.playwrightRequestId ||
      item.playwrightRequestId !== failed.playwrightRequestId || sharedIdentityMatches.length !== 1 ||
      !sameJson(item.binding, binding) || !Number.isFinite(requestTime) || !Number.isFinite(failureTime) ||
      !comparedTimes.every(Number.isFinite) ||
      Math.abs(requestTime - failed.requestStartedAtMs) > 2 || Math.abs(failureTime - failed.failedAtMs) > 2 ||
      mutation?.commandType !== targetCommand?.type || !['UPDATE_COLUMN', 'REMOVE_COLUMN'].includes(targetCommand?.type) ||
      mutationOwner?.project !== failedOwner?.project || mutationOwner?.explorerId !== failedOwner?.explorerId ||
      mutationOwner?.origin !== failedOwner?.origin ||
      mutation.request?.project !== failedOwner?.project || mutation.request?.explorerId !== failedOwner?.explorerId ||
      targetCommand?.outputId !== binding.outputId ||
      (targetCommand?.type === 'UPDATE_COLUMN' && targetCommand.columnValue?.label !== mutation.editedLabel) ||
      (targetCommand?.type === 'REMOVE_COLUMN' && typeof targetCommand.column !== 'string') ||
      mutation.request.body?.snapshotToken !== binding.snapshotToken ||
      mutation.request.body?.expectedDraftVersion !== binding.draftVersion ||
      mutation.request.body?.expectedDraftDigest !== binding.draftDigest ||
      !Number.isInteger(mutation.response?.status) || mutation.response.status < 200 || mutation.response.status >= 300 ||
      receipt?.commandId !== mutation.request.body?.commandId ||
      !Number.isInteger(receipt?.draftVersion) || receipt.draftVersion <= binding.draftVersion ||
      typeof receipt?.draftDigest !== 'string' || receipt.draftDigest === binding.draftDigest ||
      !action || action.status !== 'passed' || action.label !== proof.action.label ||
      !['save edited Height column label', 'remove saved Height coded column'].includes(action.label) ||
      requestTime >= action.startedAtMs || action.endedAtMs > failureTime ||
      !Number.isFinite(action.startedAtEpochMs) || !Number.isFinite(action.finishedAtEpochMs) ||
      !Number.isFinite(mutation.response?.completedAtEpochMs) ||
      mutation.response.completedAtEpochMs < action.startedAtEpochMs ||
      mutation.response.completedAtEpochMs > action.finishedAtEpochMs ||
      item.requestTimeline?.failedAtMs < action.endedAtMs ||
      (targetCommand.type === 'UPDATE_COLUMN' && action.label !== 'save edited Height column label') ||
      (targetCommand.type === 'REMOVE_COLUMN' && action.label !== 'remove saved Height coded column') ||
      !hasPassedAction(report, proof) || !hasPassedAssertion(report, proof)) return false;
  return true;
};

const hasExactFrameSearchInvalidation = (report, item, proof) => {
  const failed = proof?.failedRequest;
  const replacement = proof?.replacement;
  const failedOwner = routeOwner(failed?.url, 'frame-source-options');
  const reportOwner = routeOwner(item.rawURL ?? item.url, 'frame-source-options');
  const replacementOwner = routeOwner(replacement?.request?.url, 'frame-source-options');
  const action = recordAt(report.actions ?? [], proof?.action?.id);
  const sharedIdentityMatches = (report.network ?? []).filter(record =>
    record.kind === 'network' && record.playwrightRequestId === failed?.playwrightRequestId);
  const selection = proof?.selection;
  const source = selection?.source;
  const responseSource = replacement?.response?.body?.sources?.find(entry => entry.choiceId === selection?.choiceId);
  const setSource = selection?.setFrameSource;
  const setSourceOwner = routeOwner(setSource?.request?.url, 'commands');
  const setSourceCommand = setSource?.request?.body?.commands?.find(command => command.type === 'SET_FRAME_SOURCE');
  const assertion = (report.assertions ?? []).find(entry => entry.name === proof?.assertion?.name && entry.status === 'passed');
  const failedRequest = failed?.request;
  const replacementRequest = replacement?.request;
  const replacementBody = replacement?.response?.body;
  const setSourceRequest = setSource?.request;
  const setSourceResponse = setSource?.response;
  const reportFailure = item.requestTimeline;
  const localToReportOffsetMs = proof.clockAlignment?.localToReportOffsetMs;
  const actionTimes = [action?.startedAtMs, action?.endedAtMs];
  const requestTimes = [
    failed?.localStartedAtMs,
    failed?.requestStartedMs,
    failed?.failedAtMs,
    replacementRequest?.localStartedAtMs,
    replacementRequest?.requestStartedMs,
    localToReportOffsetMs,
    reportFailure?.requestStartedMs,
    reportFailure?.failedAtMs,
    ...actionTimes,
  ];

  return Boolean(
    item.kind === 'network' && item.method === 'POST' && item.errorText === 'net::ERR_ABORTED' &&
    reportOwner?.route === failed?.url && item.playwrightRequestId === failed?.playwrightRequestId &&
    typeof failed?.playwrightRequestId === 'string' && failed.playwrightRequestId && sharedIdentityMatches.length === 1 &&
    item.requestDetails?.requestId === failed?.requestId &&
    item.requestDetails?.outputId === failedRequest?.outputId &&
    item.triggerAction === 'browse coded source values' &&
    item.requestTimeline?.action?.label === 'browse coded source values' &&
    failedRequest?.method === 'POST' && failedRequest.query === '' &&
    failedOwner?.project === replacementRequest?.project && failedOwner?.explorerId === replacementRequest?.explorerId &&
    failedRequest.outputId === replacementRequest?.body?.outputId &&
    failedRequest.snapshotToken === replacementRequest?.body?.snapshotToken &&
    replacementOwner?.origin === failedOwner?.origin &&
    replacementOwner?.project === failedOwner?.project && replacementOwner?.explorerId === failedOwner?.explorerId &&
    replacementRequest?.body?.query === 'Observation' && Number.isInteger(replacement?.response?.status) &&
    replacement.response.status >= 200 && replacement.response.status < 300 &&
    replacement.response.finished === true && replacementBody?.query === 'Observation' &&
    replacementBody.outputId === failedRequest.outputId && replacementBody.snapshotToken === failedRequest.snapshotToken &&
    requestTimes.every(Number.isFinite) &&
    Math.abs(reportFailure.requestStartedMs - failed.requestStartedMs) <= 2 &&
    Math.abs(reportFailure.failedAtMs - failed.failedAtMs) <= 2 &&
    Math.abs(failed.requestStartedMs - failed.localStartedAtMs - localToReportOffsetMs) <= 2 &&
    Math.abs(replacementRequest.requestStartedMs - replacementRequest.localStartedAtMs - localToReportOffsetMs) <= 2 &&
    replacementRequest.requestStartedMs > failed.requestStartedMs &&
    proof.action?.label === 'search native Observation framing choices' && action?.status === 'passed' &&
    action.label === proof.action.label && hasPassedAction(report, proof) &&
    reportFailure.requestStartedMs < action.startedAtMs &&
    reportFailure.failedAtMs >= action.startedAtMs &&
    replacementRequest.requestStartedMs >= action.startedAtMs &&
    replacementRequest.requestStartedMs <= action.endedAtMs &&
    selection?.choiceId === source?.choiceId && source?.resourceType === 'Observation' &&
    Array.isArray(source.route) && source.route.length === 0 && source.sourcePath === 'code' &&
    source.valuePath === 'valueQuantity.value' && source.exampleConcept === 'Height' &&
    Number.isFinite(source.observedOccurrences) && responseSource?.resourceType === source.resourceType &&
    sameJson(responseSource.route, source.route) && responseSource.sourcePath === source.sourcePath &&
    responseSource.valuePath === source.valuePath && responseSource.exampleConcept === source.exampleConcept &&
    setSourceOwner?.origin === failedOwner?.origin &&
    setSourceOwner?.project === failedOwner?.project && setSourceOwner?.explorerId === failedOwner?.explorerId &&
    Number.isInteger(setSourceResponse?.status) && setSourceResponse.status >= 200 && setSourceResponse.status < 300 &&
    setSourceRequest?.body?.snapshotToken === failedRequest.snapshotToken &&
    setSourceCommand?.frameChoiceId === selection.choiceId && setSourceCommand?.outputId === failedRequest.outputId &&
    assertion?.evidence?.sourceChoiceId === selection.choiceId &&
    proof.assertion?.name === 'native Coded values controls save the direct Observation Height frame'
  );
};

export const classifyCodedColumnDiagnostics = report => {
  const expectedObsoleteReads = [];
  const cancelledReads = report.network.filter(item => {
    try {
      const proof = (report.expectedObsolete ?? []).find(candidate =>
        candidate.failedRequest?.playwrightRequestId === item.playwrightRequestId);
      if (proof?.kind === 'capability-command-invalidation') {
        if (!hasExactActionBoundCapabilityInvalidation(report, item, proof)) return false;
      } else if (!proof) {
        return false;
      }
      const matches = proof.kind === 'frame-source-search'
        ? hasExactFrameSearchInvalidation(report, item, proof)
        : proof.kind === 'capability-command-invalidation';
      if (!matches) return false;
      item.expectedObsolete = true;
      item.obsolescenceEvidence = proof;
      if (!registerValidatedObsoleteNetworkRead(report, item, proof)) {
        delete item.expectedObsolete;
        delete item.obsolescenceEvidence;
        return false;
      }
      expectedObsoleteReads.push(item);
      return true;
    } catch {
      return false;
    }
  });
  report.expectedObsoleteReads = expectedObsoleteReads;
  return {
    cancelledReads,
    unexpected: report.network.filter(item => !cancelledReads.includes(item)),
  };
};

export const directHeightQuantityChoices = (renderedChoices, sourceOptions) => {
  const optionsByChoiceID = new Map(sourceOptions.map(source => [source.choiceId, source]));
  return renderedChoices.filter(choice => {
    if (!/Example:\s*Height\b/i.test(choice.text)) return false;
    const prefix = 'frame-source-choice-';
    if (typeof choice.testId !== 'string' || !choice.testId.startsWith(prefix)) return false;
    const source = optionsByChoiceID.get(choice.testId.slice(prefix.length));
    return source?.resourceType === 'Observation' && Array.isArray(source.route) && source.route.length === 0 &&
      /(?:^|\.)code(?:\.|$)/i.test(source.sourcePath) && source.valuePath === 'valueQuantity.value';
  });
};

const checkUnexpectedDiagnostics = report => {
  const { cancelledReads, unexpected } = classifyCodedColumnDiagnostics(report);
  report.cancelledOwnedReads = cancelledReads;
  recordCheck(report, 'correctness', 'no unexpected network, API, or browser errors', unexpected.length === 0,
    { console: unexpected.filter(item => item.kind === 'console-error'),
      pageErrors: unexpected.filter(item => item.kind === 'exception'),
      networkFailures: unexpected.filter(item => item.kind === 'network') });
};

export const builderCodedSourceColumnWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  configureNativePage(page);
  const target = context.target;
  const requestCaptureStartedAt = performance.now();
  const capturedRequestByObject = new WeakMap();
  const capturedRequestFailures = [];
  const ownedWorkflowRequest = request => {
    let url;
    try { url = new URL(request.url()); } catch { return undefined; }
    if (url.origin !== new URL(target.uiUrl).origin || request.method() !== 'POST') return undefined;
    const match = url.pathname.match(/^\/api\/v1\/projects\/([^/]+)\/explorers\/([^/]+)\/authoring\/v2\/(frame-source-options|construction-capabilities)$/);
    if (!match || decodeURIComponent(match[1]) !== target.fixtureProject) return undefined;
    const explorerId = decodeURIComponent(match[2]);
    if (explorerId !== (report.target.explorer ?? target.bootstrapExplorerId)) return undefined;
    let body;
    try { body = request.postDataJSON(); } catch { return undefined; }
    const requestHeaders = request.headers();
    const headerRequestId = requestHeaders['x-request-id'] ?? requestHeaders['X-Request-ID'];
    const frameBody = match[3] === 'frame-source-options'
      ? normalizeFrameSourceRequest(body, headerRequestId)
      : undefined;
    const common = {
      url: url.origin + url.pathname,
      project: decodeURIComponent(match[1]),
      explorerId,
      method: request.method(),
      body,
      requestId: frameBody?.requestId ?? headerRequestId ?? body?.requestId ?? body?.requestID ?? null,
    };
    common.startedAt = performance.now();
    common.startedAtMs = common.startedAt - requestCaptureStartedAt;
    if (match[3] === 'frame-source-options' && frameBody) {
      return { ...common, body: frameBody, kind: 'frame-source-options' };
    }
    if (match[3] === 'construction-capabilities' && typeof body?.snapshotToken === 'string' &&
        Number.isInteger(body.expectedDraftVersion) && typeof body.expectedDraftDigest === 'string' &&
        typeof body.outputId === 'string' && typeof body.stageId === 'string') {
      return { ...common, kind: 'construction-capabilities',
        binding: {
          route: url.origin + url.pathname,
          snapshotToken: body.snapshotToken,
          draftVersion: body.expectedDraftVersion,
          draftDigest: body.expectedDraftDigest,
          outputId: body.outputId,
          stageId: body.stageId,
        } };
    }
    return undefined;
  };
  const onOwnedRequest = request => {
    const capture = ownedWorkflowRequest(request);
    if (capture) capturedRequestByObject.set(request, capture);
  };
  const onOwnedRequestFailed = request => {
    const capture = capturedRequestByObject.get(request);
    if (!capture) return;
    capture.errorText = request.failure()?.errorText ?? '';
    capture.failedAt = performance.now();
    capture.failedAtMs = capture.failedAt - requestCaptureStartedAt;
    capture.durationMs = Math.max(0, Math.round(capture.failedAt - capture.startedAt));
    capturedRequestFailures.push(capture);
  };
  page.on('request', onOwnedRequest);
  page.on('requestfailed', onOwnedRequestFailed);
  page.once('close', () => {
    page.off('request', onOwnedRequest);
    page.off('requestfailed', onOwnedRequestFailed);
  });
  const capturedFailureFor = async (predicate, timeoutMs = 1500) => {
    const deadline = performance.now() + timeoutMs;
    while (performance.now() < deadline) {
      const match = capturedRequestFailures.find(predicate);
      if (match) return match;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    return capturedRequestFailures.find(predicate);
  };
  const reportFailureFor = captured => {
    if (!captured) return undefined;
    const matches = report.network.filter(item => item.kind === 'network' && item.method === captured.method &&
      item.errorText === captured.errorText && item.rawURL === captured.url &&
      Number.isFinite(item.requestTimeline?.durationMs) && Number.isFinite(captured.durationMs) &&
      Math.abs(item.requestTimeline.durationMs - captured.durationMs) <= 2 &&
      (!captured.requestId || item.requestDetails?.requestId === captured.requestId) &&
      (captured.kind === 'frame-source-options'
        ? item.requestDetails?.requestId === captured.body.requestId && item.requestDetails?.outputId === captured.body.outputId
        : sameJson(item.binding, captured.binding) &&
          item.requestDetails?.outputId === captured.binding.outputId &&
          item.requestDetails?.draftVersion === captured.binding.draftVersion &&
          item.requestDetails?.draftDigest === captured.binding.draftDigest &&
          item.requestDetails?.stageId === captured.binding.stageId));
    return matches.length === 1 && typeof matches[0].playwrightRequestId === 'string' && matches[0].playwrightRequestId
      ? matches[0]
      : undefined;
  };
  const actionRecord = label => [...report.actions].reverse().find(item => item.label === label);
  const responseReceipt = async (response, expectedCommandType) => {
    const request = response.request();
    const body = requestBody(request);
    const url = new URL(response.url());
    const route = url.pathname.match(/^\/api\/v1\/projects\/([^/]+)\/explorers\/([^/]+)\/authoring\/v2\/commands$/);
    const receipt = await response.json();
    const command = body?.commands?.find(item => item.type === expectedCommandType);
    return {
      request: {
        url: url.origin + url.pathname,
        project: route ? decodeURIComponent(route[1]) : undefined,
        explorerId: route ? decodeURIComponent(route[2]) : undefined,
        body,
      },
      response: {
        status: response.status(),
        completedAtEpochMs: Date.now(),
        body: {
          commandId: receipt.commandId,
          draftVersion: receipt.draftVersion,
          draftDigest: receipt.draftDigest,
        },
      },
      command,
    };
  };
  const sourcePath = join(target.fixtureDir, 'Observation.ndjson');
  const sourceBytes = readFileSync(sourcePath);
  const sourceSHA256 = createHash('sha256').update(sourceBytes).digest('hex');
  const observations = sourceBytes.toString('utf8').trim().split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const expectedIDs = observations.map(observation => observation.id).sort();
  const expectedHeightRows = observations.map(observation => {
    const heightCoding = observation.code?.coding?.find(coding =>
      coding.system === 'https://example.test/codes' && coding.code === 'height');
    return { id: observation.id, value: heightCoding ? String(observation.valueQuantity?.value) : null };
  }).sort((left, right) => left.id.localeCompare(right.id));
  assert.equal(observations.length, 6, 'The devloop fixture must contain exactly six Observation records.');
  assert.equal(new Set(expectedIDs).size, expectedIDs.length, 'The Observation fixture IDs must be unique.');
  assert.deepEqual(expectedHeightRows.filter(row => row.value !== null), [
    { id: 'dev-observation-001', value: '172.5' },
    { id: 'dev-observation-003', value: '180' },
  ], 'The independent Observation fixture must contain the expected two Height values.');
  assert.equal(expectedHeightRows.filter(row => row.value === null).length, 4);
  report.target.fixtureOracle = {
    path: sourcePath,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sha256: sourceSHA256,
    observationCount: observations.length,
    observationIDs: expectedIDs,
    heightRows: expectedHeightRows,
  };
  check('correctness', 'fresh fixture contains the exact six independent Observation IDs and Height values',
    context.custom === false && context.seed?.fresh === true && target.fixtureProject.startsWith('loom_dev_verify_') &&
      target.bootstrapExplorerId && expectedIDs.length === 6,
    { project: target.fixtureProject, generation: target.fixtureGeneration,
      bootstrapExplorerId: target.bootstrapExplorerId, fresh: context.seed?.fresh === true,
      observationIDs: expectedIDs, heightRows: expectedHeightRows });

  const act = async (name, locator, perform, options = {}) => {
    await unique(locator);
    return action(name, locator, perform, options);
  };
  const uiOrigin = new URL(target.uiUrl).origin;
  const explorerCollectionPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const title = `Verify ${context.runID.slice(-10)} coded source`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  await page.getByText('New explorer', { exact: true }).waitFor({ state: 'visible' });
  await act('open New explorer', page.getByText('New explorer', { exact: true }),
    () => page.getByText('New explorer', { exact: true }).click());
  const nameInput = page.locator('#new-explorer-name');
  await act('name disposable coded-source Explorer', nameInput, () => nameInput.fill(title), { editable: true });
  const createResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === explorerCollectionPath &&
      requestBody(request)?.title === title;
  });
  const createBlank = page.getByRole('button', { name: 'Create blank' });
  await act('create blank disposable Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      const response = await createResponsePromise;
      assert(response.ok(), `Native Explorer creation returned HTTP ${response.status()}.`);
    },
  });
  await page.waitForFunction(expectedTitle => {
    const select = document.querySelector('select[aria-label="Explorer"]');
    return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
  }, title);
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The user-created Explorer must be distinct from the fixture bootstrap.');
  report.target.explorer = explorer;
  report.target.nativeMutationInventory = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    bootstrapExplorerId: target.bootstrapExplorerId,
    explorerId: explorer,
    setup: ['fresh fixture project/generation/bootstrap Explorer from the fixture harness', 'native New explorer → Create blank'],
    authoring: [],
    cleanup: [],
    fixtureProjectTeardown: 'The fixture harness retains each uniquely named loom_dev_verify_* project; this case removes its coded source column through Builder UI and leaves the disposable project/Explorer retained.',
  };
  check('correctness', 'Builder authoring is scoped to one new disposable Explorer in the fresh fixture project',
    Boolean(explorer && explorer !== target.bootstrapExplorerId && target.fixtureProject.startsWith('loom_dev_verify_')),
    { project: target.fixtureProject, generation: target.fixtureGeneration,
      bootstrapExplorerId: target.bootstrapExplorerId, explorerId: explorer,
      createStatus: (await createResponsePromise).status() });

  const tableName = page.locator('#first-table-name');
  await act('name Observation table', tableName, () => tableName.fill('Observations'), { editable: true });
  const commandPath = `${explorerCollectionPath}/${encodeURIComponent(explorer)}/authoring/v2/commands`;
  const createTableCommandPromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' && commandHas(request, 'CREATE_TABLE');
  });
  const addRootIDCommandPromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' &&
      body?.commands?.some(command => command.type === 'ADD_COLUMN' && command.title === 'Observation ID');
  });
  await act('choose Observation rows', page.getByRole('button', { name: 'Choose Observation rows' }),
    () => page.getByRole('button', { name: 'Choose Observation rows' }).click(), {
      after: async () => {
        const [createResponse, addIDResponse] = await Promise.all([
          createTableCommandPromise, addRootIDCommandPromise,
        ]);
        assert(createResponse.ok(), `Native Observation table command returned HTTP ${createResponse.status()}.`);
        assert(addIDResponse.ok(), `Native Observation ID column command returned HTTP ${addIDResponse.status()}.`);
      },
    });
  await page.getByTestId('construction-action-add-columns').waitFor({ state: 'visible' });
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-add-columns"]');
    return button && !button.disabled;
  });
  await waitForPreview(page, expectedIDs.length, 1);
  const rootPreview = await previewSnapshot(page);
  const rootIDIndex = rootPreview.headers.findIndex(label => previewHeaderMatches(label, 'Observation ID'));
  assert.notEqual(rootIDIndex, -1, `The root preview has no Observation ID column: ${JSON.stringify(rootPreview.headers)}`);
  const rootIDs = rootPreview.rows.map(row => row[rootIDIndex]).sort();
  check('correctness', 'native Observation root preview renders all six independent fixture IDs',
    JSON.stringify(rootIDs) === JSON.stringify(expectedIDs),
    { expectedIDs, rootIDs, headers: rootPreview.headers, rowCount: rootPreview.rowCount });
  const tableTab = page.locator('[data-testid^="construction-table-"]');
  await unique(tableTab);
  const tableTestId = await tableTab.getAttribute('data-testid');
  const outputId = tableTestId.replace(/^construction-table-/, '');
  report.target.outputId = outputId;
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Choose Observation rows'],
    capturedCommands: ['CREATE_TABLE', 'ADD_COLUMN Observation ID'],
    outputId,
  });

  const addColumns = page.getByRole('button', { name: /Add columns:/ });
  await act('open Add columns', addColumns, () => addColumns.click());
  await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' });
  await act('open Coded values', page.getByRole('button', { name: 'Coded values', exact: true }),
    () => page.getByRole('button', { name: 'Coded values', exact: true }).click());
  const framePanel = page.getByTestId('frame-source-panel');
  await framePanel.waitFor({ state: 'visible' });
  const browseButton = framePanel.getByRole('button', { name: /^Browse sources/ });
  await act('browse coded source values', browseButton, () => browseButton.click());
  const sourceSearch = page.getByRole('searchbox', { name: 'Search framing sources' });
  await sourceSearch.waitFor({ state: 'visible' });
  await act('search Observation coded sources', sourceSearch, () => sourceSearch.fill('Observation'), { editable: true });
  const sourceOptionsResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return request.method() === 'POST' && new URL(response.url()).pathname.endsWith('/frame-source-options') &&
      body?.outputId === outputId && body?.query === 'Observation';
  });
  let sourceOptionsBody;
  let sourceOptionsResponse;
  let sourceOptionsRequestCapture;
  const sourceSearchButton = framePanel.getByRole('button', { name: 'Search', exact: true });
  await act('search native Observation framing choices', sourceSearchButton, () => sourceSearchButton.click(), {
    after: async () => {
      const response = await sourceOptionsResponsePromise;
      assert(response.ok(), `Native Observation frame search returned HTTP ${response.status()}.`);
      sourceOptionsResponse = response;
      sourceOptionsBody = await response.json();
      sourceOptionsRequestCapture = capturedRequestByObject.get(response.request());
      assert.equal(sourceOptionsBody.outputId, outputId, 'Native frame search must stay on the selected Observation table.');
    },
  });
  const choiceButtons = page.locator('[data-testid^="frame-source-choice-"]');
  await choiceButtons.first().waitFor({ state: 'visible' });
  const sourceChoices = await choiceButtons.evaluateAll(buttons => buttons.map(button => ({
    testId: button.getAttribute('data-testid'),
    text: button.parentElement?.innerText?.replace(/\s+/g, ' ').trim() ?? '',
  })));
  const heightSourceChoices = directHeightQuantityChoices(sourceChoices, sourceOptionsBody.sources);
  assert.equal(heightSourceChoices.length, 1,
    `The native search must expose one direct Observation Height Quantity source; choices: ${JSON.stringify(sourceChoices)}`);
  const sourceChoiceId = heightSourceChoices[0].testId.replace(/^frame-source-choice-/, '');
  const sourceOption = sourceOptionsBody.sources.find(source => source.choiceId === sourceChoiceId);
  assert(sourceOption, 'The native Height source control must map to its own frame-source response item.');
  assert.equal(sourceOption.resourceType, 'Observation');
  assert.equal(sourceOption.route.length, 0, 'The Height source must be on direct Observation records.');
  assert.match(sourceOption.sourcePath.toLowerCase(), /code/);
  assert.equal(sourceOption.valuePath, 'valueQuantity.value', 'The selected Height code must pair with the fixture Quantity value field.');
  const sourceChoice = page.getByTestId(heightSourceChoices[0].testId);
  const initialCategoryResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return request.method() === 'POST' && new URL(response.url()).pathname.endsWith('/semantic-inventory') &&
      body?.outputId === outputId && Boolean(body?.frameId) && !body?.query;
  });
  const frameSourceResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' && commandHas(request, 'SET_FRAME_SOURCE');
  });
  let frameSourceResponse;
  await act('use the direct Observation Height source', sourceChoice, () => sourceChoice.click(), {
    after: async () => {
      const response = await frameSourceResponsePromise;
      assert(response.ok(), `Native SET_FRAME_SOURCE command returned HTTP ${response.status()}.`);
      frameSourceResponse = response;
      const command = requestBody(response.request()).commands.find(candidate => candidate.type === 'SET_FRAME_SOURCE');
      assert.equal(command?.frameChoiceId, sourceChoiceId, 'The saved frame must use the exact source choice selected in the UI.');
    },
  });
  const savedFrame = page.locator('[data-testid^="saved-frame-"]');
  await savedFrame.waitFor({ state: 'visible' });
  await unique(savedFrame);
  const savedFrameTestId = await savedFrame.getAttribute('data-testid');
  const frameId = savedFrameTestId.replace(/^saved-frame-/, '');
  report.target.frameId = frameId;
  const savedFrameText = (await savedFrame.innerText()).replace(/\s+/g, ' ').trim();
  check('correctness', 'native Coded values controls save the direct Observation Height frame',
    Boolean(frameId && /Observation/i.test(savedFrameText) && /On each Observation record/i.test(savedFrameText)),
    { frameId, sourceChoiceId, sourceCard: heightSourceChoices[0].text, savedFrameText });
  const blankSourceRequest = await capturedFailureFor(capture => capture.kind === 'frame-source-options' &&
    capture.body.query === '' && capture.errorText === 'net::ERR_ABORTED');
  const blankSourceFailure = reportFailureFor(blankSourceRequest);
  report.target.frameSourceFailureCaptures = capturedRequestFailures
    .filter(capture => capture.kind === 'frame-source-options')
    .map(capture => frameSourceFailureCaptureRecord(capture, reportFailureFor(capture)))
    .filter(Boolean);
  if (blankSourceRequest && blankSourceFailure && sourceOptionsResponse &&
      sourceOptionsRequestCapture && frameSourceResponse) {
    const sourceAction = actionRecord('search native Observation framing choices');
    const persistedSourceCheck = [...report.assertions].reverse().find(assertion =>
      assertion.name === 'native Coded values controls save the direct Observation Height frame');
    const selectedSource = sourceOptionsBody.sources.find(source => source.choiceId === sourceChoiceId);
    appendFrameSearchInvalidationProof(report, {
      networkFailure: blankSourceFailure,
      failedRequestCapture: blankSourceRequest,
      replacementRequestCapture: sourceOptionsRequestCapture,
      replacementResponse: {
        status: sourceOptionsResponse.status(),
        finished: true,
        body: {
          query: sourceOptionsRequestCapture.body.query,
          outputId: sourceOptionsBody.outputId,
          snapshotToken: sourceOptionsBody.snapshotToken,
          sources: sourceOptionsBody.sources.map(source => ({
            choiceId: source.choiceId,
            resourceType: source.resourceType,
            route: source.route,
            sourcePath: source.sourcePath,
            valuePath: source.valuePath,
            exampleConcept: source.exampleConcept,
            observedOccurrences: source.observedOccurrences,
          })),
        },
      },
      action: sourceAction,
      selection: {
        choiceId: sourceChoiceId,
        source: selectedSource && {
          choiceId: selectedSource.choiceId,
          resourceType: selectedSource.resourceType,
          route: selectedSource.route,
          sourcePath: selectedSource.sourcePath,
          valuePath: selectedSource.valuePath,
          exampleConcept: selectedSource.exampleConcept,
          observedOccurrences: selectedSource.observedOccurrences,
        },
        setFrameSource: {
          request: {
            url: frameSourceResponse.url(),
            body: requestBody(frameSourceResponse.request()),
          },
          response: { status: frameSourceResponse.status() },
        },
      },
      assertion: persistedSourceCheck,
    });
  }
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Add columns', 'Coded values', 'Browse sources', 'Search framing sources: Observation', 'Use this source'],
    capturedCommand: 'SET_FRAME_SOURCE',
    frameId,
  });

  const categoryPanel = page.getByTestId(`frame-categories-${frameId}`);
  if (!(await categoryPanel.isVisible())) {
    await act('open saved Observation coded values', savedFrame.getByRole('button', { name: 'Choose values', exact: true }),
      () => savedFrame.getByRole('button', { name: 'Choose values', exact: true }).click());
  }
  await categoryPanel.waitFor({ state: 'visible' });
  const categorySearch = categoryPanel.getByRole('searchbox', { name: /^Search coded values in / });
  await categorySearch.waitFor({ state: 'visible' });
  const initialCategoryResponse = await initialCategoryResponsePromise;
  assert(initialCategoryResponse.ok(), `Initial native coded-value inventory returned HTTP ${initialCategoryResponse.status()}.`);
  assert.equal(requestBody(initialCategoryResponse.request())?.frameId, frameId,
    'The initial coded-value inventory must belong to the new Observation frame.');
  await act('search Height coded value', categorySearch, () => categorySearch.fill('height'), { editable: true });
  const heightInventoryResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return request.method() === 'POST' && new URL(response.url()).pathname.endsWith('/semantic-inventory') &&
      body?.frameId === frameId && body?.outputId === outputId && body?.query === 'height';
  });
  let heightInventoryBody;
  await act('search native Height categories', categoryPanel.getByRole('button', { name: 'Search', exact: true }),
    () => categoryPanel.getByRole('button', { name: 'Search', exact: true }).click(), {
      after: async () => {
        const response = await heightInventoryResponsePromise;
        assert(response.ok(), `Height semantic inventory returned HTTP ${response.status()}.`);
        heightInventoryBody = await response.json();
        assert.equal(heightInventoryBody.frameId, frameId);
        assert.equal(heightInventoryBody.state, 'complete');
      },
    });
  const independentHeightChoices = heightInventoryBody.entries.filter(entry =>
    entry.resourceType === 'Observation' && entry.system === 'https://example.test/codes' &&
    entry.code === 'height' && entry.display === 'Height');
  assert.equal(independentHeightChoices.length, 1, 'The UI inventory must expose the exact source Height coding once.');
  const heightChoice = categoryPanel.getByRole('checkbox', { name: 'Select Height', exact: true });
  await heightChoice.waitFor({ state: 'visible' });
  check('correctness', 'native Height category matches the independent Observation code identity',
    await heightChoice.isEnabled() && independentHeightChoices.length === 1,
    { frameId, system: independentHeightChoices[0].system, code: independentHeightChoices[0].code,
      display: independentHeightChoices[0].display, sourcePath: independentHeightChoices[0].sourcePath,
      valueSelector: independentHeightChoices[0].valueSelector });
  await act('select Height coded value', heightChoice, () => heightChoice.check());
  const addHeightColumn = categoryPanel.getByRole('button', { name: 'Add 1 column', exact: true });
  const constructionChoiceResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return request.method() === 'POST' && new URL(response.url()).pathname.endsWith('/construction-choice-proposals') &&
      body?.outputId === outputId && body?.constructionChoices?.length === 1 &&
      body.constructionChoices[0]?.frameId === frameId;
  });
  const applyHeightCommandPromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' &&
      body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_CHOICE' &&
        command.constructionChoice?.frameId === frameId);
  });
  await act('add and save the Height coded source column', addHeightColumn, () => addHeightColumn.click(), {
    after: async () => {
      const [proposalResponse, applyResponse] = await Promise.all([
        constructionChoiceResponsePromise, applyHeightCommandPromise,
      ]);
      assert(proposalResponse.ok(), `Native coded-column proposal returned HTTP ${proposalResponse.status()}.`);
      assert(applyResponse.ok(), `Native coded-column save returned HTTP ${applyResponse.status()}.`);
      const proposal = await proposalResponse.json();
      assert.equal(proposal.previewStatus, 'READY', 'The UI-owned coded-column proposal must include a ready preview.');
      assert.equal(proposal.candidateColumnIds?.length, 1, 'The Height proposal must identify one candidate column.');
      const command = requestBody(applyResponse.request());
      assert.equal(command.commands.length, 1, 'The UI must save only the selected Height coded column.');
      assert.equal(command.commands[0].type, 'APPLY_CONSTRUCTION_CHOICE');
      assert.equal(command.commands[0].constructionChoice.frameId, frameId);
    },
  });
  await waitForPreview(page, expectedIDs.length, 2);
  const savedLabel = 'Height';
  const savedPreview = await previewSnapshot(page);
  const actualHeightRows = rowsByObservationID(savedPreview, savedLabel);
  check('correctness', 'saved Height column preview matches the independent raw Observation oracle',
    JSON.stringify(actualHeightRows) === JSON.stringify(expectedHeightRows) &&
      savedPreview.rowCount === expectedIDs.length && savedPreview.columnCount === 2,
    { expectedHeightRows, actualHeightRows, headers: savedPreview.headers,
      rowCount: savedPreview.rowCount, columnCount: savedPreview.columnCount });
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Select Height', 'Add 1 column'],
    capturedRequests: ['POST /construction-choice-proposals → READY preview', 'POST /commands with APPLY_CONSTRUCTION_CHOICE'],
    frameId,
    savedLabel,
  });

  const editedLabel = `Fixture Height ${context.runID.slice(-6)}`;
  await act('open table Columns menu', page.getByRole('button', { name: 'Columns', exact: true }),
    () => page.getByRole('button', { name: 'Columns', exact: true }).click());
  const labelInput = page.getByRole('textbox', { name: `Column name for ${savedLabel}`, exact: true });
  await act('edit saved Height column label', labelInput, () => labelInput.fill(editedLabel), { editable: true });
  const renameResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' && commandHas(request, 'UPDATE_COLUMN');
  });
  let renameMutation;
  await act('save edited Height column label', labelInput, () => labelInput.press('Enter'), {
    after: async () => {
      const response = await renameResponsePromise;
      assert(response.ok(), `Native coded-column edit returned HTTP ${response.status()}.`);
      renameMutation = await responseReceipt(response, 'UPDATE_COLUMN');
      assert.equal(renameMutation.request.body.commands.length, 1, 'The native label edit must save only the selected Height column.');
      assert.equal(renameMutation.command.outputId, outputId);
      assert.equal(renameMutation.command.columnValue?.label, editedLabel);
      assert.equal(renameMutation.response.body.commandId, renameMutation.request.body.commandId);
    },
  });
  const editedHeader = page.locator('[data-testid="preview-table-scroll"] [role="columnheader"]')
    .filter({ hasText: new RegExp(editedLabel.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i') });
  await editedHeader.waitFor({ state: 'visible' });
  const editedPreview = await previewSnapshot(page);
  const editedHeightRows = rowsByObservationID(editedPreview, editedLabel);
  check('persistence', 'edited coded source column label preserves the exact Height values',
    JSON.stringify(editedHeightRows) === JSON.stringify(expectedHeightRows),
    { editedLabel, expectedHeightRows, actualHeightRows: editedHeightRows, headers: editedPreview.headers });
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Columns', `Column name for ${savedLabel}`, 'Enter'],
    capturedCommand: 'UPDATE_COLUMN',
    editedLabel,
  });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitForPreview(page, expectedIDs.length, 2);
  const reloadedEditedPreview = await previewSnapshot(page);
  const reloadedEditedHeightRows = rowsByObservationID(reloadedEditedPreview, editedLabel);
  check('persistence', 'edited Height column and exact values survive Builder reload',
    JSON.stringify(reloadedEditedHeightRows) === JSON.stringify(expectedHeightRows) &&
      reloadedEditedPreview.headers.some(header => previewHeaderMatches(header, editedLabel)),
    { explorer, outputId, frameId, editedLabel, expectedHeightRows,
      actualHeightRows: reloadedEditedHeightRows, headers: reloadedEditedPreview.headers });
  const renameAction = actionRecord('save edited Height column label');
  const renameObsoleteRequest = await capturedFailureFor(capture => capture.kind === 'construction-capabilities' &&
    capture.binding.outputId === outputId &&
    capture.binding.snapshotToken === renameMutation?.request.body?.snapshotToken &&
    capture.binding.draftVersion === renameMutation?.request.body?.expectedDraftVersion &&
    capture.binding.draftDigest === renameMutation?.request.body?.expectedDraftDigest &&
    capture.errorText === 'net::ERR_ABORTED', 100);
  const renameObsoleteFailure = reportFailureFor(renameObsoleteRequest);
  const renameAssertion = [...report.assertions].reverse().find(assertion =>
    assertion.name === 'edited Height column and exact values survive Builder reload');
  if (renameAction && renameMutation && renameObsoleteRequest && renameObsoleteFailure && renameAssertion) {
    appendCapabilityCommandInvalidationProof(report, {
      networkFailure: renameObsoleteFailure,
      commandType: 'UPDATE_COLUMN',
      editedLabel,
      mutation: renameMutation,
      action: renameAction,
      assertion: renameAssertion,
    });
  }

  await act('reopen Add columns after reload', page.getByRole('button', { name: /Add columns:/ }),
    () => page.getByRole('button', { name: /Add columns:/ }).click());
  await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' });
  await act('open Coded values after reload', page.getByRole('button', { name: 'Coded values', exact: true }),
    () => page.getByRole('button', { name: 'Coded values', exact: true }).click());
  const reloadedFrame = page.getByTestId(`saved-frame-${frameId}`);
  await reloadedFrame.waitFor({ state: 'visible' });
  const removeHeightColumn = reloadedFrame.getByRole('button', { name: `Remove ${editedLabel} column`, exact: true });
  const removeCommandPromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' && commandHas(request, 'REMOVE_COLUMN');
  });
  let removeMutation;
  await act('remove saved Height coded column', removeHeightColumn, () => removeHeightColumn.click(), {
    after: async () => {
      const response = await removeCommandPromise;
      assert(response.ok(), `Native REMOVE_COLUMN command returned HTTP ${response.status()}.`);
      removeMutation = await responseReceipt(response, 'REMOVE_COLUMN');
      assert.equal(removeMutation.request.body.commands.length, 1, 'The native removal must affect only the Height coded column.');
      assert.equal(removeMutation.command.outputId, outputId);
      assert.equal(typeof removeMutation.command.column, 'string');
      assert.equal(removeMutation.response.body.commandId, removeMutation.request.body.commandId);
    },
  });
  await waitForPreview(page, expectedIDs.length, 1);
  const removedPreview = await previewSnapshot(page);
  const removedIDIndex = removedPreview.headers.findIndex(label => previewHeaderMatches(label, 'Observation ID'));
  const removedIDs = removedPreview.rows.map(row => row[removedIDIndex]).sort();
  check('correctness', 'native Remove column restores the six Observation ID rows',
    JSON.stringify(removedIDs) === JSON.stringify(expectedIDs) &&
      !removedPreview.headers.some(header => previewHeaderMatches(header, editedLabel)),
    { expectedIDs, removedIDs, headers: removedPreview.headers, rowCount: removedPreview.rowCount });
  report.target.nativeMutationInventory.cleanup.push({
    control: `Remove ${editedLabel} column`,
    capturedCommand: 'REMOVE_COLUMN',
    explorerId: explorer,
    outputId,
  });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitForPreview(page, expectedIDs.length, 1);
  const reloadedRemovedPreview = await previewSnapshot(page);
  const finalIDIndex = reloadedRemovedPreview.headers.findIndex(label => previewHeaderMatches(label, 'Observation ID'));
  const finalIDs = reloadedRemovedPreview.rows.map(row => row[finalIDIndex]).sort();
  check('persistence', 'removed coded column stays absent after Builder reload',
    JSON.stringify(finalIDs) === JSON.stringify(expectedIDs) &&
      !reloadedRemovedPreview.headers.some(header => normalizePreviewHeader(header).includes('height')) &&
      reloadedRemovedPreview.columnCount === 1,
    { expectedIDs, finalIDs, headers: reloadedRemovedPreview.headers,
      rowCount: reloadedRemovedPreview.rowCount, columnCount: reloadedRemovedPreview.columnCount });
  const removeAction = actionRecord('remove saved Height coded column');
  const removeObsoleteRequest = await capturedFailureFor(capture => capture.kind === 'construction-capabilities' &&
    capture.binding.outputId === outputId &&
    capture.binding.snapshotToken === removeMutation?.request.body?.snapshotToken &&
    capture.binding.draftVersion === removeMutation?.request.body?.expectedDraftVersion &&
    capture.binding.draftDigest === removeMutation?.request.body?.expectedDraftDigest &&
    capture.errorText === 'net::ERR_ABORTED', 100);
  const removeObsoleteFailure = reportFailureFor(removeObsoleteRequest);
  const removeAssertion = [...report.assertions].reverse().find(assertion =>
    assertion.name === 'native Remove column restores the six Observation ID rows');
  const persistedRemoveAssertion = [...report.assertions].reverse().find(assertion =>
    assertion.name === 'removed coded column stays absent after Builder reload');
  if (removeAction && removeMutation && removeObsoleteRequest && removeObsoleteFailure && removeAssertion && persistedRemoveAssertion) {
    appendCapabilityCommandInvalidationProof(report, {
      networkFailure: removeObsoleteFailure,
      commandType: 'REMOVE_COLUMN',
      editedLabel,
      mutation: removeMutation,
      action: removeAction,
      assertion: removeAssertion,
      reloadAssertion: persistedRemoveAssertion,
    });
  }

  checkUnexpectedDiagnostics(report);
  const sourceSHA256After = createHash('sha256').update(readFileSync(sourcePath)).digest('hex');
  recordCheck(report, 'correctness', 'independent Observation source stayed unchanged during browser lifecycle',
    sourceSHA256After === sourceSHA256, { before: sourceSHA256, after: sourceSHA256After });
};
