import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { assertExactMultiset } from './group-related-multiset.mjs';
import { classifyNativeBrowserApiRequest, isSameUiProxyResponse } from './lib/native-browser-api-scope.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { sanitizePayload } from './lib/playwright-browser.mjs';
import { browserEval, click, launchCdaBrowser, navigate, selectOption, waitForBrowser, waitForControl } from './lib/playwright-cda-actions.mjs';
import { assertReopenedProposalAfterCancel } from './lib/proposal-reopen-binding.mjs';

const sourceRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const load = (path) => import(pathToFileURL(join(sourceRoot, path)).href);
const [{ captureSourceFreeze },
  { captureApiBuildFreeze, checkContainerApiBuildStamp },
  { selectSavedPreviewRequest },
  { relatedSourceProposalCandidate }] = await Promise.all([
  load('scripts/lib/source-freeze.mjs'),
  load('scripts/lib/api-build-freeze.mjs'),
  load('scripts/lib/saved-preview-binding.mjs'),
  load('scripts/lib/related-source-capture.mjs'),
]);

const project = process.env.LOOM_CDA_PROJECT;
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `group-edit-before-related-column-${Date.now()}-${randomUUID().slice(0, 8)}`;
const evidence = process.argv[2] ?? `/tmp/loom-group-edit-before-related-column-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot, arangoContainer });
assert.notEqual(explorer, protectedExplorer);

const explorerRoot = `/api/v1/projects/${project}/explorers/${explorer}`;
const base = `${explorerRoot}/authoring/v2`;
const root = `/api/v1/projects/${project}/explorers`;
const browserTransportScope = { uiOrigin, apiOrigin, project, explorer, protectedExplorer };
const apiBuildContainer = apiContainer;
const report = {
  status: 'running', explorer, project, generation, protectedExplorer,
  scope: { apiOrigin, uiOrigin, browserApiTransport: 'same-origin UI /api proxy',
    rawOracle: 'project + dataset generation + explicit selected Specimen ID' },
  cases: [], errors: [], browserTransportViolations: [], requests: [], nativeRequests: [], started: new Date().toISOString(),
};
await mkdir(evidence, { recursive: true });
let browser, builder, outputId, selection, source, selectedPopulationRoute, sourceFreeze, apiBuildFreeze;
let activeBrowserOwner = 'workspace setup';
let nativeDrainAttempted = false;
const nativeById = new Map();
const pendingNetworkReads = new Set();
const oracleQueries = [];
const expectedCancelReceipts = new Map();
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
      entry.expectedCancelOwner = evidence;
      if (!evidence.requestIds.includes(entry.requestId)) evidence.requestIds.push(entry.requestId);
    }
  }
  return evidence;
};
const pause = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

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
  const result = spawnSync('rtk', ['proxy', 'docker', 'exec',
    arangoContainer,
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],
  { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, `${result.stdout}${result.stderr}`);
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
  const invalid = report.nativeRequests.filter((entry) => entry.bodyReadStatus !== 'decoded');
  const unclassifiedAborts = report.nativeRequests.filter((entry) => entry.cancelled && !entry.cancelContext);
  report.nativeRequestDrain = {
    status: 'checking',
    terminalRequests: report.nativeRequests.length,
    decodedResponses: report.nativeRequests.filter((entry) => entry.bodyReadStatus === 'decoded').length,
    expectedProposalCancelOwners: [...expectedCancelReceipts.values()],
    terminalFailures: invalid.map(({ requestId, path, status, owner, bodyReadStatus, bodyError, loadingFailure, cancelContext }) => ({
      requestId, path, status, owner, bodyReadStatus, bodyError, loadingFailure,
      cancelContext: cancelContext && { proposalId: cancelContext.proposalId, owner: cancelContext.owner, transition: cancelContext.transition },
    })),
    unclassifiedAborts: unclassifiedAborts.map(({ requestId, path, method, owner, startedAt, loadingFailure, initiator }) => ({ requestId, path, method, owner, startedAt, loadingFailure, initiator })),
  };
  assert.deepEqual(pending, [], `Native API drain left requests without a terminal event/body result: ${JSON.stringify(pending.map(({ requestId, path, method, owner, status, bodyReadStatus, startedAt }) => ({ requestId, path, method, owner, status, bodyReadStatus, startedAt })))}`);
  assert.deepEqual(unclassifiedAborts, [], `Native API aborts have no explicit proposal-cancel context: ${JSON.stringify(report.nativeRequestDrain.unclassifiedAborts)}`);
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
  await waitForBrowser(browser.page, ({ outputId: expectedOutput, version, digest }) => {
    const p = document.querySelector('[data-testid="construction-preview"]');
    return p?.dataset.previewStatus === 'ready' && p?.dataset.previewOutputId === expectedOutput &&
      p?.dataset.currentDraftVersion === version && p?.dataset.currentDraftDigest === digest && Boolean(p?.dataset.previewReceiptId);
  }, Math.max(1, deadline - Date.now()), { outputId, version: String(expectedBuilder.draftVersion), digest: expectedBuilder.draftDigest });
  const active = await browserEval(browser.page, () => {
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
  await navigate(browser.page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForControl(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await click(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await waitForControl(browser.page, '[data-testid="construction-rows-settings-trigger"]', { enabled: true });
  await waitForBrowser(browser.page, ({ columnCount: count }) => {
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
  await waitForBrowser(browser.page, ({ rows: rowCount, columns: columnTotal }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === rowCount && table?.getAttribute('aria-colcount') === columnTotal &&
      !document.body.innerText.includes('Loading your table…');
  }, 5000, { rows: String(Math.min(25, expectedRows.length) + 1), columns: String(columnCount) });
  const rows = await browserEval(browser.page, () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
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
const installBrowserCapture = () => {
  browser.page.on('pageerror', error => report.errors.push({ kind: 'runtime', text: error.message }));
  browser.page.on('console', message => {
    if (message.type() !== 'error') return;
    const location = message.location().url;
    if (location === `${uiOrigin}/favicon.ico` && /404 \(Not Found\)/.test(message.text())) {
      (report.assetFailures ??= []).push({ url: location, status: 404, kind: 'console' });
      return;
    }
    report.errors.push({ kind: 'console', text: message.text() });
  });
  browser.page.on('request', request => {
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
    const requestId = headers['x-request-id'] ?? `playwright-${nextBrowserRequestId++}`;
    const entry = {
      requestId, path: url.pathname, origin: url.origin, method: request.method(),
      url: request.url(), owner: activeBrowserOwner,
      transportScope: transport.scope, body, requestBodyParseError,
      authorizationHeaderPresent: Object.keys(headers).some(key => key.toLowerCase() === 'authorization'),
      startedAt: Date.now(), status: null, completedAt: null, networkTerminal: false,
      bodyReadStatus: 'pending', terminalState: 'pending', cancelled: false,
    };
    report.nativeRequests.push(entry);
    nativeById.set(requestId, entry);
    nativeByRequest.set(request, entry);
  });
  browser.page.on('response', response => {
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
  browser.page.on('requestfinished', request => {
    const entry = nativeByRequest.get(request);
    if (!entry) return;
    entry.networkTerminal = true;
    entry.terminalState = 'finished';
    entry.completedAt ??= Date.now();
  });
  browser.page.on('requestfailed', request => {
    const entry = nativeByRequest.get(request);
    if (!entry) return;
    entry.networkTerminal = true;
    entry.terminalState = 'failed';
    const failure = request.failure();
    entry.cancelled = /aborted|cancelled/i.test(failure?.errorText ?? '');
    entry.loadingFailure = { errorText: failure?.errorText ?? 'unknown', canceled: entry.cancelled };
    const proposalId = entry.proposalId ?? entry.body?.receiptId;
    const expectedOwner = proposalId ? expectedCancelReceipts.get(proposalId) : undefined;
    if (expectedOwner) {
      entry.cancelContext = expectedOwner;
      if (!expectedOwner.requestIds.includes(entry.requestId)) expectedOwner.requestIds.push(entry.requestId);
    }
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
  await waitForBrowser(browser.page, ({ selector }) => ['ready', 'error', 'needs-repair'].includes(document.querySelector(selector)?.dataset.proposalStatus),
    Math.max(1, startedAt + 5000 - Date.now()), { selector: panelSelector });
  const panel = await browserEval(browser.page, ({ selector }) => {
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
  await click(browser.page, '[data-testid="construction-apply-proposal"]');
  await waitForControl(browser.page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
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
  await click(browser.page, `[data-testid="construction-history-step-${stepId}"]`);
  await click(browser.page, `[data-testid="construction-edit-step-${stepId}"]`);
  await waitForControl(browser.page, 'select[aria-label="Summary 1"]', { enabled: true });
  const startedAt = Date.now();
  await selectOption(browser.page, 'select[aria-label="Summary 1"]', operation);
  let selectedField;
  if (operation === 'COUNT_DISTINCT') {
    await waitForControl(browser.page, 'select[aria-label="Summary field 1"]', { enabled: true });
    const options = await browserEval(browser.page, () => [...document.querySelector('select[aria-label="Summary field 1"]').options]
      .map(option => ({ value: option.value, label: option.textContent })));
    selectedField = options.find((field) => field.label.startsWith('Observation FHIR resource ID'));
    assert(selectedField, `Active related Observation identity must be available to Group before a downstream projection: ${JSON.stringify(options)}`);
    await selectOption(browser.page, 'select[aria-label="Summary field 1"]', selectedField.value);
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
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze = { root: sourceRoot, watchedFileCount: sourceFreeze.watchedFileCount, startedAt: new Date().toISOString() };
  apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiBuildContainer));
  report.apiBuildFreeze = { target: `running local CDA API build stamp (${apiBuildContainer})`, initial: apiBuildFreeze.initial, invalidatesRun: true, productFailure: false };

  const sourceQuery = `
