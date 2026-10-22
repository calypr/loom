import { randomUUID } from 'node:crypto';
import { test as base, expect } from '@playwright/test';

const required = (name) => {
  const value = String(process.env[name] ?? '').trim();
  if (!value) throw new Error(name + ' is required; run the fast global setup first');
  return value;
};

export { expect };

export const captureJourneyProvenance = () => ({
  sourceProfile: 'journey',
  sourceCommit: required('LOOM_SOURCE_COMMIT'),
  contractSourceCommit: required('LOOM_CONTRACT_SOURCE_COMMIT'),
  sourceFingerprint: required('LOOM_SOURCE_FINGERPRINT'),
});

const AUTHORING_PREFIX = '/authoring/v2/';
const COMMAND_ID_PATHS = new Set(['construction-choice-proposals', 'commands']);
let authoringObservationSequence = 0;

export const captureAuthoringRequest = (request, captureId = randomUUID()) => {
  const requestUrl = new URL(request.url());
  const path = requestUrl.pathname.split(AUTHORING_PREFIX)[1] ?? requestUrl.pathname;
  const requestId = String(request.headers()['x-request-id'] ?? '').trim() || null;
  const record = {
    method: request.method(),
    path,
    requestId,
    captureId,
  };
  if (COMMAND_ID_PATHS.has(path)) {
    let body = null;
    try {
      body = request.postDataJSON();
    } catch {
      body = null;
    }
    record.commandId = body?.commandId ?? null;
  }
  return record;
};

export const captureAuthoringExchange = async (request, observedResponse = undefined, captureId) => {
  const requestRecord = captureAuthoringRequest(request, captureId);
  const response = observedResponse === undefined ? await request.response() : observedResponse;
  if (!response) return { request: requestRecord, response: null, payload: null, ok: false, rawResponse: null };

  const payload = await response.json().catch(() => null);
  const preview = payload?.preview ?? payload;
  const rows = Array.isArray(preview?.rows) ? preview.rows : null;
  const rawRowCount = preview?.rowCount ?? rows?.length ?? null;
  const rawProposalDraftVersion = payload?.draftVersion ?? null;
  const proposalDraftVersion = rawProposalDraftVersion === null
    ? null
    : Number(rawProposalDraftVersion);
  const responseRequestId = [
    response.headers()['x-request-id'],
    payload?.requestId,
    payload?.error?.requestId,
    payload?.diagnostics?.[0]?.requestId,
  ].find((value) => typeof value === 'string' && value.trim())?.trim() ?? null;
  const hasPreview = Boolean(preview && (
    preview.receiptId !== undefined
    || preview.outputId !== undefined
    || preview.rowCount !== undefined
    || Array.isArray(preview.rows)
  ));
  const rowCount = rawRowCount === null ? null : Number(rawRowCount);

  return {
    request: requestRecord,
    response: {
      status: response.status(),
      requestId: responseRequestId,
      captureId: requestRecord.captureId,
      ...(COMMAND_ID_PATHS.has(requestRecord.path) ? { commandId: payload?.commandId ?? null } : {}),
      proposalId: payload?.proposalId ?? null,
      proposalOutputId: payload?.outputId ?? null,
      proposalSnapshotToken: payload?.snapshotToken ?? null,
      proposalDraftVersion: Number.isFinite(proposalDraftVersion) ? proposalDraftVersion : null,
      proposalDraftDigest: payload?.draftDigest ?? null,
      proposalPreviewStatus: payload?.previewStatus ?? null,
      preview: hasPreview ? {
        receiptId: preview.receiptId ?? null,
        outputId: preview.outputId ?? null,
        rowCount: Number.isFinite(rowCount) ? rowCount : null,
        rowsPresent: Array.isArray(preview?.rows),
      } : null,
      errorCode: payload?.error?.code ?? payload?.diagnostics?.[0]?.code ?? null,
    },
    payload,
    ok: response.ok(),
    rawResponse: response,
  };
};

