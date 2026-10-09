import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { nativeAbortSignalObservationForRequest } from './native-abort-probe.mjs';

export const reportDimensions = Object.freeze(['usability', 'correctness', 'persistence', 'performance']);

export const createReport = ({ scenario, target, evidenceDirectory, caseName, requiredChecks = [] }) => ({
  schemaVersion: 2,
  scenario,
  case: caseName ?? null,
  target,
  status: 'running',
  dimensions: Object.fromEntries(reportDimensions.map((dimension) => [dimension, { status: 'untested', evidence: [] }])),
  actions: [],
  errors: [],
  network: [],
  assetFailures: [],
  assertions: [],
  requiredChecks,
  missingRequiredChecks: [],
  evidence: [],
  timings: {},
  failureDom: [],
  startedAt: new Date().toISOString(),
  evidenceDirectory,
});

export const recordCheck = (report, dimension, name, passed, evidence = {}) => {
  const status = passed ? 'passed' : 'failed';
  const current = report.dimensions[dimension];
  if (!current) throw new Error('unknown report dimension: ' + dimension);
  if (status === 'failed' || current.status === 'untested') current.status = status;
  report.assertions.push({ dimension, name, status, evidence });
  current.evidence.push({ name, status, ...evidence });
  return passed;
};

export const recordUntested = (report, dimension, name, reason) => {
  const current = report.dimensions[dimension];
  if (!current) throw new Error('unknown report dimension: ' + dimension);
  current.evidence.push({ name, status: 'untested', reason });
};

const validatedObsoleteNetworkReads = new WeakMap();

const isCodedSourceColumnReport = (report) =>
  report?.scenario === 'builder-coded-source-column' && report?.case === 'coded-source-column';

const validationSnapshot = (report, record, proof) => {
  const assertionNames = new Set([proof.assertion?.name, proof.assertion?.reloadName].filter(Boolean));
  const networkRecord = { ...record };
  delete networkRecord.rawURL;
  return JSON.stringify({
    proof,
    record: networkRecord,
    fixtureOracle: report.target?.fixtureOracle,
    actions: (report.actions ?? []).filter(action => action.id === proof.action?.id),
    assertions: (report.assertions ?? []).filter(assertion => assertionNames.has(assertion.name)),
  });
};

const exactValidatedObsoleteRead = (report, record) => {
  const registered = validatedObsoleteNetworkReads.get(report)?.get(record.playwrightRequestId);
  const proof = registered?.proof;
  if (!proof || !isCodedSourceColumnReport(report) || record.kind !== 'network' ||
      record.errorText !== 'net::ERR_ABORTED' || record.status >= 400 || record.internalError ||
      record.expectedObsolete !== true || record.obsolescenceEvidence !== proof ||
      (record.rawURL !== undefined && record.rawURL !== record.url) ||
      typeof record.playwrightRequestId !== 'string' || !record.playwrightRequestId ||
      proof.failedRequest?.playwrightRequestId !== record.playwrightRequestId ||
      (report.network ?? []).filter(candidate => candidate === record).length !== 1 ||
      (report.network ?? []).filter(candidate => candidate.kind === 'network' &&
        candidate.playwrightRequestId === record.playwrightRequestId).length !== 1 ||
      (report.expectedObsolete ?? []).filter(candidate => candidate === proof).length !== 1 ||
      (report.expectedObsolete ?? []).filter(candidate =>
        candidate?.failedRequest?.playwrightRequestId === record.playwrightRequestId).length !== 1 ||
      validationSnapshot(report, record, proof) !== registered.snapshot) return false;
  return true;
};