FOR s IN (
  FOR candidate IN Specimen
    FILTER candidate.project == ${JSON.stringify(project)} AND candidate.dataset_generation == ${JSON.stringify(generation)}
    SORT candidate.id
    LIMIT 2000
    RETURN {id:candidate.id,_id:candidate._id,generation:candidate.dataset_generation,resourceType:candidate.resourceType}
)
  LET patients = (
    FOR e IN fhir_edge
      FILTER e._from == s._id AND e._to != null AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
        AND STARTS_WITH(e._to, "Patient/")
      LET p = DOCUMENT(e._to)
      FILTER p.project == ${JSON.stringify(project)} AND p.dataset_generation == ${JSON.stringify(generation)}
      RETURN DISTINCT {id:p.id,_id:p._id}
  )
  FILTER LENGTH(patients) == 1
  LET patient = patients[0]
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == patient._id AND STARTS_WITH(e._from, "Observation/")
        AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)}
        AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      SORT o.id
      RETURN {id:o.id,_id:o._id,status:o.payload.status,hasScalarStatus:IS_STRING(o.payload.status)}
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
  assert.equal(new Set(observationIDs).size, observations.length, 'Raw witness Observation IDs must be unique');
  assert(observationIDs.length >= 2, 'Raw witness must contain at least two distinct Observation IDs');
  assert(observations.length <= 24, 'Raw witness must fit completely in the 25-row preview bound');
  report.oracle = { kind: 'bounded real CDA source fixture and exact project/generation-scoped raw fhir_edge witness',
    sourceQuery, searchBounds: { sortedSpecimens: 2000, observationsPerPatientMin: 2, observationsPerPatientMax: 24 },
    source, patient, observations, statusValuesPresent: [...new Set(observationStatuses)].sort(),
    statusMultiplicityPreserved: true, chain: [] };

  await api(root, { name: explorer, title: 'Group edit before related column QA' });
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

  browser = await launchCdaBrowser(evidence, apiOrigin, uiOrigin);
  installBrowserCapture();
  const sourceRows = [[source.id]];
  await open(sourceRows, 1, 'selected-raw-Specimen-reload');
  let witnesses = [{ anchor: source._id, values: [source.id] }];
  const chain = [
    { from: 'Specimen', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND' },
    { from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND' },
  ];
  let expectedExpandedRows = sourceRows;
  for (const hop of chain) {
    activeBrowserOwner = `expand-${hop.from}-${hop.to}`;
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
    await click(browser.page, '[data-testid="construction-rows-settings-trigger"]');
    await waitForControl(browser.page, '[data-testid="construction-action-related-rows"]', { enabled: true });
    await click(browser.page, '[data-testid="construction-action-related-rows"]');
    const editor = '[data-testid="construction-related-expand-editor"]';
    await waitForControl(browser.page, editor + ' select[aria-label="Related record type"]', { enabled: true });
    const startedAt = Date.now();
    await selectOption(browser.page, editor + ' select[aria-label="Related record type"]', hop.to);
    const routeLabel = hop.from + (hop.direction === 'INBOUND' ? ` <-[${hop.field}]- ` : ` -[${hop.field}]-> `) + hop.to;
    await waitForControl(browser.page, `${editor} input[aria-label="${routeLabel}"]`, { timeout: 5000 });
    await click(browser.page, editor + ` input[aria-label="${routeLabel}"]`);
    const expandedProposal = await browserProposal({ requestStart: startIndex, path: base + '/construction-proposals',
      expectedRows: expectedExpandedRows, state: builder, label: `expand-${hop.from}-${hop.to}`, startedAt });
    const actionStart = Date.now();
    const proposalResponse = expandedProposal.response;
    await click(browser.page, '[data-testid="construction-apply-proposal"]');
    await waitForControl(browser.page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
    await mountedRows(expectedExpandedRows, expectedExpandedRows[0]?.length ?? 2);
    await waitNetwork();
    const applied = findNative(startIndex, base + '/commands', (entry) => entry.body?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === proposalResponse.proposalId));
    assertDraftRequest(applied, builder, `apply expand-${hop.from}-${hop.to}`);
    builder = await api(base + '/builder');
    assert.equal(builder.draftDigest, proposalResponse.candidateWorkspaceDigest);
    recordAction(`apply-expand-${hop.from}-${hop.to}`, actionStart, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
    assertSourceBinding(builder, selectedPopulationRoute);
  }
  assert(witnesses.length > 1, 'The chosen CDA Specimen must reach multiple Observations');
  const contributorIDs = [...new Set(witnesses.map((witness) => witness.values.at(-1)))].sort();
  assert(contributorIDs.length > 1);
  const pipelineObservationIDs = new Set(witnesses.map((witness) => witness.values.at(-1)));
  assert(!pipelineObservationIDs.has('—'), 'The selected Specimen must have concrete related Observations');
  assert.deepEqual([...pipelineObservationIDs].sort(), [...observationIDs].sort(),
    'The bounded source finder and exact pipeline edge oracle must identify the same Observation set');
  const groupedRows = [[patient.id, witnesses.length]];
  const distinctRows = [[patient.id, contributorIDs.length]];

  const groupStartIndex = report.nativeRequests.length;
  activeBrowserOwner = 'group-by-Patient-FHIR-resource-ID-count-rows';
  await click(browser.page, '[data-testid="construction-rows-settings-trigger"]');
  await click(browser.page, '[data-testid="construction-action-group-rows"]');
  await waitForControl(browser.page, 'input[aria-label="Group by Patient FHIR resource ID"]', { enabled: true });
  const groupStartedAt = Date.now();
  await click(browser.page, 'input[aria-label="Group by Patient FHIR resource ID"]');
  const initialGroupProposal = await browserProposal({ requestStart: groupStartIndex, path: base + '/construction-proposals',
    expectedRows: groupedRows, state: builder, label: 'group-by-Specimen-ID-count-rows', startedAt: groupStartedAt });
  builder = await applyGroupProposal(initialGroupProposal.response, builder, groupedRows, 'apply-initial-Group');
  const groupBaseline = structuredClone(builder);
  const groupDocumentBaseline = structuredClone(doc(builder));
  assertSourceBinding(builder, selectedPopulationRoute);
  const groupedReload = await open(groupedRows, 2, 'reload-initial-Group');
  assert.deepEqual(doc(groupedReload).construction, groupDocumentBaseline.construction);

  // Distinct uncovered transition: commit the upstream Group edit while the
  // downstream Observation.status column does not exist yet.
  const beforeFirstEdit = structuredClone(builder);
  const countDistinctProposal = await editGroupAggregate('COUNT_DISTINCT', distinctRows, builder,
    'edit-Group-to-count-distinct-before-column');
  assert.deepEqual(countDistinctProposal.panel.rows[0].slice(0, 2).map((cell) => cell.text), distinctRows[0].map(String));
  const cancelStartedAt = Date.now();
  activeBrowserOwner = 'cancel-Group-edit-before-column';
  registerProposalCancelOwner(countDistinctProposal.response.proposalId, 'cancel the first upstream Group aggregate candidate');
  await click(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(browser.page, '[data-testid="construction-proposal-panel"]', { hidden: true });
  const afterEditCancel = await api(base + '/builder');
  assert.equal(afterEditCancel.draftVersion, beforeFirstEdit.draftVersion);
  assert.equal(afterEditCancel.draftDigest, beforeFirstEdit.draftDigest);
  assert.deepEqual(afterEditCancel.workspace, beforeFirstEdit.workspace, 'Group edit Cancel must preserve the exact source and draft bindings');
  recordAction('cancel-Group-edit-before-column', cancelStartedAt);

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
    const alreadyOpen = await browserEval(browser.page, () => Boolean(document.querySelector('[aria-label="Add columns editor"]')));
    if (!alreadyOpen) {
      await click(browser.page, '[data-testid="construction-action-add-columns"]');
      await click(browser.page, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
    }
    await waitForControl(browser.page, '[data-testid="construction-add-columns-source"]');
    const relatedOpen = await browserEval(browser.page, () => document.querySelector('[aria-label="Related resources"]')?.open === true);
    if (!relatedOpen) await click(browser.page, '[aria-label="Related resources"] summary');
    await click(browser.page, '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
    const rawFieldsOpen = await browserEval(browser.page, () => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true);
    if (!rawFieldsOpen) await click(browser.page, '[data-testid="feature-catalog-raw-fields"] summary');
    const fieldSelector = 'input[aria-label="Select Observation.status"]';
    await waitForControl(browser.page, `${fieldSelector}:not(:disabled)`);
    const fieldChecked = await browserEval(browser.page, ({ selector }) => document.querySelector(selector)?.checked === true, { selector: fieldSelector });
    if (!fieldChecked) await click(browser.page, fieldSelector);
    await click(browser.page, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
    await waitForControl(browser.page, '[role="dialog"]');
    const otherPaths = await browserEval(browser.page, () => [...document.querySelectorAll('[role="dialog"] summary')]
      .some(item => item.innerText.includes('Other relationship paths')));
    if (otherPaths) await click(browser.page, '[role="dialog"] summary', { includes: 'Other relationship paths' });
    const routeSelector = `[role="dialog"] input[aria-label=${JSON.stringify(relatedRouteLabel)}]`;
    await waitForControl(browser.page, routeSelector);
    await click(browser.page, routeSelector);
    const formSelector = '[role="dialog"] input[aria-label="Status: Keep all matching values"]';
    await waitForControl(browser.page, formSelector);
    await click(browser.page, formSelector);
    await selectOption(browser.page, '[role="dialog"] select[aria-label="Values per grouped row"]', 'ALL');
    const selected = await browserEval(browser.page, ({ routeSelector: route, formSelector: form }) => {
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
    await click(browser.page, '[role="dialog"] button', { name: 'Add 1 column' });
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
    const cells = await browserEval(browser.page, () => [...document.querySelector('[data-testid="construction-proposal-preview-row"]').querySelectorAll('td')]
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

  const fieldCancelStartedAt = Date.now();
  activeBrowserOwner = 'cancel-related-column-after-Group-edit';
  registerProposalCancelOwner(fieldProposal.response.proposalId, 'cancel the first downstream RELATED_SOURCE candidate');
  await click(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(browser.page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  await mountedRows(distinctRows, 2);
  const afterFieldCancel = await api(base + '/builder');
  assert.equal(afterFieldCancel.draftVersion, beforeFieldAdd.draftVersion);
  assert.equal(afterFieldCancel.draftDigest, beforeFieldAdd.draftDigest);
  assert.deepEqual(afterFieldCancel.workspace, beforeFieldAdd.workspace, 'Related-column Cancel must preserve exact Group edit and source bindings');
  recordAction('cancel-related-column-after-Group-edit', fieldCancelStartedAt);

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
  await click(browser.page, '[data-testid="construction-apply-proposal"]');
  await waitForControl(browser.page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
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

  const reloadedField = await open(withFieldTyped, 3, 'reload-related-column-after-Group-edit');
  assert.deepEqual(doc(reloadedField), withFieldDocument, 'Reload must preserve the exact field occurrence, ALL policy, and Group edit');

  // Round-trip the upstream aggregate with the related source present, then remove
  // its owning native step through an automatic proposal and prove restoration.
  const beforeRestoreEdit = structuredClone(builder);
  const countRowsProposal = await editGroupAggregate('COUNT_ROWS', withFieldTyped, builder,
    'edit-Group-back-to-count-rows-with-related-column');
  const restoreEditCancelStartedAt = Date.now();
  activeBrowserOwner = 'cancel-Group-round-trip-edit';
  registerProposalCancelOwner(countRowsProposal.response.proposalId, 'cancel the Group round-trip candidate while RELATED_SOURCE is saved');
  await click(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(browser.page, '[data-testid="construction-proposal-panel"]', { hidden: true });
  const afterRestoreCancel = await api(base + '/builder');
  assert.deepEqual(afterRestoreCancel.workspace, beforeRestoreEdit.workspace);
  assert.equal(afterRestoreCancel.draftDigest, beforeRestoreEdit.draftDigest);
  recordAction('cancel-Group-round-trip-edit', restoreEditCancelStartedAt);

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

  const proposeRelatedStepRemoval = async (state, label) => {
    activeBrowserOwner = label;
    const step = doc(state).construction.steps.find((candidate) => candidate.id === relatedStep.id);
    assert(step?.operation.kind === 'RELATED_SOURCE');
    await click(browser.page, `[data-testid="construction-history-step-${step.id}"]`);
    const removeSelector = `[data-testid="construction-remove-step-${step.id}"]:not(:disabled)`;
    await waitForControl(browser.page, removeSelector, { timeout: 5000 });
    const requestStart = report.nativeRequests.length;
    const startedAt = Date.now();
    await click(browser.page, `[data-testid="construction-remove-step-${step.id}"]`);
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
  await click(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(browser.page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  const afterRemoveCancel = await api(base + '/builder');
  assert.equal(afterRemoveCancel.draftVersion, beforeRemoveCancel.draftVersion);
  assert.equal(afterRemoveCancel.draftDigest, beforeRemoveCancel.draftDigest);
  assert.deepEqual(afterRemoveCancel.workspace, beforeRemoveCancel.workspace,
    'Canceling related-source removal must preserve its exact step, Group, and source bindings');
  recordAction('cancel-remove-RELATED_SOURCE', removeCancelStartedAt);
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

  await drainNativeRequests();
  assert.deepEqual(report.browserTransportViolations, [], 'Browser API requests and responses must remain on the scoped local UI proxy');
  assert(report.nativeRequests.every((entry) => entry.origin === uiOrigin &&
    entry.responseBinding?.matchesCapturedUiProxyRequest === true && !entry.authorizationHeaderPresent),
  'Every captured native API response must remain bound to the local no-auth UI proxy');
  assert(report.apiBuildFreeze.initial.fresh, 'The local CDA API server build binding must be fresh before browser verification');
  assert(!report.nativeRequests.some((entry) => entry.path.includes(protectedExplorer)), 'Protected shared Explorer must remain untouched');
  assert.deepEqual(report.errors, [], 'The native lifecycle must have no HTTP, runtime, or console errors');
  report.rawOracleQueries = oracleQueries;
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  await browser?.captureFailure(error, { phase: activeBrowserOwner, explorer, nativeRequests: report.nativeRequests.slice(-20) });
  process.exitCode = 1;
} finally {
  if (browser && !nativeDrainAttempted) {
    try { await drainNativeRequests(); }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'failed';
      report.error = [report.error, `Strict native request drain failed: ${String(error.stack ?? error)}`].filter(Boolean).join('\n');
      process.exitCode = 1;
    }
  }
  if (apiBuildFreeze) {
    try { report.apiBuildFreeze = { ...report.apiBuildFreeze, ...await apiBuildFreeze.assertUnchanged() }; }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreeze = { ...report.apiBuildFreeze, unchanged: false, invalidatesRun: true, productFailure: false, error: String(error), reason: error.reason, before: error.before, after: error.after };
      process.exitCode = 1;
    }
  }
  if (sourceFreeze) {
    try { report.sourceFreeze = { ...report.sourceFreeze, ...await sourceFreeze.assertUnchanged(), finishedAt: new Date().toISOString() }; }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false, error: String(error), finishedAt: new Date().toISOString() };
      process.exitCode = 1;
    }
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(sanitizePayload(report), null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, explorer, cases: report.cases.map(({ name, durationMs }) => ({ name, durationMs })), error: report.error }, null, 2));