export const observeAuthoringAttempt = (page, { method = 'POST', path, owner, timeout = 30_000 }) => {
  const matchesRequest = (request) => {
    const requestUrl = new URL(request.url());
    return request.method() === method
      && requestUrl.pathname.split(AUTHORING_PREFIX)[1] === path;
  };
  const started = page.waitForRequest(matchesRequest, { timeout }).then(async (request) => {
    const captureId = randomUUID();
    const responseEvent = new Promise((resolve) => {
      const cleanup = () => {
        page.off('response', onResponse);
        page.off('requestfailed', onRequestFailed);
      };
      const onResponse = (response) => {
        if (response.request() !== request) return;
        cleanup();
        resolve({
          response,
          sequence: ++authoringObservationSequence,
          observation: captureBuilderObservation(page),
        });
      };
      const onRequestFailed = (failedRequest) => {
        if (failedRequest !== request) return;
        cleanup();
        resolve({
          response: null,
          sequence: ++authoringObservationSequence,
          errorText: failedRequest.failure()?.errorText ?? null,
          observation: captureBuilderObservation(page),
        });
      };
      page.on('response', onResponse);
      page.on('requestfailed', onRequestFailed);
    });
    const sequence = ++authoringObservationSequence;
    return {
      owner: { kind: owner.kind, attempt: owner.attempt },
      request: captureAuthoringRequest(request, captureId),
      requestObservation: await captureBuilderObservation(page),
      rawRequest: request,
      responseEvent,
      sequence,
    };
  });
  const settled = started.then(async (attempt) => {
    const observedResponse = await attempt.responseEvent;
    const exchange = await captureAuthoringExchange(
      attempt.rawRequest,
      observedResponse.response,
      attempt.request.captureId,
    );
    return {
      owner: attempt.owner,
      request: exchange.request,
      response: exchange.response,
      requestObservation: attempt.requestObservation,
      requestSequence: attempt.sequence,
      responseSequence: observedResponse.sequence,
      settlement: observedResponse.response
        ? { kind: 'http-response', captureId: attempt.request.captureId }
        : {
          kind: 'request-failed',
          captureId: attempt.request.captureId,
          ...(observedResponse.errorText ? { errorText: observedResponse.errorText } : {}),
        },
      payload: exchange.payload,
      ok: exchange.ok,
      rawResponse: exchange.rawResponse,
      responseObservation: await observedResponse.observation,
    };
  });
  return { started, settled };
};