export const registerValidatedObsoleteNetworkRead = (report, record, proof) => {
  if (!isCodedSourceColumnReport(report) || record.kind !== 'network' ||
      record.errorText !== 'net::ERR_ABORTED' || record.status >= 400 || record.internalError ||
      record.expectedObsolete !== true || record.obsolescenceEvidence !== proof ||
      typeof record.playwrightRequestId !== 'string' || !record.playwrightRequestId ||
      proof?.failedRequest?.playwrightRequestId !== record.playwrightRequestId ||
      (report.network ?? []).filter(candidate => candidate === record).length !== 1 ||
      (report.network ?? []).filter(candidate => candidate.kind === 'network' &&
        candidate.playwrightRequestId === record.playwrightRequestId).length !== 1 ||
      (report.expectedObsolete ?? []).filter(candidate => candidate === proof).length !== 1 ||
      (report.expectedObsolete ?? []).filter(candidate =>
        candidate?.failedRequest?.playwrightRequestId === record.playwrightRequestId).length !== 1 ||
      (record.rawURL !== undefined && record.rawURL !== record.url)) return false;
  let proofsByRequestID = validatedObsoleteNetworkReads.get(report);
  if (!proofsByRequestID) {
    proofsByRequestID = new Map();
    validatedObsoleteNetworkReads.set(report, proofsByRequestID);
  }
  const prior = proofsByRequestID.get(record.playwrightRequestId);
  if (prior && prior.proof !== proof) return false;
  let snapshot;
  try {
    snapshot = validationSnapshot(report, record, proof);
  } catch {
    return false;
  }
  proofsByRequestID.set(record.playwrightRequestId, Object.freeze({ proof, snapshot }));
  return true;
};

const expectedRootQuantityValidationConsoleMessage =
  'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';

const parseCapturedResponse = (record) => {
  if (record.responseBody?.captureState !== 'completed' || typeof record.responseBody.body !== 'string') return null;
  try { return JSON.parse(record.responseBody.body); } catch { return null; }
};

