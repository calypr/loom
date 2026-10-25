import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';

const clone = value => value === undefined ? null : structuredClone(value);

const eventStamp = mountEpoch => ({
  atMonotonicMs: performance.now(),
  atEpochMs: Date.now(),
  mountEpoch,
});

export const createTemporalFilterCapture = ({ page, networkObserver }) => {
  assert(page && networkObserver, 'Temporal-filter capture requires the workflow Page and network observer.');
  const requestStartIndex = networkObserver.requests.length;
  const requestStamps = new WeakMap();
  const extraResponseBodies = new WeakMap();
  const builderReads = [];
  const domObservations = [];
  const steps = [];
  let mountEpoch = 0;
  let finished = false;

  const onRequest = request => {
    const entry = networkObserver.byRequest.get(request);
    if (entry) requestStamps.set(entry, eventStamp(mountEpoch));
  };
  const onFrameNavigated = frame => {
    if (frame === page.mainFrame()) mountEpoch += 1;
  };
  page.on('request', onRequest);
  page.on('framenavigated', onFrameNavigated);

  const beginBuilderRead = () => ({ readId: randomUUID(), ...eventStamp(mountEpoch) });

  const finishBuilderRead = (started, response, body) => {
    const requestId = response.headers.get('x-request-id');
    const read = {
      readId: started.readId,
      capturedAtMonotonicMs: started.atMonotonicMs,
      capturedAtEpochMs: started.atEpochMs,
      mountEpoch: started.mountEpoch,
      status: response.status,
      ...(typeof requestId === 'string' && requestId.trim() ? { requestId } : {}),
      catalogGeneration: body.catalog?.generation ?? null,
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
      const rowIds = idIndex < 0 ? [] : rows.map(row => {
        const cells = [...row.querySelectorAll('[role="cell"], td')];
        return String(cells[idIndex]?.innerText ?? '').trim();
      }).filter(Boolean);
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
    proposalCaptureIds = [],
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
      proposalCaptureIds: [...proposalCaptureIds],
      beforeReadIndex,
      afterReadIndex,
      domObservationIndex,
    };
    steps.push(step);
    return step;
  };

  const waitForRequest = (predicate, { fromIndex = requestStartIndex, timeoutMs = 5_000 } = {}) =>
    networkObserver.waitForResponse(entry => entry.index >= fromIndex && predicate(entry), { fromIndex, timeoutMs });

  const requestsSince = (fromIndex, predicate = () => true) =>
    networkObserver.requests.slice(fromIndex).filter(predicate);

  const settleRequests = async entries => {
    await Promise.all(entries.map(entry => networkObserver.waitForResponse(candidate => candidate === entry, {
      fromIndex: entry.index,
      timeoutMs: 5_000,
    })));
  };

  const associateResponseBody = (entry, body) => {
    assert(networkObserver.requests.includes(entry), 'Response body must belong to an observed Playwright request.');
    extraResponseBodies.set(entry, clone(body));
  };

  const finish = async metadata => {
    assert(!finished, 'Temporal-filter capture can only finish once.');
    const entries = networkObserver.requests.slice(requestStartIndex);
    await settleRequests(entries);
    for (const entry of entries) {
      assert(requestStamps.has(entry), `Observed request ${entry.browserRequestId} is missing its request-event timestamp.`);
    }
    finished = true;
    page.off('request', onRequest);
    page.off('framenavigated', onFrameNavigated);
    const requestEvents = entries.map(entry => {
      const stamp = requestStamps.get(entry);
      const event = {
        browserRequestId: entry.browserRequestId,
        ...(typeof entry.requestId === 'string' ? { requestId: entry.requestId } : {}),
        method: entry.method,
        path: entry.path,
        status: entry.status ?? null,
        request: clone(networkObserver.requestBody(entry)),
        response: clone(extraResponseBodies.has(entry) ? extraResponseBodies.get(entry) : networkObserver.responseBody(entry)),
        startedAt: entry.startedAt,
        completedAt: entry.completedAt ?? null,
        isMutation: Boolean(entry.isMutation),
        atMonotonicMs: stamp.atMonotonicMs,
        atEpochMs: stamp.atEpochMs,
        mountEpoch: stamp.mountEpoch,
      };
      return event;
    });
    return {
      schemaVersion: 1,
      scenario: 'basic-temporal-filter',
      ...metadata,
      requestEvents,
      builderReads,
      domObservations,
      steps,
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
    requestsSince,
    settleRequests,
    associateResponseBody,
    finish,
  };
};