export const recordAuthoringAttempt = (page, actions, attempt, {
  requestLabel,
  requestEvent,
  responseLabel,
  responseErrorLabel = responseLabel,
  responseEvent,
}) => {
  const started = attempt.started.then((observed) => {
    const isChoicePreview = observed.request.path === 'construction-choice-proposals';
    actions.push({
      label: isChoicePreview ? 'ChoicePreview' : requestLabel,
      event: isChoicePreview ? 'choice-preview' : requestEvent,
      owner: observed.owner,
      request: observed.request,
      observation: observed.requestObservation,
    });
    return observed;
  });
  const settled = started.then(async (observed) => {
    const exchange = await attempt.settled;
    const isChoicePreview = exchange.request.path === 'construction-choice-proposals';
    const settledResponseLabel = isChoicePreview ? 'ChoicePreviewComplete' : responseLabel;
    const settledErrorLabel = isChoicePreview ? 'ChoicePreviewError' : responseErrorLabel;
    const settledResponseEvent = isChoicePreview ? 'choice-preview-response' : responseEvent;
    if (!exchange.response) {
      actions.push({
        label: settledErrorLabel,
        event: settledResponseEvent,
        owner: observed.owner,
        request: exchange.request,
        response: null,
        settlement: exchange.settlement,
        observation: exchange.responseObservation,
      });
      return { observed, exchange };
    }
    if (isChoicePreview) {
      const preview = exchange.response.preview;
      const completed = exchange.response.status >= 200
        && exchange.response.status < 300
        && exchange.response.errorCode == null
        && exchange.response.proposalPreviewStatus === 'READY'
        && typeof preview?.receiptId === 'string'
        && preview.receiptId.length > 0
        && typeof preview?.outputId === 'string'
        && preview.outputId.length > 0
        && preview.rowsPresent === true
        && typeof preview.rowCount === 'number'
        && Number.isFinite(preview.rowCount);
      actions.push({
        label: completed ? settledResponseLabel : settledErrorLabel,
        event: settledResponseEvent,
        owner: observed.owner,
        request: exchange.request,
        response: exchange.response,
        settlement: exchange.settlement,
        observation: exchange.responseObservation,
      });
      return { observed, exchange };
    }
    await page.waitForFunction(() => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return !panel || ['ready', 'error', 'needs-repair'].includes(panel.getAttribute('data-proposal-status'));
    }, undefined, { timeout: 30_000 });
    const settledObservation = await captureBuilderObservation(page);
    const panelStatus = settledObservation.status;
    const observation = combineAuthoringObservation(exchange, settledObservation);
    const typedError = Boolean(exchange.response && (
      exchange.response.status < 200
      || exchange.response.status >= 300
      || exchange.response.errorCode
      || exchange.response.proposalPreviewStatus === 'NEEDS_REPAIR'
    ));
    let label;
    if (panelStatus === 'ready' && !typedError) {
      label = settledResponseLabel;
    } else if (['error', 'needs-repair'].includes(panelStatus)) {
      label = settledErrorLabel;
    } else if (['absent', 'unknown'].includes(panelStatus)) {
      if (typedError) {
        label = settledErrorLabel;
      } else if (exchange.response?.status >= 200
        && exchange.response.status < 300
        && exchange.response.preview?.receiptId
        && typeof exchange.response.preview.rowsPresent === 'boolean') {
        label = settledResponseLabel;
      } else {
        throw new Error(`ambiguous ${requestEvent} settlement: no proposal status or typed preview result was observed`);
      }
    } else {
      throw new Error(`ambiguous ${requestEvent} settlement: proposal panel status ${panelStatus}`);
    }
    actions.push({
      label,
      event: settledResponseEvent,
      owner: observed.owner,
      request: exchange.request,
      response: exchange.response,
      settlement: exchange.settlement,
      observation,
    });
    return { observed, exchange };
  });
  return { started, settled };
};

