import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import {
  TEMPORAL_CAPTURE_DRAIN_TIMEOUT_MS,
  validateTemporalPendingReadScope,
} from './temporal-filter-pending-read.mjs';
import { classifyCapturedTransport } from './transport-terminal.mjs';

const clone = value => value === undefined ? null : structuredClone(value);
const jsonSafe = (value, label = 'capture checkpoint', ancestors = new Set()) => {
  if (value === undefined || value === null) return null;
  if (typeof value === 'string' || typeof value === 'boolean') return value;
  if (typeof value === 'number') {
    assert(Number.isFinite(value), `${label} must not contain non-finite numbers.`);
    return value;
  }
  assert(typeof value === 'object', `${label} must contain only JSON values.`);
  assert(!ancestors.has(value), `${label} must not contain cycles.`);
  ancestors.add(value);
  let result;
  if (Array.isArray(value)) {
    result = value.map((item, index) => jsonSafe(item, `${label}[${index}]`, ancestors));
  } else {
    const prototype = Object.getPrototypeOf(value);
    assert(prototype === Object.prototype || prototype === null,
      `${label} must not contain non-JSON object instances.`);
    result = Object.fromEntries(Object.entries(value).map(([key, item]) =>
      [key, jsonSafe(item, `${label}.${key}`, ancestors)]));
  }
  ancestors.delete(value);
  return result;
};

const eventStamp = mountEpoch => ({
  atMonotonicMs: performance.now(),
  atEpochMs: Date.now(),
  mountEpoch,
});

export const remainingDrainWaitMs = (deadlineMonotonicMs, nowMonotonicMs) => {
  const remainingMs = deadlineMonotonicMs - nowMonotonicMs;
  return remainingMs > 0 ? Math.ceil(remainingMs) : 0;
};

