import assert from 'node:assert/strict';

const AUTHORING_PREFIX = '/authoring/v2/';

const waitForPromises = (promises, timeoutMs) => new Promise((resolve, reject) => {
  let timer;
  Promise.race([
    Promise.all(promises),
    new Promise((_, fail) => {
      timer = setTimeout(() => fail(new Error(`authoring transport span did not settle within ${timeoutMs}ms`)), timeoutMs);
    }),
  ]).then(resolve, reject).finally(() => clearTimeout(timer));
});

/** Capture Policy One's complete setup and formal authoring transport span by exact Playwright Request identity. */
export const createPolicyOneTransportObserver = (page, { authoringPrefix = AUTHORING_PREFIX } = {}) => {
  const captures = [];
  const entriesByRequest = new WeakMap();
  const pendingRequests = new Set();
  const pendingResponseReads = new Set();
  let transportIndex = 0;
  let transportEventIndex = 0;
  let capturePhase = 'SETUP';
  let formalEntry = null;

  const captureRequest = request => {
    if (entriesByRequest.has(request)) return entriesByRequest.get(request);
    const url = new URL(request.url());
    const prefixIndex = url.pathname.indexOf(authoringPrefix);
    if (prefixIndex < 0) return undefined;
    let requestBody = null;
    try { requestBody = request.postDataJSON(); } catch { requestBody = null; }
    let resolveTerminal;
    const terminal = new Promise(resolve => { resolveTerminal = resolve; });
    const entry = {
      requestObject: request,
      terminal,
      resolveTerminal,
      record: {
        transportIndex: ++transportIndex,
        requestEventIndex: ++transportEventIndex,
        capturePhase,
        origin: url.origin,
        resourcePath: `${url.origin}${url.pathname}`,
        ownerScope: `${url.origin}${url.pathname.slice(0, prefixIndex)}`,
        method: request.method(),
        path: url.pathname.slice(prefixIndex + authoringPrefix.length),
        requestId: String(request.headers()['x-request-id'] ?? '').trim() || null,
        requestBody,
        settlement: 'pending',
        status: null,
        responseOwnerIndex: null,
        responseHeaders: null,
        responseReadError: null,
        responseObservedBeforeFailure: false,
        response: null,
      },
      responseObject: null,
      responseBodyFacts: null,
    };
    entriesByRequest.set(request, entry);
    captures.push(entry);
    pendingRequests.add(request);
    return entry;
  };

  const observeResponse = response => {
    const request = response.request();
    const url = new URL(request.url());
    if (!url.pathname.includes(authoringPrefix)) return;
    const entry = entriesByRequest.get(request);
    assert(entry, 'authoring transport response must retain its exact observed request object');
    assert.equal(entry.record.settlement, 'pending',
      `authoring transport ${entry.record.transportIndex} cannot receive response headers after terminal completion`);
    assert.equal(entry.responseObject, null,
      `authoring transport ${entry.record.transportIndex} must observe response headers at most once`);
    entry.responseObject = response;
    const responseRequestId = String(response.headers()['x-request-id'] ?? '').trim() || null;
    entry.record.responseHeaders = { status: response.status(), requestId: responseRequestId };
    let responseRead;
    responseRead = Promise.resolve().then(() => response.json()).then(body => {
      const preview = body?.preview ?? null;
      const outputIds = [
        ...(Array.isArray(body?.outputs) ? body.outputs.map(output => output?.outputId) : []),
        body?.outputId,
        preview?.outputId,
      ].filter(value => typeof value === 'string');
      const receiptIds = [body?.receiptId, body?.proposalId, preview?.receiptId]
        .filter(value => typeof value === 'string');
      entry.responseBodyFacts = {
        status: response.status(),
        requestId: responseRequestId ?? body?.requestId ?? null,
        commandId: body?.commandId ?? null,
        receiptId: body?.receiptId ?? body?.proposalId ?? preview?.receiptId ?? null,
        receiptIds: [...new Set(receiptIds)],
        outputIds: [...new Set(outputIds)],
        snapshotToken: body?.snapshotToken ?? preview?.snapshotToken ?? null,
        draftVersion: body?.draftVersion ?? null,
        draftDigest: body?.draftDigest ?? null,
        candidateWorkspaceDigest: body?.candidateWorkspaceDigest ?? null,
        intentDigest: body?.intentDigest ?? null,
        previewRowsPresent: Array.isArray(preview?.rows),
        frameSourceOptionsOutputId: body?.outputId ?? null,
        frameSourceOptionsSnapshotToken: body?.snapshotToken ?? null,
        frameSourceOptionsComplete: typeof body?.complete === 'boolean' ? body.complete : null,
        frameSourceOptionsTruncated: typeof body?.truncated === 'boolean' ? body.truncated : null,
        frameSourceOptionsSourceCount: Array.isArray(body?.sources) ? body.sources.length : null,
        frameSourceOptionsNextCursor: body?.nextCursor ?? null,
      };
      if (entry.record.settlement === 'pending') entry.record.responseBody = entry.responseBodyFacts;
      if (entry.record.settlement === 'response') {
        entry.record.response = entry.responseBodyFacts;
      } else if (entry.record.settlement === 'request-failed') {
        entry.record.responseBody = entry.responseBodyFacts;
      }
    }).catch(error => {
      entry.record.responseReadError = String(error);
      if (entry.record.settlement === 'response') {
        entry.record.response = {
          status: entry.record.responseHeaders.status,
          requestId: entry.record.responseHeaders.requestId,
          responseReadError: entry.record.responseReadError,
        };
      }
    }).finally(() => {
      pendingResponseReads.delete(responseRead);
    });
    pendingResponseReads.add(responseRead);
  };

  const settleRequestFinished = request => {
    const url = new URL(request.url());
    if (!url.pathname.includes(authoringPrefix)) return;
    const entry = entriesByRequest.get(request);
    assert(entry, 'authoring transport completion must retain its exact observed request object');
    assert.equal(entry.record.settlement, 'pending',
      `authoring transport ${entry.record.transportIndex} must settle exactly once`);
    assert(entry.record.responseHeaders,
      `authoring transport ${entry.record.transportIndex} must observe response headers before requestfinished`);
    entry.record.settlement = 'response';
    entry.record.settlementEventIndex = ++transportEventIndex;
    entry.record.responseOwnerIndex = entry.record.transportIndex;
    entry.record.status = entry.record.responseHeaders.status;
    entry.record.response = entry.responseBodyFacts ?? {
      status: entry.record.responseHeaders.status,
      requestId: entry.record.responseHeaders.requestId,
      ...(entry.record.responseReadError ? { responseReadError: entry.record.responseReadError } : {}),
    };
    pendingRequests.delete(request);
    entry.resolveTerminal();
  };

  const settleRequestFailed = request => {
    const url = new URL(request.url());
    if (!url.pathname.includes(authoringPrefix)) return;
    const entry = entriesByRequest.get(request);
    assert(entry, 'authoring transport settlement must retain its exact observed request object');
    assert.equal(entry.record.settlement, 'pending',
      `authoring transport ${entry.record.transportIndex} must settle exactly once`);
    entry.record.settlement = 'request-failed';
    entry.record.settlementEventIndex = ++transportEventIndex;
    entry.record.failure = request.failure()?.errorText ?? 'request failed without error detail';
    entry.record.responseObservedBeforeFailure = entry.record.responseHeaders !== null;
    entry.record.status = null;
    entry.record.responseOwnerIndex = null;
    entry.record.response = null;
    if (entry.responseBodyFacts) entry.record.responseBody = entry.responseBodyFacts;
    pendingRequests.delete(request);
    entry.resolveTerminal();
  };

  page.on('request', captureRequest);
  page.on('response', observeResponse);
  page.on('requestfinished', settleRequestFinished);
  page.on('requestfailed', settleRequestFailed);

  const waitUntilIdle = async ({ timeoutMs = 5000, allowPendingAtDeadline = false } = {}) => {
    const deadline = Date.now() + timeoutMs;
    while (true) {
      const pending = [...pendingRequests].map(request => entriesByRequest.get(request));
      const eventIndexBeforeQuietTurn = transportEventIndex;
      if (pending.length || pendingResponseReads.size) {
        const remaining = deadline - Date.now();
        if (remaining <= 0 && allowPendingAtDeadline && pending.length > 0 && pendingResponseReads.size === 0) {
          return {
            deadlineReached: true,
            pendingTransportIndices: pending.map(entry => entry.record.transportIndex).sort((left, right) => left - right),
          };
        }
        assert(remaining > 0, `authoring setup transports or response bodies remain unsettled: ${pending.map(entry =>
          `${entry.record.method} ${entry.record.path}#${entry.record.transportIndex}`).join(', ') ||
          `${pendingResponseReads.size} response body read(s)`}`);
        try {
          await waitForPromises([
            ...pending.map(entry => entry.terminal),
            ...pendingResponseReads,
          ], remaining);
        } catch (error) {
          if (allowPendingAtDeadline && Date.now() >= deadline &&
              pendingRequests.size > 0 && pendingResponseReads.size === 0) {
            return {
              deadlineReached: true,
              pendingTransportIndices: [...pendingRequests]
                .map(request => entriesByRequest.get(request).record.transportIndex)
                .sort((left, right) => left - right),
            };
          }
          throw error;
        }
        continue;
      }
      await new Promise(resolve => setImmediate(resolve));
      if (pendingRequests.size === 0 && pendingResponseReads.size === 0 &&
          transportEventIndex === eventIndexBeforeQuietTurn) {
        return { deadlineReached: false, pendingTransportIndices: [] };
      }
      assert(Date.now() < deadline, 'authoring transport activity did not become idle before the formal entry boundary');
    }
  };

  const beginFormalSpan = async (initialSavedState, { expectedEventIndex = transportEventIndex } = {}) => {
    assert.equal(capturePhase, 'SETUP', 'Policy One formal span can begin only once');
    await waitUntilIdle();
    assert.equal(pendingRequests.size, 0, 'all setup authoring requests must be terminal before the formal entry');
    assert.equal(pendingResponseReads.size, 0, 'all setup authoring response bodies must be read before the formal entry');
    assert.equal(transportEventIndex, expectedEventIndex,
      'refresh the actual initial saved snapshot after setup transports settle');
    assert(initialSavedState && typeof initialSavedState === 'object',
      'formal entry must bind the actual initial saved Builder snapshot');
    const setupMutations = captures.filter(entry => entry.record.capturePhase === 'SETUP'
      && entry.record.method === 'POST' && entry.record.path === 'commands');
    for (const entry of setupMutations) {
      const record = entry.record;
      assert.equal(record.settlement, 'response',
        `setup mutation ${record.transportIndex} must have a successful HTTP response before formal entry`);
      assert(record.status >= 200 && record.status < 300,
        `setup mutation ${record.transportIndex} must succeed before formal entry`);
      assert.equal(typeof record.requestBody?.commandId, 'string',
        `setup mutation ${record.transportIndex} must retain its exact commandId`);
      assert.equal(record.response?.commandId, record.requestBody.commandId,
        `setup mutation ${record.transportIndex} response must echo its exact commandId`);
      assert(Number.isInteger(record.response?.draftVersion) && record.response.draftVersion > 0,
        `setup mutation ${record.transportIndex} must retain its actual response draft version`);
      assert.equal(typeof record.response?.draftDigest, 'string',
        `setup mutation ${record.transportIndex} must retain its actual response draft digest`);
    }
    const lastSetupMutation = setupMutations.at(-1)?.record;
    if (lastSetupMutation) {
      assert.equal(lastSetupMutation.response.draftVersion, initialSavedState.draftVersion,
        'formal entry saved Builder version must include the final successful setup mutation');
      assert.equal(lastSetupMutation.response.draftDigest, initialSavedState.draftDigest,
        'formal entry saved Builder digest must include the final successful setup mutation');
    }
    formalEntry = {
      afterTransportIndex: transportIndex,
      afterEventIndex: transportEventIndex,
      initialSavedState: structuredClone(initialSavedState),
    };
    capturePhase = 'FORMAL';
    return structuredClone(formalEntry);
  };

  const entryForRequest = request => {
    const entry = entriesByRequest.get(request);
    assert(entry, 'semantic request must retain its exact observed request object');
    return entry;
  };

  const bindRequestCaptureId = (request, captureId) => {
    assert.equal(typeof captureId, 'string');
    assert(captureId.length > 0);
    const entry = entryForRequest(request);
    if (entry.record.requestCaptureId !== undefined) {
      assert.equal(entry.record.requestCaptureId, captureId,
        `authoring transport ${entry.record.transportIndex} cannot change its request capture ID`);
    }
    entry.record.requestCaptureId = captureId;
    return entry.record;
  };

  const transportIndexForRequest = request => entryForRequest(request).record.transportIndex;

  const finish = async ({ timeoutMs = 5000 } = {}) => {
    assert.equal(capturePhase, 'FORMAL', 'Policy One transport observer must finish after formal entry');
    const drain = await waitUntilIdle({ timeoutMs, allowPendingAtDeadline: true });
    const pending = [...pendingRequests].map(request => entriesByRequest.get(request));
    assert.deepEqual(pending.map(entry => entry.record.transportIndex).sort((left, right) => left - right),
      drain.pendingTransportIndices,
      'formal exit must retain the exact physical requests still pending at its boundary');
    assert.equal(pendingResponseReads.size, 0,
      'response-body reads must drain before the formal exit boundary');
    for (const entry of pending) {
      const record = entry.record;
      assert.equal(record.capturePhase, 'FORMAL',
        `setup transport ${record.transportIndex} must settle before formal exit`);
      assert.equal(record.responseHeaders, null,
        `pending transport ${record.transportIndex} cannot be accepted after response headers were observed`);
      assert.equal(record.responseReadError, null,
        `pending transport ${record.transportIndex} cannot hide an observed response-body error`);
      assert.equal(record.status, null,
        `pending transport ${record.transportIndex} cannot claim an HTTP status`);
      assert.equal(record.responseOwnerIndex, null,
        `pending transport ${record.transportIndex} cannot claim a response owner`);
      assert.equal(record.response, null,
        `pending transport ${record.transportIndex} cannot claim response facts`);
      assert.equal(record.settlementEventIndex, undefined,
        `pending transport ${record.transportIndex} cannot invent a terminal event`);
      assert.equal(record.failure, undefined,
        `pending transport ${record.transportIndex} cannot invent a cancellation`);
    }
    const formalExit = {
      afterTransportIndex: transportIndex,
      afterEventIndex: transportEventIndex,
      observedAt: Date.now(),
      drainTimeoutMs: timeoutMs,
      deadlineReached: drain.deadlineReached,
      pendingTransportIndices: drain.pendingTransportIndices,
      pendingResponseReadTransportIndices: [],
    };
    capturePhase = 'FINISHED';
    page.off('request', captureRequest);
    page.off('response', observeResponse);
    page.off('requestfinished', settleRequestFinished);
    page.off('requestfailed', settleRequestFailed);
    return {
      formalEntry: structuredClone(formalEntry),
      formalExit,
      captures: captures.map(entry => structuredClone(entry.record)),
    };
  };

  return {
    beginFormalSpan,
    bindRequestCaptureId,
    get eventIndex() { return transportEventIndex; },
    entryForRequest,
    finish,
    transportIndexForRequest,
    waitUntilIdle,
  };
};