export const captureBuilderObservation = async (page) => page.evaluate(() => {
  const attribute = (element, name) => {
    const value = element?.getAttribute(name) ?? '';
    return value === '' ? null : value;
  };
  const rowCount = (element) => {
    if (!element) return null;
    const raw = element.getAttribute('aria-rowcount');
    if (raw === null) return null;
    const value = Number(raw);
    return Number.isFinite(value) ? Math.max(0, value - 1) : null;
  };
  const numberAttribute = (element, name) => {
    const raw = element?.getAttribute(name);
    if (raw == null || raw.trim() === '') return null;
    const value = Number(raw);
    return Number.isFinite(value) ? value : null;
  };
  const sourcePreview = document.querySelector('[data-testid="construction-preview"]');
  const proposalPanel = document.querySelector('[data-testid="construction-proposal-panel"]');
  const applyButton = proposalPanel?.querySelector('[data-testid="construction-apply-proposal"]');
  const sourceTable = sourcePreview?.querySelector('[data-testid="preview-table-content"] [role="table"]');
  const sourceNativeTable = sourcePreview?.querySelector('table');
  const sourcePreviewError = sourcePreview?.querySelector('p[role="status"]')?.textContent
    ?.startsWith('Preview did not complete for this draft') ?? false;
  const sourceRenderedRows = sourcePreview?.querySelectorAll('.construction-preview-data-row').length ?? 0;
  const rowOperationEditor = proposalPanel?.closest('[data-testid="construction-row-operation-editor"]');
  const rowOperationPanel = rowOperationEditor?.querySelector('[data-testid="construction-proposal-panel"]');
  const rowOperationProposalPreview = rowOperationEditor?.querySelector('[data-testid="construction-proposal-preview"]');
  const proposalId = attribute(proposalPanel, 'data-proposal-id');
  const mainPreviewProposalId = attribute(sourcePreview, 'data-preview-proposal-id');
  const mainPreviewReceiptId = attribute(sourcePreview, 'data-preview-receipt-id');
  const rowOperationSurface = Boolean(
    proposalId
    && rowOperationEditor
    && rowOperationPanel === proposalPanel
    && rowOperationProposalPreview,
  );
  const mainTableSurface = Boolean(
    proposalId
    && mainPreviewProposalId === proposalId
    && mainPreviewReceiptId === proposalId,
  );
  const proposalPreviewSurface = rowOperationSurface === mainTableSurface
    ? 'unknown'
    : rowOperationSurface
      ? 'row-operation'
      : mainTableSurface
        ? 'main-table'
        : 'unknown';
  const proposalPreviewElement = proposalPreviewSurface === 'row-operation'
    ? rowOperationProposalPreview
    : proposalPreviewSurface === 'main-table'
      ? sourcePreview
      : null;
  const proposalPreviewProposalId = proposalPreviewSurface === 'main-table'
    ? mainPreviewProposalId
    : proposalPreviewSurface === 'row-operation'
      ? proposalId
      : null;
  const proposalPreviewTable = proposalPreviewElement?.querySelector('table');
  const proposalPreviewRoleTable = proposalPreviewElement?.querySelector('[data-testid="preview-table-content"] [role="table"]');
  const proposalRenderedRows = proposalPreviewSurface === 'main-table'
    ? sourceRenderedRows
    : proposalPreviewElement?.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length ?? 0;
  const sourceTablePreview = sourcePreview ? {
    status: attribute(sourcePreview, 'data-preview-status'),
    receiptId: attribute(sourcePreview, 'data-preview-receipt-id'),
    outputId: attribute(sourcePreview, 'data-preview-output-id'),
    proposalId: attribute(sourcePreview, 'data-preview-proposal-id'),
    draftVersion: numberAttribute(sourcePreview, 'data-current-draft-version'),
    draftDigest: attribute(sourcePreview, 'data-current-draft-digest'),
    rowsPresent: Boolean(sourceTable || sourceNativeTable),
    rowCount: rowCount(sourceTable),
    renderedRowCount: sourceRenderedRows,
  } : null;
  const embeddedProposalPreview = proposalPreviewElement ? {
    status: attribute(proposalPreviewElement, 'data-preview-status'),
    receiptId: attribute(proposalPreviewElement, 'data-preview-receipt-id'),
    outputId: attribute(proposalPreviewElement, 'data-preview-output-id'),
    proposalId: proposalPreviewProposalId,
    rowsPresent: Boolean(proposalPreviewTable || proposalPreviewRoleTable),
    rowCount: proposalPreviewSurface === 'main-table'
      ? rowCount(proposalPreviewRoleTable)
      : proposalPreviewTable
        ? proposalRenderedRows
        : rowCount(proposalPreviewRoleTable),
    renderedRowCount: proposalRenderedRows,
  } : null;
  const proposalOutputId = attribute(proposalPanel, 'data-proposal-output-id');
  const proposalSnapshotToken = attribute(proposalPanel, 'data-proposal-snapshot-token');
  const proposalDraftVersion = numberAttribute(proposalPanel, 'data-proposal-draft-version');
  const proposalDraftDigest = attribute(proposalPanel, 'data-proposal-draft-digest');
  const proposalPreviewStatus = attribute(proposalPanel, 'data-proposal-preview-status');
  const tablePreviewStatus = sourceTablePreview?.status;
  const renderedSourcePreview = Boolean(sourceTable || sourcePreviewError || sourcePreview?.querySelector('table'));
  const proposalStatus = attribute(proposalPanel, 'data-proposal-status');

  return {
    status: proposalPanel ? (proposalStatus ?? 'unknown') : 'absent',
    proposalPanelPresent: Boolean(proposalPanel),
    canApply: Boolean(applyButton && !applyButton.disabled),
    proposalIdPresent: Boolean(proposalId),
    proposalId,
    proposalOutputId,
    proposalSnapshotToken,
    proposalDraftVersion,
    proposalDraftDigest,
    proposalPreviewStatus,
    proposalPreviewSurface,
    previewReceiptPresent: Boolean(embeddedProposalPreview?.receiptId),
    rowsPresent: Boolean(embeddedProposalPreview?.rowsPresent),
    receiptPresent: Boolean(embeddedProposalPreview?.receiptId),
    previewReceiptId: embeddedProposalPreview?.receiptId ?? null,
    previewOutputId: embeddedProposalPreview?.outputId ?? null,
    ...(renderedSourcePreview && tablePreviewStatus ? { tablePreviewStatus } : {}),
    sourceTablePreview,
    embeddedProposalPreview,
    apply: {
      available: Boolean(applyButton),
      enabled: Boolean(applyButton && !applyButton.disabled),
      applying: Boolean(proposalPanel?.querySelector('[data-testid="construction-proposal-applying"]')),
    },
  };
});