const exactRootQuantityValidationBatch = (report) => {
  const target = report?.target;
  const { project, explorer } = target ?? {};
  if (report?.scenario !== 'root-quantity-pivot' || report?.case !== 'fixture-lifecycle' ||
      typeof project !== 'string' || !project || typeof explorer !== 'string' || !explorer ||
      (report.explorer !== undefined && report.explorer !== explorer) || typeof target?.uiUrl !== 'string') return null;
  let origin;
  try {
    const targetURL = new URL(target.uiUrl);
    if (targetURL.origin !== target.uiUrl) return null;
    origin = targetURL.origin;
  } catch { return null; }

  const route = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/construction-proposals`;
  const batches = (report.expectedHttpFailureBatches ?? []).filter(batch => batch?.kind === 'expected-root-quantity-pivot-validation-console-batch');
  if (batches.length !== 1) return null;
  const batch = batches[0];
  const { requestIDs, fixtureRequestPairs: pairs, sumRepairPairs: repairs } = batch;
  if (batch.project !== project || batch.explorer !== explorer || batch.route !== route || batch.status !== 422 ||
      batch.code !== 'TABLE_PIVOT_CELL_CARDINALITY' || batch.duplicatePolicy !== 'ERROR' ||
      typeof batch.outputId !== 'string' || typeof batch.snapshotToken !== 'string' ||
      !Number.isInteger(batch.draftVersion) || typeof batch.draftDigest !== 'string' ||
      batch.consoleEventCount !== 2 || batch.consoleEventsHaveRequestIDs !== false ||
      !Array.isArray(requestIDs) || requestIDs.length !== 2 || new Set(requestIDs).size !== 2 ||
      !Array.isArray(pairs) || pairs.length !== 2 || !Array.isArray(repairs) || repairs.length !== 2) return null;
  const pairByRequestID = new Map(pairs.map(pair => [pair?.requestId, pair]));
  const repairByRequestID = new Map(repairs.map(repair => [repair?.validationRequestId, repair]));
  if (pairByRequestID.size !== 2 || repairByRequestID.size !== 2 || requestIDs.some(id =>
      !pairByRequestID.has(id) || !repairByRequestID.has(id)) ||
      new Set(pairs.map(pair => pair?.browserRequestId)).size !== 2 ||
      new Set(pairs.map(pair => pair?.playwrightRequestId)).size !== 2 ||
      new Set(pairs.map(pair => pair?.networkIndex)).size !== 2) return null;

  const url = `${origin}${route}`;
  const requests = pairs.map(pair => Number.isInteger(pair.networkIndex) ? report.network?.[pair.networkIndex] : null);
  const matchingRequests = (report.network ?? []).filter(record => record.kind === 'network' && record.method === 'POST' &&
    record.status === 422 && record.url === url);
  if (requests.some(record => !record) || matchingRequests.length !== 2 || requests.some(record => !matchingRequests.includes(record))) return null;
  for (const pair of pairs) {
    const requestId = pair.requestId;
    const record = report.network[pair.networkIndex];
    const repair = repairByRequestID.get(requestId);
    const expectedProof = { ...batch, requestId, browserRequestId: pair.browserRequestId,
      playwrightRequestId: pair.playwrightRequestId, sumRepair: repair };
    if (record.playwrightRequestId !== pair.playwrightRequestId || record.requestDetails?.requestId !== requestId ||
        record.expected !== true || JSON.stringify(record.expectedHttpFailure) !== JSON.stringify(expectedProof) ||
        record.requestDetails?.outputId !== batch.outputId || record.requestDetails?.draftVersion !== batch.draftVersion ||
        record.requestDetails?.draftDigest !== batch.draftDigest) return null;
    const response = parseCapturedResponse(record);
    const diagnostic = response?.error?.diagnostic;
    if (response?.error?.code !== batch.code || response.error?.requestId !== requestId ||
        response?.diagnostics?.length !== 1 || diagnostic?.code !== batch.code || diagnostic?.requestId !== requestId ||
        diagnostic?.stage !== 'preview' || diagnostic?.severity !== 'error' ||
        response.diagnostics[0]?.code !== batch.code || response.diagnostics[0]?.requestId !== requestId ||
        response.diagnostics[0]?.stage !== 'preview' || response.diagnostics[0]?.severity !== 'error') return null;
  }

  const consoles = (report.network ?? []).filter(record => record.kind === 'console-error' &&
    record.location === url && record.text === expectedRootQuantityValidationConsoleMessage);
  if (consoles.length !== 2 || consoles.some(record => record.expected !== true ||
      JSON.stringify(record.expectedHttpFailure) !== JSON.stringify(batch))) return null;
  return { batch, requests, consoles };
};

const validatedRootQuantityValidationRecord = (report, record) => {
  const evidence = exactRootQuantityValidationBatch(report);
  if (!evidence) return false;
  if (record.kind === 'console-error') return evidence.consoles.includes(record);
  if (record.kind !== 'network') return false;
  return evidence.requests.includes(record);
};

const expectedSchemaFieldsOwnerRetirement = (report, record) => {
  if (record.kind !== 'network' || record.method !== 'POST' || record.resourceType !== 'fetch' ||
      record.errorText !== 'net::ERR_ABORTED' || (record.status !== undefined && record.status !== null) || record.internalError ||
      typeof record.playwrightRequestId !== 'string' || record.playwrightRequestId.length === 0) return null;
  if ((report.network ?? []).filter(candidate => candidate.kind === 'network' &&
      candidate.playwrightRequestId === record.playwrightRequestId).length !== 1) return null;

  const target = report.target;
  const captureScope = report.nativeRequestCaptureScope;
  if (typeof target?.uiUrl !== 'string' || typeof target.project !== 'string' || typeof target.explorer !== 'string' ||
      !captureScope) return null;

  let origin;
  try {
    origin = new URL(target.uiUrl).origin;
  } catch {
    return null;
  }
  const project = encodeURIComponent(target.project);
  const explorer = encodeURIComponent(target.explorer);
  const pathPrefix = `/api/v1/projects/${project}/explorers/${explorer}`;
  const path = `${pathPrefix}/authoring/v2/schema-fields`;
  let requestURL;
  try {
    requestURL = new URL(record.url);
  } catch {
    return null;
  }
  if (requestURL.origin !== origin || requestURL.pathname !== path ||
      record.url !== `${origin}${path}` || captureScope.project !== undefined && captureScope.project !== target.project ||
      captureScope.origin !== undefined && captureScope.origin !== origin ||
      (captureScope.selectedExplorer ?? captureScope.explorer) !== target.explorer ||
      captureScope.observedPathPrefix !== undefined && captureScope.observedPathPrefix !== `${pathPrefix.slice(0, pathPrefix.lastIndexOf('/'))}`) {
    return null;
  }

  const requestId = record.requestDetails?.requestId;
  const ledger = report.nativeRequestTerminalLedger;
  if (typeof requestId !== 'string' || !requestId.startsWith('schema-fields-') ||
      ledger?.requests?.length === undefined) return null;
  const matchingLedger = ledger.requests.filter(entry => entry.requestId === requestId ||
    entry.browserRequestId === record.playwrightRequestId);
  if (matchingLedger.length !== 1) return null;
  const entry = matchingLedger[0];
  if (entry.requestId !== requestId || entry.browserRequestId !== record.playwrightRequestId ||
      entry.origin !== origin || entry.path !== path || entry.method !== 'POST' ||
      entry.status !== null || entry.failure !== 'net::ERR_ABORTED' || entry.terminalEvent !== 'requestfailed' ||
      entry.state !== 'failed' || entry.complete !== true || entry.frameIdentityStatus !== 'exact' ||
      entry.frameIsMainFrame !== true || typeof entry.pageId !== 'string' || typeof entry.frameId !== 'string') return null;
  const chronology = entry.nativeEventChronology;
  if (!Array.isArray(chronology) || chronology.length !== 2 ||
      chronology[0]?.event !== 'request' || chronology[0]?.browserRequestId !== entry.browserRequestId ||
      chronology[0]?.objectMatch !== true || !Number.isFinite(chronology[0]?.observedAt) ||
      chronology[1]?.event !== 'requestfailed' || chronology[1]?.browserRequestId !== entry.browserRequestId ||
      chronology[1]?.objectMatch !== true || chronology[1]?.failure !== 'net::ERR_ABORTED' ||
      !Number.isFinite(chronology[1]?.observedAt) || chronology[1].observedAt < chronology[0].observedAt ||
      entry.requestTimeline?.mainFrameNavigations?.length !== 0 ||
      record.requestTimeline?.mainFrameNavigations?.length !== 0) return null;

  const observations = [
    ...(report.nativeAbortSignalObservations ?? []),
    ...(report.nativeAbortProbeCorrelations ?? []),
  ].filter(item => item.requestId === requestId && item.origin === origin && item.path === path && item.method === 'POST');
  if (observations.length !== 1 || observations[0].requestIdentityMatchCount !== 1) return null;
  const projected = observations[0].observation;
  const recomputed = nativeAbortSignalObservationForRequest({
    ...entry,
    requestCorrelationId: entry.requestId,
    requestIdentityMatchCount: 1,
  }, report.nativeAbortProbeEvents ?? []);
  if (!projected || JSON.stringify(projected) !== JSON.stringify(recomputed) ||
      recomputed.exactRequestSignalCorrelation !== true || recomputed.matchCount !== 1 ||
      recomputed.nativeEntryIdentityMatchCount !== 1 || recomputed.requestId !== requestId ||
      recomputed.origin !== origin || typeof recomputed.controllerId !== 'string' ||
      recomputed.signalWasAlreadyAborted !== false || recomputed.requestObservedAfterAbort !== false ||
      recomputed.fetchStateAtAbort !== 'pending' || recomputed.fetchStateAfterAbort !== 'rejected' ||
      recomputed.nativeTerminalObserved !== true || recomputed.sameDocumentOwnerRetirement !== true ||
      recomputed.ownerDomAtFetch?.ruleOwner !== 'feature-catalog-generated-fields' ||
      recomputed.ownerDomAtFetch?.selector !== '#feature-catalog-search' ||
      recomputed.ownerDomAtFetch?.status !== 'unique' || recomputed.ownerDomAtFetch?.connectedAtFetch !== true ||
      recomputed.ownerDomAtAbort?.detachedAtAbort !== true || recomputed.ownerDomAtAbort?.connectedAtAbort !== false) return null;

  const close = recomputed.ownerRetirementAction;
  const requestStartedAt = chronology[0].observedAt;
  const failedAt = chronology[1].observedAt;
  if (!close || close.type !== 'click' || close.isTrusted !== true ||
      !['native-event-isTrusted-true', 'trusted-interaction-list-membership'].includes(close.trustEvidence) ||
      close.closestButton?.testId !== 'construction-close-operation-editor' ||
      close.closestButton?.accessibleLabel !== 'Close operation editor' ||
      !Number.isFinite(close.at) || requestStartedAt > close.at ||
      !Number.isFinite(recomputed.controllerAbortedAt) || recomputed.controllerAbortedAt < close.at ||
      recomputed.controllerAbortedAt > failedAt ||
      !Number.isFinite(recomputed.ownerDomAtAbort.detachedObservedAt) ||
      recomputed.ownerDomAtAbort.detachedObservedAt < close.at ||
      recomputed.ownerDomAtAbort.detachedObservedAt > recomputed.controllerAbortedAt ||
      !Number.isFinite(recomputed.fetchSettledAt) || recomputed.fetchSettledAt < recomputed.controllerAbortedAt ||
      recomputed.fetchSettledAt > failedAt ||
      !Number.isFinite(recomputed.fetchSettlementObservedAt) ||
      recomputed.fetchSettlementObservedAt < recomputed.controllerAbortedAt ||
      recomputed.fetchSettlementObservedAt > failedAt) return null;

  const actionId = record.requestTimeline?.action?.id;
  const sourceActions = (report.actions ?? []).filter(action => action.id === actionId);
  const closeActions = (report.actions ?? []).filter(action => Number.isFinite(action.startedAtEpochMs) &&
    Number.isFinite(action.finishedAtEpochMs) && action.startedAtEpochMs <= close.at &&
    close.at <= action.finishedAtEpochMs);
  if (sourceActions.length !== 1 || closeActions.length !== 1 || closeActions[0].status !== 'passed') return null;
  const sourceAction = sourceActions[0];
  if (!Number.isFinite(sourceAction.startedAtEpochMs) ||
      sourceAction.startedAtEpochMs > requestStartedAt || sourceAction.finishedAtEpochMs < requestStartedAt) return null;
  if (sourceAction.id === closeActions[0].id) {
    if (sourceAction.status !== 'passed') return null;
  } else if (sourceAction.status !== 'passed' || !Number.isFinite(sourceAction.finishedAtEpochMs) ||
      sourceAction.finishedAtEpochMs > close.at) {
    return null;
  }

  return {
    kind: 'same-document-owner-retirement',
    endpoint: 'schema-fields',
    requestId,
    browserRequestId: record.playwrightRequestId,
    project: target.project,
    explorer: target.explorer,
    owner: 'feature-catalog-generated-fields',
    selector: '#feature-catalog-search',
    controllerId: recomputed.controllerId,
    controllerAbortedAt: recomputed.controllerAbortedAt,
    closeAt: close.at,
    requestFailedAt: failedAt,
    associatedAction: { id: sourceAction.id, status: sourceAction.status, finishedAtEpochMs: sourceAction.finishedAtEpochMs },
    reason: 'the exact generated-field catalog owner was retired by trusted Close after its associated action completed',
  };
};

const classifyReportNetworkRecord = (report, record) => {
  if (isCodedSourceColumnReport(report) && record.errorText === 'net::ERR_ABORTED') {
    return exactValidatedObsoleteRead(report, record) ? 'cancelled' : 'unexpected-error';
  }
  if (validatedRootQuantityValidationRecord(report, record)) return 'expected-validated-http';
  if (expectedSchemaFieldsOwnerRetirement(report, record)) return 'cancelled';
  return classifyNetworkRecord(record);
};

export const classifyNetworkRecord = (record) => {
  if (record.kind === 'exception' || record.kind === 'console-error' || record.internalError) return 'unexpected-error';
  if (record.kind === 'asset-failure') return 'incidental-asset';
  if (record.injectedFault && record.status === 422 && record.injectedStatus === 422) return 'expected-injected';
  if (record.status >= 400 || record.internalError) return 'unexpected-error';
  if (record.errorText === 'net::ERR_ABORTED' && record.canceled) return 'cancelled';
  if (record.injectedFault && record.errorText) return 'expected-injected';
  if (record.errorText) return 'unexpected-error';
  return 'ok';
};

export const isActionable = (snapshot) =>
  Boolean(snapshot?.visible)
  && !snapshot?.disabled
  && snapshot?.ariaDisabled !== 'true'
  && snapshot?.pointerEvents !== 'none'
  && Boolean(snapshot?.receivesPointer);

export const finishReport = (report) => {
  const expectedOwnerRetirements = report.network.flatMap(record => {
    const evidence = expectedSchemaFieldsOwnerRetirement(report, record);
    return evidence ? [evidence] : [];
  });
  if (expectedOwnerRetirements.length) report.expectedOwnerRetirements = expectedOwnerRetirements;
  else delete report.expectedOwnerRetirements;
  const unexpected = report.network.filter((record) => classifyReportNetworkRecord(report, record) === 'unexpected-error');
  if (unexpected.length) {
    report.errors.push(...unexpected.map((record) => ({ kind: 'unexpected-network', ...record })));
    recordCheck(report, 'correctness', 'no unexpected network, module, or browser errors', false, { count: unexpected.length });
  } else if (report.network.some((record) => classifyReportNetworkRecord(report, record) === 'expected-injected')) {
    report.errors.push(...report.network.filter((record) => classifyReportNetworkRecord(report, record) === 'expected-injected').map((record) => ({ kind: 'expected-injected', ...record })));
  }
  const failed = report.assertions.some((assertion) => assertion.status === 'failed');
  const passed = report.assertions.some((assertion) => assertion.status === 'passed');
  const unreachable = report.dimensions && Object.values(report.dimensions).some((dimension) => dimension.status === 'unreachable');
  report.missingRequiredChecks = report.requiredChecks.length
    ? report.requiredChecks.filter((name) => !report.assertions.some((assertion) => assertion.name === name && assertion.status === 'passed'))
    : ['case requirements are missing'];
  report.status = failed ? 'failed' : unreachable ? 'unreachable' : !passed ? 'untested' : report.missingRequiredChecks.length ? 'partial' : 'passed';
  report.finishedAt = new Date().toISOString();
  return report;
};

export const adjudicatePendingLifecycle = (report) => {
  if (report.lifecycle?.status !== 'pending-final-adjudication') return report;
  report.lifecycle.status = report.status === 'passed' ? 'passed' : 'failed';
  report.lifecycle.finalReportStatus = report.status;
  if (report.lifecycle.status === 'failed') {
    const unexpectedNetwork = (report.network ?? [])
      .filter((record) => classifyReportNetworkRecord(report, record) === 'unexpected-error')
      .map(({ kind, message, status, method, url, errorText }) => ({
        kind,
        ...(message === undefined ? {} : { message }),
        ...(status === undefined ? {} : { status }),
        ...(method === undefined ? {} : { method }),
        ...(url === undefined ? {} : { url }),
        ...(errorText === undefined ? {} : { errorText }),
      }));
    report.lifecycle.failure ??= {
      finalReportStatus: report.status,
      missingRequiredChecks: [...(report.missingRequiredChecks ?? [])],
      unexpectedNetwork,
    };
  }
  return report;
};

export const writeReport = (path, report) => {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
};