export const createTemporalFilterCapture = ({
  page,
  networkObserver,
  initialMountEpoch = 0,
  finishDrainTimeoutMs = TEMPORAL_CAPTURE_DRAIN_TIMEOUT_MS,
}) => {
  assert(page && networkObserver, 'Temporal-filter capture requires the workflow Page and network observer.');
  assert(Number.isInteger(initialMountEpoch) && initialMountEpoch >= 0,
    'Temporal-filter capture mount epoch must be a nonnegative integer.');
  assert(Number.isInteger(finishDrainTimeoutMs) && finishDrainTimeoutMs > 0 &&
    finishDrainTimeoutMs <= TEMPORAL_CAPTURE_DRAIN_TIMEOUT_MS,
  'Temporal-filter capture drain must be positive and cannot exceed the production five-second budget.');
  const requestStartIndex = networkObserver.requests.length;
  const requestStamps = new WeakMap();
  const responseMountEpochs = new WeakMap();
  const failureMountEpochs = new WeakMap();
  const extraResponseBodies = new WeakMap();
  const builderReads = [];
  const domObservations = [];
  const steps = [];
  let mountEpoch = initialMountEpoch;
  let finished = false;

  const requestEvent = entry => {
    const stamp = requestStamps.get(entry);
    assert(stamp, `Observed request ${entry.browserRequestId} is missing its request-event timestamp.`);
    const responseHeadersObserved = entry.status !== undefined && entry.status !== null &&
      Number.isFinite(entry.responseReceivedAt);
    const terminal = classifyCapturedTransport({
      responseHeadersObserved,
      requestFailed: Boolean(entry.failure),
      networkComplete: Boolean(entry.networkComplete),
      responseCaptureComplete: Boolean(entry.responseCaptureComplete),
    });
    return {
      index: entry.index,
      browserRequestId: entry.browserRequestId,
      ...(typeof entry.requestId === 'string' ? { requestId: entry.requestId } : {}),
      method: entry.method,
      origin: entry.origin ?? null,
      path: entry.path,
      status: entry.status ?? null,
      request: clone(networkObserver.requestBody(entry)),
      response: clone(extraResponseBodies.has(entry) ? extraResponseBodies.get(entry) : networkObserver.responseBody(entry)),
      startedAt: entry.startedAt,
      responseReceivedAt: entry.responseReceivedAt ?? null,
      completedAt: entry.completedAt ?? null,
      responseMountEpoch: responseMountEpochs.get(entry) ?? null,
      failureMountEpoch: failureMountEpochs.get(entry) ?? null,
      responseObservedBeforeFailure: terminal.responseObservedBeforeFailure,
      isMutation: Boolean(entry.isMutation),
      failure: clone(entry.failure),
      readCancellationDiagnostic: Boolean(entry.readCancellationDiagnostic),
      responseCapture: entry.responseCapture ?? null,
      responseCaptureComplete: Boolean(entry.responseCaptureComplete),
      responseBodyCaptured: Boolean(entry.responseBodyCaptured),
      ...(entry.responseCaptureError ? { responseCaptureError: entry.responseCaptureError } : {}),
      networkComplete: Boolean(entry.networkComplete),
      settlement: terminal.settlement,
      atMonotonicMs: stamp.atMonotonicMs,
      atEpochMs: stamp.atEpochMs,
      mountEpoch: stamp.mountEpoch,
    };
  };

  const checkpoint = metadata => {
    const entries = networkObserver.requests.slice(requestStartIndex);
    const at = eventStamp(mountEpoch);
    const requestEvents = entries.map(requestEvent);
    const pending = requestEvents.filter(event => event.settlement === 'pending');
    const pendingResponseReads = requestEvents.filter(event => event.settlement === 'response-body-pending');
    const checkpointData = jsonSafe({
      schemaVersion: 1,
      checkpointId: randomUUID(),
      checkpointedAtEpochMs: at.atEpochMs,
      checkpointedAtMonotonicMs: at.atMonotonicMs,
      requestStartIndex,
      requestCount: requestEvents.length,
      afterObserverIndex: requestEvents.at(-1)?.index ?? requestStartIndex - 1,
      pendingObserverIndexes: pending.map(event => event.index),
      pendingBrowserRequestIds: pending.map(event => event.browserRequestId),
      pendingResponseReadObserverIndexes: pendingResponseReads.map(event => event.index),
      ...metadata,
      requestEvents,
      builderReads,
      domObservations,
      steps,
    });
    assert.doesNotThrow(() => JSON.stringify(checkpointData), 'Capture checkpoint must be JSON-safe.');
    return checkpointData;
  };

  const onRequest = request => {
    const entry = networkObserver.byRequest.get(request);
    if (entry) requestStamps.set(entry, eventStamp(mountEpoch));
  };
  const onFrameNavigated = frame => {
    if (frame === page.mainFrame()) mountEpoch += 1;
  };
  const onResponse = response => {
    const entry = networkObserver.byRequest.get(response.request());
    if (entry) responseMountEpochs.set(entry, mountEpoch);
  };
  const onRequestFailed = request => {
    const entry = networkObserver.byRequest.get(request);
    if (entry) failureMountEpochs.set(entry, mountEpoch);
  };
  page.on('request', onRequest);
  page.on('framenavigated', onFrameNavigated);
  page.on('response', onResponse);
  page.on('requestfailed', onRequestFailed);

  const beginBuilderRead = () => ({ readId: randomUUID(), ...eventStamp(mountEpoch) });

  const finishBuilderRead = (started, response, body, target) => {
    assert(target && typeof target.path === 'string' && target.path.length > 0 &&
      typeof target.project === 'string' && target.project.length > 0 &&
      typeof target.explorerId === 'string' && target.explorerId.length > 0,
    'A Builder read must retain the exact requested path, project, and Explorer identity.');
    const observed = eventStamp(mountEpoch);
    const requestId = response.headers.get('x-request-id');
    const read = {
      readId: started.readId,
      requestMethod: 'GET',
      requestPath: target.path,
      requestProject: target.project,
      requestExplorerId: target.explorerId,
      requestStartedAtMonotonicMs: started.atMonotonicMs,
      requestStartedAtEpochMs: started.atEpochMs,
      requestMountEpoch: started.mountEpoch,
      capturedAtMonotonicMs: observed.atMonotonicMs,
      capturedAtEpochMs: observed.atEpochMs,
      mountEpoch: observed.mountEpoch,
      status: response.status,
      ...(typeof requestId === 'string' && requestId.trim() ? { requestId } : {}),
      catalogGeneration: body.catalog?.generation ?? null,
      snapshotToken: body.catalog?.snapshotToken ?? null,
      draftVersion: body.draftVersion ?? null,
      draftDigest: body.draftDigest ?? null,
      workspaceDocuments: clone(body.workspace?.documents ?? []),
    };
    builderReads.push(read);
    return read;
  };

  const captureDOM = async ({ resourceType }) => {
    const started = eventStamp(mountEpoch);
    const dom = await page.evaluate(type => {
      const visible = element => Boolean(element && element.getClientRects().length);
      const proposalPanel = document.querySelector('[data-testid="construction-proposal-panel"]');
      const choicePanel = document.querySelector('[data-testid="construction-choice-proposal-panel"]');
      const activePanel = [proposalPanel, choicePanel].find(visible) ?? null;
      const proposalPreview = document.querySelector('[data-testid="construction-proposal-preview"]');
      const constructionPreview = document.querySelector('[data-testid="construction-preview"]');
      const preview = visible(proposalPreview) ? proposalPreview : constructionPreview;
      const control = document.querySelector('[data-testid="construction-filter-editor"] input');
      const condition = document.querySelector('select[aria-label="Condition"]');
      const column = document.querySelector('select[aria-label="Column"]');
      const tableContainer = visible(proposalPreview) ? proposalPreview : constructionPreview;
      const table = visible(tableContainer)
        ? tableContainer.matches('table, [role="table"]')
          ? tableContainer
          : tableContainer.querySelector('table, [role="table"]')
        : null;
      const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim().toLowerCase();
      const rows = [...(table?.querySelectorAll('[role="row"], tbody tr') ?? [])]
        .filter(row => visible(row) && row.querySelector('[role="cell"], td'));
      const headers = [...(table?.querySelectorAll('[role="columnheader"], thead th') ?? [])]
        .map(cell => {
          const text = cell.querySelector('span')?.textContent ?? cell.innerText ?? cell.textContent;
          return normalize(String(text ?? '').split(/\r?\n/, 1)[0]);
        });
      const idIndex = headers.findIndex(value => value === `${type.toLowerCase()} id`);
      const rowIds = idIndex < 0 ? [] : rows.flatMap(row => {
        const cells = [...row.querySelectorAll('[role="cell"], td')];
        const idCell = cells[idIndex];
        if (!idCell || idCell.hasAttribute('colspan')) return [];
        const id = String(idCell.innerText ?? '').trim();
        return id ? [id] : [];
      });
      const panel = activePanel;
      return {
        proposalStatus: panel?.dataset.proposalStatus ?? null,
        proposalId: panel?.dataset.proposalId ?? null,
        receiptId: preview?.dataset.previewReceiptId ?? null,
        outputId: preview?.dataset.previewOutputId ?? null,
        columnId: column?.value || null,
        inputType: control?.getAttribute('type') ?? null,
        inputValue: control?.value ?? null,
        operator: condition?.value || null,
        rowIds,
      };
    }, resourceType);
    const observation = {
      capturedAtMonotonicMs: started.atMonotonicMs,
      capturedAtEpochMs: started.atEpochMs,
      mountEpoch: started.mountEpoch,
      ...dom,
    };
    domObservations.push(observation);
    return domObservations.length - 1;
  };

  const recordStep = ({
    label,
    requestEntry = null,
    relatedMutationCaptureIds = [],
    proposalCaptureIds = [],
    reconcileCaptureIds = [],
    beforeReadIndex = null,
    afterReadIndex = null,
    domObservationIndex = null,
  }) => {
    assert(!finished, 'Cannot add a temporal-filter step after capture is finished.');
    for (const index of [beforeReadIndex, afterReadIndex]) {
      assert(index === null || (Number.isInteger(index) && index >= 0 && index < builderReads.length),
        `${label} must reference an existing Builder read or null.`);
    }
    assert(domObservationIndex === null || (Number.isInteger(domObservationIndex) &&
      domObservationIndex >= 0 && domObservationIndex < domObservations.length),
    `${label} must reference an existing DOM observation or null.`);
    if (requestEntry) assert(requestStamps.has(requestEntry), `${label} request must have a request-event timestamp.`);
    assert(Array.isArray(relatedMutationCaptureIds), `${label} related mutation links must be an array.`);
    assert.equal(new Set(relatedMutationCaptureIds).size, relatedMutationCaptureIds.length,
      `${label} related mutation links must not repeat a physical request.`);
    if (requestEntry) assert(!relatedMutationCaptureIds.includes(requestEntry.browserRequestId),
      `${label} related mutation links cannot repeat its primary request.`);
    for (const captureId of relatedMutationCaptureIds) {
      const matches = networkObserver.requests.slice(requestStartIndex)
        .filter(entry => entry.browserRequestId === captureId);
      assert.equal(matches.length, 1, `${label} related mutation ${captureId} must identify one actual observer request.`);
      assert(requestStamps.has(matches[0]), `${label} related mutation ${captureId} must have an actual request-event timestamp.`);
      assert.equal(Boolean(matches[0].isMutation), true,
        `${label} related request ${captureId} must be observer-classified as a mutation.`);
    }
    const stamp = requestEntry
      ? requestStamps.get(requestEntry)
      : domObservationIndex === null
        ? eventStamp(mountEpoch)
        : {
            atMonotonicMs: domObservations[domObservationIndex].capturedAtMonotonicMs,
            atEpochMs: domObservations[domObservationIndex].capturedAtEpochMs,
            mountEpoch: domObservations[domObservationIndex].mountEpoch,
          };
    const step = {
      label,
      atMonotonicMs: stamp.atMonotonicMs,
      atEpochMs: stamp.atEpochMs,
      mountEpoch: stamp.mountEpoch,
      requestCaptureId: requestEntry?.browserRequestId ?? null,
      relatedMutationCaptureIds: [...relatedMutationCaptureIds],
      proposalCaptureIds: [...proposalCaptureIds],
      reconcileCaptureIds: [...reconcileCaptureIds],
      beforeReadIndex,
      afterReadIndex,
      domObservationIndex,
    };
    steps.push(step);
    return step;
  };

  const waitForRequest = (predicate, { fromIndex = requestStartIndex, timeoutMs = 5_000 } = {}) =>
    networkObserver.waitForResponse(entry => entry.index >= fromIndex && predicate(entry), { fromIndex, timeoutMs });

  const requestWindow = fromIndex => {
    assert(Number.isInteger(fromIndex) && fromIndex >= requestStartIndex,
      'A temporal-filter request window must start at or after capture began.');
    const endIndex = networkObserver.requests.length;
    assert(fromIndex <= endIndex, 'A temporal-filter request window cannot start beyond the observer ledger.');
    return { entries: networkObserver.requests.slice(fromIndex, endIndex), endIndex };
  };

  const requestsSince = (fromIndex, predicate = () => true) =>
    networkObserver.requests.slice(fromIndex).filter(predicate);

  const settleRequests = async (entries, { timeoutMs = TEMPORAL_CAPTURE_DRAIN_TIMEOUT_MS } = {}) => {
    await Promise.all(entries.map(entry => networkObserver.waitForResponse(candidate => candidate === entry, {
      fromIndex: entry.index,
      timeoutMs,
    })));
  };

  const associateResponseBody = (entry, body) => {
    assert(networkObserver.requests.includes(entry), 'Response body must belong to an observed Playwright request.');
    extraResponseBodies.set(entry, clone(body));
  };

  const finish = async metadata => {
    assert(!finished, 'Temporal-filter capture can only finish once.');
    assert(typeof metadata?.checkpointId === 'string' && metadata.checkpointId.length > 0,
      'Temporal-filter capture finish must bind its pre-drain checkpoint identity.');
    const drainStartedAtMonotonicMs = performance.now();
    const drainStartedAtEpochMs = Date.now();
    const drainDeadlineMonotonicMs = drainStartedAtMonotonicMs + finishDrainTimeoutMs;
    const settledEntries = new Set();
    const acceptedPendingEntries = new Set();
    let finalDOMObservationIndex = null;
    for (;;) {
      const endIndex = networkObserver.requests.length;
      const entries = networkObserver.requests.slice(requestStartIndex, endIndex);
      for (const entry of entries) {
        if (entry.networkComplete && entry.responseCaptureComplete) settledEntries.add(entry);
      }
      const pending = entries.filter(entry => !settledEntries.has(entry) && !acceptedPendingEntries.has(entry));
      if (pending.length > 0) {
        const remainingMs = remainingDrainWaitMs(drainDeadlineMonotonicMs, performance.now());
        if (remainingMs > 0) {
          const outcomes = await Promise.allSettled(pending.map(entry => networkObserver.waitForResponse(
            candidate => candidate === entry, { fromIndex: entry.index, timeoutMs: remainingMs })));
          outcomes.forEach((outcome, index) => {
            if (outcome.status === 'fulfilled') settledEntries.add(pending[index]);
          });
          const unexpectedFailure = outcomes.find(outcome => outcome.status === 'rejected' &&
            !/^Timed out waiting for the scoped response after \d+ms\.$/u.test(String(outcome.reason?.message ?? outcome.reason)));
          if (unexpectedFailure) throw unexpectedFailure.reason;
          if (performance.now() < drainDeadlineMonotonicMs) continue;
        }

        const currentEntries = networkObserver.requests.slice(requestStartIndex);
        for (const entry of currentEntries) {
          if (entry.networkComplete && entry.responseCaptureComplete) settledEntries.add(entry);
        }
        const unresolved = currentEntries.filter(entry => !settledEntries.has(entry));
        const partialResponseReads = unresolved.filter(entry => entry.status !== undefined && entry.status !== null);
        assert.deepEqual(partialResponseReads.map(entry => entry.index), [],
          'Temporal-filter capture cannot exit while a response-body read is pending.');
        for (const entry of unresolved) {
          const stamp = requestStamps.get(entry);
          assert(stamp && stamp.atMonotonicMs <= drainDeadlineMonotonicMs,
            `Request ${entry.browserRequestId} started after the bounded temporal capture drain.`);
          validateTemporalPendingReadScope(requestEvent(entry), metadata.pendingReadScope, builderReads);
          acceptedPendingEntries.add(entry);
        }
      }

      const linkedCaptureIds = new Set(steps.flatMap(step => step.reconcileCaptureIds));
      const lateReconciles = entries.filter(entry => entry.method === 'POST' &&
        entry.path.endsWith('/reconcile') && !linkedCaptureIds.has(entry.browserRequestId));
      const lateReceipts = new Set(lateReconciles
        .map(entry => entry.response?.receiptId)
        .filter(value => typeof value === 'string' && value.length > 0));
      const relevantResponseTimes = entries.filter(entry => entry.method === 'POST' &&
        (entry.path.endsWith('/reconcile') ||
          (entry.path.endsWith('/preview') && lateReceipts.has(entry.request?.receiptId))))
        .map(entry => entry.responseReceivedAt)
        .filter(Number.isFinite);
      const latestRelevantResponseAt = relevantResponseTimes.length > 0
        ? Math.max(...relevantResponseTimes)
        : Number.NEGATIVE_INFINITY;
      const currentFinalDOM = finalDOMObservationIndex === null
        ? null
        : domObservations[finalDOMObservationIndex];
      if (lateReconciles.length > 0 && (!currentFinalDOM ||
        currentFinalDOM.capturedAtEpochMs < latestRelevantResponseAt)) {
        assert(metadata.resourceType, 'Late reconcile evidence requires the workflow resource type for a final DOM read.');
        finalDOMObservationIndex = await captureDOM({ resourceType: metadata.resourceType });
        continue;
      }
      if (networkObserver.requests.length === endIndex) break;
    }
    const entries = networkObserver.requests.slice(requestStartIndex);
    const linkedCaptureIds = new Set(steps.flatMap(step => step.reconcileCaptureIds));
    const lateReconcileCaptureIds = entries.filter(entry => entry.method === 'POST' &&
      entry.path.endsWith('/reconcile') && !linkedCaptureIds.has(entry.browserRequestId))
      .map(entry => entry.browserRequestId);
    finished = true;
    page.off('request', onRequest);
    page.off('framenavigated', onFrameNavigated);
    page.off('response', onResponse);
    page.off('requestfailed', onRequestFailed);
    const requestEvents = entries.map(requestEvent);
    const pendingEvents = requestEvents.filter(event => event.settlement === 'pending');
    const pendingResponseReadEvents = requestEvents.filter(event => event.settlement === 'response-body-pending');
    assert.deepEqual(pendingResponseReadEvents.map(event => event.index), [],
      'Temporal-filter capture cannot leave response-body reads pending at its exit.');
    const acceptedPendingIndexes = new Set([...acceptedPendingEntries].map(entry => entry.index));
    assert(pendingEvents.every(event => acceptedPendingIndexes.has(event.index)),
    'Every unresolved temporal request must be retained by the accepted pending-read boundary.');
    const observedAtEpochMs = Date.now();
    const observedAtMonotonicMs = performance.now();
    const captureExit = {
      checkpointId: metadata.checkpointId,
      drainStartedAtEpochMs,
      drainStartedAtMonotonicMs,
      observedAtEpochMs,
      observedAtMonotonicMs,
      drainTimeoutMs: finishDrainTimeoutMs,
      deadlineReached: pendingEvents.length > 0,
      afterObserverIndex: requestEvents.at(-1)?.index ?? requestStartIndex - 1,
      requestCount: requestEvents.length,
      pendingObserverIndexes: pendingEvents.map(event => event.index),
      pendingBrowserRequestIds: pendingEvents.map(event => event.browserRequestId),
      pendingResponseReadObserverIndexes: [],
      scope: clone(metadata.pendingReadScope),
    };
    if (pendingEvents.length > 0) {
      assert(observedAtMonotonicMs - drainStartedAtMonotonicMs >= finishDrainTimeoutMs,
        'Pending temporal reads require the full bounded capture exit interval.');
    }
    return {
      schemaVersion: 1,
      scenario: 'basic-temporal-filter',
      ...metadata,
      requestEvents,
      builderReads,
      domObservations,
      steps,
      captureExit,
      lateReconcileCaptureIds,
      finalDOMObservationIndex,
    };
  };

  return {
    get mountEpoch() { return mountEpoch; },
    builderReads,
    domObservations,
    steps,
    beginBuilderRead,
    finishBuilderRead,
    captureDOM,
    recordStep,
    waitForRequest,
    requestWindow,
    requestsSince,
    settleRequests,
    associateResponseBody,
    checkpoint,
    finish,
  };
};