export const waitForPublishedSourcePreview = async (page, preview, timeout = 45_000) => {
  const receiptId = preview?.receiptId;
  const outputId = preview?.outputId;
  if (typeof receiptId !== 'string' || receiptId.length === 0 ||
      typeof outputId !== 'string' || outputId.length === 0) {
    throw new Error('choice preview must provide non-empty receipt and output identities before waiting for publication');
  }
  await page.waitForFunction(({ expectedReceiptId, expectedOutputId }) => {
    const source = document.querySelector('[data-testid="construction-preview"]');
    const table = source?.querySelector('[data-testid="preview-table-content"] [role="table"], table');
    return source?.dataset.previewStatus === 'ready'
      && source.dataset.previewReceiptId === expectedReceiptId
      && source.dataset.previewOutputId === expectedOutputId
      && Boolean(table);
  }, { expectedReceiptId: receiptId, expectedOutputId: outputId }, { timeout });
};

export const combineAuthoringObservation = (exchange, observation = exchange.responseObservation) => {
  const responseProposalId = exchange.response?.proposalId;
  if (observation.status === 'ready'
    && responseProposalId
    && observation.proposalId
    && responseProposalId !== observation.proposalId) {
    throw new Error(`proposal response ${responseProposalId} does not match rendered proposal ${observation.proposalId}`);
  }
  return observation;
};

export const addFieldWithCommandTrace = async (page, fieldName, actions, addField) => {
  const proposalAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'construction-choice-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
  });
  const commandAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt: 'P1' },
  });
  const proposalTrace = recordAuthoringAttempt(page, actions, proposalAttempt, {
    requestLabel: 'Preview',
    requestEvent: 'preview',
    responseLabel: 'PreviewComplete',
    responseErrorLabel: 'PreviewError',
    responseEvent: 'preview-response',
  });
  const result = await addField(page, fieldName);
  const { exchange: proposalExchange } = await proposalTrace.settled;
  const commandStarted = await commandAttempt.started;
  const commandExchange = await commandAttempt.settled;
  await waitForPublishedSourcePreview(page, proposalExchange.response?.preview);
  actions.push({
    label: 'AddColumn',
    event: 'add-column',
    owner: commandStarted.owner,
    request: commandExchange.request,
    response: commandExchange.response,
    settlement: commandExchange.settlement,
    observation: await captureBuilderObservation(page),
  });
  return result;
};

export const test = base.extend({
  fastState: [async ({}, use) => {
    await use({
      project: required('LOOM_FAST_PROJECT'),
      explorerId: required('LOOM_FAST_EXPLORER'),
      uiUrl: required('LOOM_FAST_UI_URL'),
      apiUrl: required('LOOM_FAST_API_URL'),
    });
  }, { scope: 'worker' }],

  fastContext: [async ({ browser }, use) => {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      ...(process.env.LOOM_FAST_STORAGE_STATE ? { storageState: process.env.LOOM_FAST_STORAGE_STATE } : {}),
    });
    await use(context);
    await context.close();
  }, { scope: 'worker' }],

  fastPage: [async ({ fastContext }, use) => {
    const page = await fastContext.newPage();
    page.setDefaultTimeout(5_000);
    page.setDefaultNavigationTimeout(5_000);
    await use(page);
    await page.close();
  }, { scope: 'test' }],

  fastUrl: [async ({ fastState }, use) => {
    const url = new URL(fastState.uiUrl);
    url.searchParams.set('project', fastState.project);
    url.searchParams.set('explorer', fastState.explorerId);
    url.searchParams.set('mode', 'builder');
    await use(url.toString());
  }, { scope: 'test' }],
});
