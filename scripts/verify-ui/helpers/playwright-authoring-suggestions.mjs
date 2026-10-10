import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { assertPreviewPatientIds, patientWindow } from './playwright-authoring.mjs';
import { configureNativePage } from './playwright-authoring-page.mjs';
import { browserURL } from '../workflows/builder-url.mjs';
import { recordCheck } from './report.mjs';

const unique = async locator => {
  const count = await locator.count();
  assert.equal(count, 1, `Expected one target for ${locator.toString()}, found ${count}`);
  return locator;
};

const hasVerifiedCapabilityReplacement = item => {
  const binding = item.binding;
  const replacement = item.replacement;
  return item.kind === 'network' && item.errorText === 'net::ERR_ABORTED' &&
    item.canceled === true && item.cancellationReason === 'superseded capability binding has a later successful replacement' &&
    item.method === 'POST' && Number.isInteger(item.sequence) &&
    binding?.route === item.url && new URL(binding.route).pathname.endsWith('/authoring/v2/construction-capabilities') &&
    replacement && Number.isInteger(replacement.sequence) && replacement.sequence > item.sequence &&
    Number.isInteger(replacement.status) && replacement.status >= 200 && replacement.status < 300 &&
    replacement.finished === true && replacement.responseMatches === true && replacement.failed !== true &&
    replacement.binding?.route === binding.route &&
    JSON.stringify(replacement.binding) !== JSON.stringify(binding);
};

const suggestionRequestURL = (target, explorer) => `${new URL(target.uiUrl).origin}` +
  `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/suggestions`;

const exactInjectedSuggestionFailure = (report, target, failure) => {
  const expectedURL = suggestionRequestURL(target, report.target.explorer);
  return report.network.includes(failure) && failure.kind === 'network' && failure.method === 'POST' &&
    failure.url === expectedURL && failure.rawURL === expectedURL && failure.errorText === 'net::ERR_FAILED' &&
    failure.injectedFault === true && failure.injectedAction === 'abort' &&
    failure.injectedRequestId === failure.requestDetails?.requestId &&
    typeof failure.injectedRequestId === 'string' && failure.injectedRequestId.length > 0 &&
    typeof failure.playwrightRequestId === 'string' && failure.playwrightRequestId.length > 0;
};

export const classifySuggestionDiagnostics = (report, target, injectedFailure) => {
  const cancelledReads = report.network.filter(item => {
    try { return hasVerifiedCapabilityReplacement(item); } catch { return false; }
  });
  const validFailure = exactInjectedSuggestionFailure(report, target, injectedFailure) ? injectedFailure : undefined;
  const competingSameRequestURLFailures = validFailure
    ? report.network.filter(item => item !== validFailure && item.kind === 'network' &&
      item.method === validFailure.method && item.errorText === 'net::ERR_FAILED' &&
      (item.rawURL ?? item.url) === validFailure.rawURL)
    : [];
  const matchingAbortConsoleErrors = validFailure && competingSameRequestURLFailures.length === 0
    ? report.network.filter(item => item.kind === 'console-error' &&
      item.text === 'Failed to load resource: net::ERR_FAILED' && item.rawLocation === validFailure.rawURL)
    : [];
  const expectedAbortConsoleError = matchingAbortConsoleErrors.length === 1 ? matchingAbortConsoleErrors[0] : undefined;
  if (expectedAbortConsoleError) {
    Object.assign(expectedAbortConsoleError, {
      kind: 'network',
      observedAs: 'console-error',
      method: validFailure.method,
      url: validFailure.url,
      errorText: 'net::ERR_FAILED',
      injectedFault: true,
      injectedAction: 'abort',
      injectedRequestId: validFailure.injectedRequestId,
      playwrightRequestId: validFailure.playwrightRequestId,
      requestDetails: validFailure.requestDetails,
    });
  }
  const expected = new Set([
    ...cancelledReads,
    ...(validFailure ? [validFailure] : []),
    ...(expectedAbortConsoleError ? [expectedAbortConsoleError] : []),
  ]);
  return {
    cancelledReads,
    expectedInjectedSuggestionFailures: [
      ...(validFailure ? [validFailure] : []),
      ...(expectedAbortConsoleError ? [expectedAbortConsoleError] : []),
    ],
    expectedAbortConsoleError,
    competingSameRequestURLFailures,
    unexpected: report.network.filter(item => !expected.has(item)),
  };
};

const checkUnexpectedDiagnostics = (report, target, injectedFailure) => {
  const { cancelledReads, expectedInjectedSuggestionFailures, expectedAbortConsoleError, unexpected } =
    classifySuggestionDiagnostics(report, target, injectedFailure);
  report.cancelledOwnedReads = cancelledReads;
  report.expectedInjectedSuggestionFailures = expectedInjectedSuggestionFailures;
  recordCheck(report, 'correctness', 'no unexpected network, API, or browser errors', unexpected.length === 0,
    { console: unexpected.filter(item => item.kind === 'console-error'),
      pageErrors: unexpected.filter(item => item.kind === 'exception'),
      matchedAbortConsoleError: expectedAbortConsoleError ? {
        text: expectedAbortConsoleError.text,
        url: expectedAbortConsoleError.rawLocation ?? expectedAbortConsoleError.location,
      } : null,
      networkFailures: unexpected.filter(item => item.kind === 'network') });
};

const explorerFromAuthoringPath = pathname => {
  const match = /\/explorers\/([^/]+)\/authoring\/v2\/(?:builder|suggestions)$/.exec(pathname);
  return match ? decodeURIComponent(match[1]) : undefined;
};

export const isOwnedRootSuggestionRequest = ({
  method, url, expectedURL, requestId, body, rootNodeId, snapshotToken,
}) => method === 'POST' && url === expectedURL &&
  typeof requestId === 'string' && /^suggestions-.+/.test(requestId) &&
  !requestId.startsWith('first-table-suggestions-') &&
  typeof rootNodeId === 'string' && body?.nodeId === rootNodeId &&
  typeof snapshotToken === 'string' && body?.snapshotToken === snapshotToken;

export const sanitizedSuggestionRequestBinding = ({ kind, requestId, body, observedAtMs }) => {
  const snapshotToken = typeof body?.snapshotToken === 'string' ? body.snapshotToken : '';
  return {
    kind,
    endpoint: '/authoring/v2/suggestions',
    method: 'POST',
    attemptId: typeof requestId === 'string' ? `${kind}:${requestId.slice(0, 180)}` : null,
    requestId: typeof requestId === 'string' ? requestId.slice(0, 200) : null,
    nodeId: typeof body?.nodeId === 'string' ? body.nodeId.slice(0, 200) : null,
    snapshotTokenPresent: snapshotToken.length > 0,
    snapshotTokenSHA256: snapshotToken ? createHash('sha256').update(snapshotToken).digest('hex') : null,
    observedAtMs,
  };
};

export const captureSuggestionUiState = async page => {
  const capturedAtMs = Date.now();
  try {
    const state = await page.evaluate(() => {
      const visible = element => {
        if (!element || element.getClientRects().length === 0) return false;
        for (let ancestor = element; ancestor; ancestor = ancestor.parentElement) {
          if (ancestor.hasAttribute('hidden') || ancestor.getAttribute('aria-hidden') === 'true') return false;
          const style = ancestor.ownerDocument.defaultView?.getComputedStyle(ancestor);
          if (style?.display === 'none' || style?.visibility === 'hidden' || style?.visibility === 'collapse') return false;
          if (ancestor.tagName === 'DETAILS' && !ancestor.hasAttribute('open')) {
            const summary = [...ancestor.children].find(child => child.tagName === 'SUMMARY');
            if (!summary?.contains(element)) return false;
          }
        }
        return true;
      };
      const text = element => element?.textContent?.trim() || null;
      const explorer = document.querySelector('select[aria-label="Explorer"]');
      const activeMode = document.querySelector('[role="group"][aria-label="Column types"] [aria-pressed="true"]');
      const activeSearchScope = document.querySelector('[role="group"][aria-label="Search scope"] [aria-pressed="true"]');
      const rawFields = document.querySelector('[data-testid="feature-catalog-raw-fields"]');
      const failureAlert = document.querySelector('[data-testid="builder-suggestions-error"]');
      const retry = document.querySelector('[data-testid="builder-suggestions-retry"]');
      const selectedOccurrence = document.querySelector(
        '[data-occurrence-id][aria-current="true"], [data-occurrence-id][aria-selected="true"], ' +
        '[data-occurrence-id][aria-pressed="true"], [data-occurrence-id][data-selected="true"]',
      );
      const failureText = text(failureAlert);
      const errorCode = visible(failureAlert)
        ? failureText?.match(/\(([A-Z][A-Z0-9_]*)\)\s*$/)?.[1] ?? null
        : null;
      return {
        explorerId: explorer?.value || null,
        explorerTitle: text(explorer?.selectedOptions?.[0]),
        tableHeading: text(document.querySelector('main h1')),
        fieldsModeSelected: activeMode ? text(activeMode) === 'Fields and related data' : null,
        searchScope: text(activeSearchScope),
        rawFieldsOpen: rawFields ? rawFields.open : null,
        selectedOccurrenceId: selectedOccurrence?.getAttribute('data-occurrence-id') ?? null,
        selectedOccurrenceObservable: Boolean(selectedOccurrence),
        failureAlertVisible: visible(failureAlert),
        failureAlertText: failureText,
        failureAlertCode: errorCode,
        retryVisible: visible(retry),
        retryEnabled: retry ? !retry.disabled : null,
      };
    });
    return { status: 'captured', capturedAtMs, ...state };
  } catch (error) {
    return {
      status: 'unavailable',
      capturedAtMs,
      captureError: error instanceof Error ? error.message : String(error),
    };
  }
};

export const waitForOwnedRootSuggestionRequest = async (requestSeen, timeoutMs = 5_000) => {
  let timer;
  try {
    return await Promise.race([
      requestSeen,
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error('Timed out waiting for the owned root suggestion request.')),
          timeoutMs);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
};

export const releaseSuggestionRouteAfterGate = async (
  route, actionCompleted, timeoutMs = 5_000, chronology = {}, beforeAbort,
) => {
  let timer;
  let completed = false;
  try {
    completed = await Promise.race([
      actionCompleted,
      new Promise(resolve => { timer = setTimeout(() => resolve(false), timeoutMs); }),
    ]) === true;
    if (completed && beforeAbort) chronology.uiBeforeAbort = await beforeAbort();
    return completed;
  } finally {
    clearTimeout(timer);
    chronology.releaseStartedAtMs = Date.now();
    chronology.releaseAction = completed ? 'abort' : 'continue';
    if (completed) {
      chronology.abortStartedAtMs = chronology.releaseStartedAtMs;
      await route.abort('failed');
    } else {
      await route.continue();
    }
    chronology.routeReleasedAtMs = Date.now();
  }
};

export const builderAuthoringSuggestionsWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  configureNativePage(page);
  const target = context.target;
  const sourceContents = readFileSync(join(target.fixtureDir, 'Patient.ndjson'));
  const sourceSHA256 = createHash('sha256').update(sourceContents).digest('hex');
  const sourceIds = sourceContents.toString('utf8').trim().split('\n').filter(Boolean)
    .map(line => JSON.parse(line).id).sort();
  const expectedIds = patientWindow(target, sourceIds);
  report.target.fixtureOracle = {
    path: join(target.fixtureDir, 'Patient.ndjson'),
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sourcePatientCount: sourceIds.length,
    sha256: sourceSHA256,
    previewIds: expectedIds,
  };
  const act = async (name, locator, perform, options = {}) => {
    await unique(locator);
    return action(name, locator, perform, options);
  };
  const title = `Verify ${context.runID.slice(-10)} suggestions`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  await page.getByText('New explorer', { exact: true }).waitFor({ state: 'visible' });

  // Shape only the new Explorer's read response so this case exercises the
  // lazy suggestions endpoint even though the development catalog is warm.
  const uiOrigin = new URL(target.uiUrl).origin;
  const projectPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/`;
  const shapedBuilderResponses = [];
  const builderResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return response.request().method() === 'GET' && url.origin === uiOrigin &&
      url.pathname.startsWith(projectPath) && url.pathname.endsWith('/authoring/v2/builder') &&
      explorerFromAuthoringPath(url.pathname) !== target.bootstrapExplorerId;
  });
  await page.route(url => url.origin === uiOrigin && url.pathname.startsWith(projectPath) &&
    url.pathname.endsWith('/authoring/v2/builder'), async route => {
    const request = route.request();
    const explorerId = explorerFromAuthoringPath(new URL(request.url()).pathname);
    if (request.method() !== 'GET' || !explorerId || explorerId === target.bootstrapExplorerId) {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    const body = await response.json();
    const candidates = body?.catalog?.candidates;
    if (!Array.isArray(candidates)) {
      shapedBuilderResponses.push({ explorerId, candidateCount: null, shaped: false });
      await route.fulfill({ response });
      return;
    }
    shapedBuilderResponses.push({ explorerId, candidateCount: candidates.length, shaped: true });
    await route.fulfill({ response, json: { ...body, catalog: { ...body.catalog, candidates: [] } } });
  });
  await act('open Explorer creation', page.getByText('New explorer', { exact: true }),
    () => page.getByText('New explorer', { exact: true }).click());
  const nameInput = page.locator('#new-explorer-name');
  await act('name Explorer', nameInput, () => nameInput.fill(title), { editable: true });
  const createBlank = page.getByRole('button', { name: 'Create blank' });
  await act('create blank Explorer', createBlank, () => createBlank.click());
  const shapedBuilderResponse = await builderResponse;
  assert.equal(shapedBuilderResponse.status(), 200, 'The new Explorer builder catalog read must succeed.');
  await page.waitForFunction(expectedTitle => {
    const select = document.querySelector('select[aria-label="Explorer"]');
    return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
  }, title);
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  check('correctness', 'created a fresh Explorer distinct from the bootstrap',
    Boolean(explorer && explorer !== target.bootstrapExplorerId), { title });
  report.target.explorer = explorer;
  const shapedCatalog = shapedBuilderResponses.find(entry => entry.explorerId === explorer);
  check('correctness', 'new Explorer catalog candidates are suppressed only in the test read response',
    Boolean(shapedCatalog?.shaped && Number(shapedCatalog.candidateCount) > 0),
    { explorer, originalCandidateCount: shapedCatalog?.candidateCount ?? null, shaped: shapedCatalog?.shaped ?? false });

  const suggestionAttempts = [];
  let driverSequence = 0;
  const suggestionsPath = `${projectPath}${encodeURIComponent(explorer)}/authoring/v2/suggestions`;
  const captureExplorerScopedUiState = async () => {
    const state = await captureSuggestionUiState(page);
    return {
      ...state,
      explorerMatchesRequest: state.status === 'captured' && typeof state.explorerId === 'string'
        ? state.explorerId === explorer : null,
    };
  };
  const suggestionsFaultChronology = {
    endpoint: '/authoring/v2/suggestions',
    firstTable: null,
    lazy: null,
    rootTableAction: {
      name: 'choose Patient rows',
      startedAtMs: null,
      startedSequence: null,
      actionCompleted: false,
      firstTableRequestId: null,
      firstTableResponseRequestId: null,
      firstTableResponseStatus: null,
      firstTableResponseOK: null,
      previewIds: null,
      previewMatchesFixture: null,
      completed: false,
      completedAtMs: null,
      completedSequence: null,
    },
  };
  report.target.suggestionsFaultChronology = suggestionsFaultChronology;
  let firstTableAttempt;
  let resolveFirstLazyRequest;
  const firstLazyRequestSeen = new Promise(resolve => { resolveFirstLazyRequest = resolve; });
  let resolveFirstLazyRouteReleased;
  const firstLazyRouteReleased = new Promise(resolve => { resolveFirstLazyRouteReleased = resolve; });
  let completeRootTableAction;
  const rootTableActionCompleted = new Promise(resolve => { completeRootTableAction = resolve; });
  let rootTableActionSignaled = false;
  let choosePatientRowsActionCompleted = false;
  const signalRootTableAction = (firstTableResponse, previewIds) => {
    if (rootTableActionSignaled) return;
    rootTableActionSignaled = true;
    const responseRequestId = firstTableResponse?.request()?.headers()['x-request-id'] ?? null;
    const previewMatchesFixture = Array.isArray(previewIds) &&
      JSON.stringify(previewIds) === JSON.stringify(expectedIds);
    const exactFirstTableResponse = Boolean(firstTableAttempt &&
      responseRequestId === firstTableAttempt.requestId &&
      firstTableResponse.status() === 200 && firstTableResponse.ok());
    Object.assign(suggestionsFaultChronology.rootTableAction, {
      actionCompleted: choosePatientRowsActionCompleted,
      firstTableRequestId: firstTableAttempt?.requestId ?? null,
      firstTableResponseRequestId: responseRequestId,
      firstTableResponseStatus: firstTableResponse?.status() ?? null,
      firstTableResponseOK: firstTableResponse?.ok() ?? null,
      previewIds: Array.isArray(previewIds) ? [...previewIds] : null,
      previewMatchesFixture,
      completed: choosePatientRowsActionCompleted && exactFirstTableResponse && previewMatchesFixture,
      completedAtMs: Date.now(),
      completedSequence: ++driverSequence,
    });
    completeRootTableAction(suggestionsFaultChronology.rootTableAction.completed);
  };
  await page.route(url => url.origin === uiOrigin && url.pathname === suggestionsPath, async route => {
    const request = route.request();
    if (request.method() !== 'POST') {
      await route.continue();
      return;
    }
    let body;
    try { body = request.postDataJSON(); } catch { body = undefined; }
    const requestId = request.headers()['x-request-id'] ?? body?.requestId ?? null;
    if (typeof requestId === 'string' && requestId.startsWith('first-table-suggestions-')) {
      const attempt = { kind: 'first-table', requestId, body, observedAtMs: Date.now() };
      attempt.evidence = sanitizedSuggestionRequestBinding({
        kind: attempt.kind, requestId, body, observedAtMs: attempt.observedAtMs,
      });
      attempt.evidence.sequence = ++driverSequence;
      attempt.evidence.projectId = target.fixtureProject;
      attempt.evidence.explorerId = explorer;
      suggestionsFaultChronology.firstTable = attempt.evidence;
      firstTableAttempt ??= attempt;
      suggestionAttempts.push(attempt);
      await route.continue();
      return;
    }
    if (isOwnedRootSuggestionRequest({
      method: request.method(),
      url: request.url(),
      expectedURL: `${uiOrigin}${suggestionsPath}`,
      requestId,
      body,
      rootNodeId: firstTableAttempt?.body?.nodeId,
      snapshotToken: firstTableAttempt?.body?.snapshotToken,
    })) {
      const attempt = { kind: 'lazy', requestId, body, explorer, observedAtMs: Date.now() };
      attempt.evidence = sanitizedSuggestionRequestBinding({
        kind: attempt.kind, requestId, body, observedAtMs: attempt.observedAtMs,
      });
      attempt.evidence.sequence = ++driverSequence;
      attempt.evidence.projectId = target.fixtureProject;
      attempt.evidence.requestOccurrenceId = requestId.startsWith('suggestions-')
        ? requestId.slice('suggestions-'.length) : null;
      attempt.evidence.explorerId = explorer;
      attempt.evidence.uiAtRequest = await captureExplorerScopedUiState();
      attempt.evidence.requestDuringRootTableAction =
        suggestionsFaultChronology.rootTableAction.startedSequence !== null &&
        attempt.evidence.sequence > suggestionsFaultChronology.rootTableAction.startedSequence &&
        suggestionsFaultChronology.rootTableAction.completed !== true;
      attempt.evidence.sameRootNodeAsFirstTable = body?.nodeId === firstTableAttempt?.body?.nodeId;
      attempt.evidence.sameSnapshotAsFirstTable = body?.snapshotToken === firstTableAttempt?.body?.snapshotToken;
      attempt.evidence.requestIdDiffersFromFirstTable = requestId !== firstTableAttempt?.requestId;
      suggestionsFaultChronology.lazy ??= attempt.evidence;
      suggestionAttempts.push(attempt);
      if (suggestionAttempts.filter(candidate => candidate.kind === 'lazy').length === 1) {
        attempt.evidence.faultArmedAtMs = Date.now();
        attempt.evidence.actionGateWaitStartedAtMs = Date.now();
        attempt.evidence.actionGateWaitTimeoutMs = 5_000;
        const chronology = {};
        resolveFirstLazyRequest(attempt);
        try {
          attempt.actionCompletedBeforeAbort = await releaseSuggestionRouteAfterGate(
            route, rootTableActionCompleted, 5_000, chronology,
            async () => {
              chronology.uiBeforeAbortSequence = ++driverSequence;
              return {
                ...await captureExplorerScopedUiState(),
                rootTableActionCompleted: suggestionsFaultChronology.rootTableAction.completed,
                firstTableResponseStatus: suggestionsFaultChronology.rootTableAction.firstTableResponseStatus,
                previewIds: suggestionsFaultChronology.rootTableAction.previewIds,
                previewMatchesFixture: suggestionsFaultChronology.rootTableAction.previewMatchesFixture,
                rootTableActionCompletedSequence:
                  suggestionsFaultChronology.rootTableAction.completedSequence,
              };
            });
          attempt.evidence.actionCompletedBeforeAbort = attempt.actionCompletedBeforeAbort;
          attempt.injectedFault = attempt.actionCompletedBeforeAbort;
          attempt.evidence.injectedFault = attempt.actionCompletedBeforeAbort;
          attempt.evidence.abortAction = chronology.releaseAction === 'abort' ? 'failed' : null;
          suggestionsFaultChronology.lazy.actionCompletedBeforeAbort = attempt.actionCompletedBeforeAbort;
        } finally {
          Object.assign(attempt.evidence, chronology);
          attempt.routeReleased = Number.isFinite(chronology.routeReleasedAtMs);
          attempt.evidence.routeReleased = attempt.routeReleased;
          suggestionsFaultChronology.lazy.actionGateWaitStartedAtMs = attempt.evidence.actionGateWaitStartedAtMs;
          suggestionsFaultChronology.lazy.actionGateWaitTimeoutMs = attempt.evidence.actionGateWaitTimeoutMs;
          suggestionsFaultChronology.lazy.actionCompletedBeforeAbort =
            attempt.evidence.actionCompletedBeforeAbort ?? false;
          suggestionsFaultChronology.lazy.abortAction = attempt.evidence.abortAction;
          suggestionsFaultChronology.lazy.releaseAction = chronology.releaseAction ?? null;
          suggestionsFaultChronology.lazy.releaseStartedAtMs = chronology.releaseStartedAtMs ?? null;
          suggestionsFaultChronology.lazy.abortStartedAtMs = chronology.abortStartedAtMs ?? null;
          suggestionsFaultChronology.lazy.routeReleasedAtMs = chronology.routeReleasedAtMs ?? null;
          suggestionsFaultChronology.lazy.routeReleased = attempt.routeReleased;
          attempt.evidence.routeReleasedSequence = ++driverSequence;
          suggestionsFaultChronology.lazy.routeReleasedSequence = attempt.evidence.routeReleasedSequence;
          const routeReleasedAfterSettledPreview = attempt.actionCompletedBeforeAbort === true &&
            chronology.releaseAction === 'abort' &&
            attempt.routeReleased === true &&
            chronology.uiBeforeAbort?.rootTableActionCompleted === true &&
            chronology.uiBeforeAbort?.firstTableResponseStatus === 200 &&
            chronology.uiBeforeAbort?.previewMatchesFixture === true &&
            chronology.uiBeforeAbortSequence > chronology.uiBeforeAbort?.rootTableActionCompletedSequence;
          check('correctness', 'injected lazy route was released after the exact root preview settled',
            routeReleasedAfterSettledPreview, {
            actionCompletedBeforeAbort: attempt.actionCompletedBeforeAbort === true,
            routeReleased: attempt.routeReleased === true,
            firstTableResponseStatus: chronology.uiBeforeAbort?.firstTableResponseStatus ?? null,
            previewIdsBeforeAbort: chronology.uiBeforeAbort?.previewIds ?? null,
            previewMatchesFixture: chronology.uiBeforeAbort?.previewMatchesFixture ?? null,
          });
          suggestionsFaultChronology.lazy.uiBeforeAbort = chronology.uiBeforeAbort ?? null;
          resolveFirstLazyRouteReleased(attempt);
        }
        return;
      }
    }
    await route.continue();
  });

  const tableName = page.locator('#first-table-name');
  let firstLazyAttempt;
  let firstTableResponse;
  let initialPatientIds;
  try {
    await act('name Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
    const firstTableSuggestionsResponse = page.waitForResponse(response => {
      const request = response.request();
      const url = new URL(response.url());
      return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === suggestionsPath &&
        (request.headers()['x-request-id'] ?? '').startsWith('first-table-suggestions-');
    });
    suggestionsFaultChronology.rootTableAction.startedAtMs = Date.now();
    suggestionsFaultChronology.rootTableAction.startedSequence = ++driverSequence;
    await act('choose Patient rows', page.getByRole('button', { name: 'Choose Patient rows' }),
      () => page.getByRole('button', { name: 'Choose Patient rows' }).click());
    choosePatientRowsActionCompleted = true;
    firstTableResponse = await firstTableSuggestionsResponse;
    if (firstTableAttempt?.evidence) {
      firstTableAttempt.evidence.responseStatus = firstTableResponse.status();
      firstTableAttempt.evidence.responseOK = firstTableResponse.ok();
      firstTableAttempt.evidence.responseObservedAtMs = Date.now();
    }
    const rootNodeId = firstTableAttempt?.body?.nodeId;
    const rootSnapshotToken = firstTableAttempt?.body?.snapshotToken;
    check('correctness', 'first-table root suggestions request succeeds before the lazy retry case',
      Boolean(firstTableAttempt && firstTableResponse.ok() && firstTableAttempt.requestId.startsWith('first-table-suggestions-') &&
        typeof rootNodeId === 'string' && rootNodeId.length > 0 && typeof rootSnapshotToken === 'string'),
      { requestId: firstTableAttempt?.requestId ?? null, nodeId: rootNodeId ?? null,
        snapshotToken: rootSnapshotToken ?? null, status: firstTableResponse.status() });
    await page.waitForFunction(rowCount =>
      document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
    expectedIds.length + 1);
    initialPatientIds = await assertPreviewPatientIds(page, expectedIds);
    check('correctness', 'native Patient root preview renders exact independent fixture IDs before candidate selection',
      JSON.stringify(initialPatientIds) === JSON.stringify(expectedIds),
      { expectedIds, visibleIds: initialPatientIds });
    signalRootTableAction(firstTableResponse, initialPatientIds);
    firstLazyAttempt = await waitForOwnedRootSuggestionRequest(firstLazyRequestSeen, 5_000);
    await firstLazyRouteReleased;
    check('correctness', 'injected transport fault owns the exact lazy Patient root request and snapshot',
      isOwnedRootSuggestionRequest({
        method: 'POST',
        url: `${uiOrigin}${suggestionsPath}`,
        expectedURL: `${uiOrigin}${suggestionsPath}`,
        requestId: firstLazyAttempt.requestId,
        body: firstLazyAttempt.body,
        rootNodeId,
        snapshotToken: rootSnapshotToken,
      }) && firstLazyAttempt.injectedFault,
      { requestId: firstLazyAttempt.requestId, rootNodeId: firstLazyAttempt.body?.nodeId,
        snapshotToken: firstLazyAttempt.body?.snapshotToken });
    assert(firstLazyAttempt.injectedFault,
      'The exact root suggestion request may be aborted only after the root-table response and preview gate passes.');
  } finally {
    signalRootTableAction(firstTableResponse, initialPatientIds);
  }
  const rawFieldSection = page.getByTestId('feature-catalog-raw-fields');
  const failedLazyAlert = page.getByTestId('builder-suggestions-error');
  let failureRecord;
  let failureUiState;
  const lazyAttemptsBeforeRetry = suggestionAttempts.filter(candidate => candidate.kind === 'lazy');
  try {
    await failedLazyAlert.waitFor({ state: 'visible' });
  } finally {
    failureRecord = report.network.find(item => item.kind === 'network' &&
      item.requestDetails?.requestId === firstLazyAttempt?.requestId && item.method === 'POST' &&
      item.url === `${uiOrigin}${suggestionsPath}` && item.rawURL === `${uiOrigin}${suggestionsPath}` &&
      item.errorText === 'net::ERR_FAILED' && typeof item.playwrightRequestId === 'string');
    suggestionsFaultChronology.lazy.failureRecordCaptured = Boolean(failureRecord);
    suggestionsFaultChronology.lazy.failureObservedAtMs = failureRecord ? Date.now() : null;
    failureUiState = await captureExplorerScopedUiState();
    const failureCaptureSequence = ++driverSequence;
    suggestionsFaultChronology.lazy.catchOutcome = {
      observedAtMs: failureUiState.capturedAtMs,
      sequence: failureCaptureSequence,
      requestFailureObserved: Boolean(failureRecord),
      requestErrorText: failureRecord?.errorText ?? null,
      failureAlertVisible: failureUiState.failureAlertVisible ?? null,
      failureAlertText: failureUiState.failureAlertText ?? null,
      appErrorCodeFromVisibleAlert: failureUiState.failureAlertCode ?? null,
      retryVisible: failureUiState.retryVisible ?? null,
      requestIdentity: {
        projectId: target.fixtureProject,
        explorerId: explorer,
        occurrenceIdFromRequestId: firstLazyAttempt?.evidence?.requestOccurrenceId ?? null,
        nodeId: firstLazyAttempt?.evidence?.nodeId ?? null,
        snapshotTokenSHA256: firstLazyAttempt?.evidence?.snapshotTokenSHA256 ?? null,
      },
      catchVisibleIdentity: {
        explorerId: failureUiState.explorerId ?? null,
        explorerMatchesRequest: typeof failureUiState.explorerId === 'string'
          ? failureUiState.explorerId === explorer : null,
        selectedOccurrenceId: failureUiState.selectedOccurrenceId ?? null,
        selectedOccurrenceObservable: failureUiState.selectedOccurrenceObservable ?? false,
        snapshotTokenSHA256: null,
        snapshotTokenObservable: false,
      },
    };
    check('correctness', 'lazy suggestion failure UI state was captured for the owned request',
      failureUiState.status === 'captured', {
        requestId: firstLazyAttempt?.requestId ?? null,
        captureStatus: failureUiState.status,
        failureAlertVisible: failureUiState.failureAlertVisible ?? null,
        appErrorCodeFromVisibleAlert: failureUiState.failureAlertCode ?? null,
      });
    const retryControlAvailable = failureUiState.failureAlertVisible === true &&
      failureUiState.retryVisible === true && failureUiState.retryEnabled === true &&
      lazyAttemptsBeforeRetry.length === 1;
    check('usability', 'one failed lazy suggestion request exposes an actionable retry control',
      retryControlAvailable, {
        retry: {
          visible: failureUiState.retryVisible ?? null,
          enabled: failureUiState.retryEnabled ?? null,
        },
        failureAlertVisible: failureUiState.failureAlertVisible ?? null,
        lazyFailureRequestId: firstLazyAttempt?.requestId ?? null,
        lazyAttemptCount: lazyAttemptsBeforeRetry.length,
      });
    if (failureRecord) {
      failureRecord.injectedFault = true;
      failureRecord.injectedAction = 'abort';
      failureRecord.injectedRequestId = firstLazyAttempt.requestId;
      failureRecord.injectedReason = 'the first lazy Patient suggestion request was intentionally aborted by this case';
    }
  }
  const failedLazyAttempt = lazyAttemptsBeforeRetry[0];
  assert.equal(failedLazyAttempt, firstLazyAttempt,
    'The exact root request must be aborted only after the root-table preview gate passes.');
  assert(failureRecord, 'The report must retain the exact failed request signature for the injected lazy suggestion transport failure.');
  const retry = page.getByTestId('builder-suggestions-retry');
  await unique(retry);
  const retryResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === suggestionsPath &&
      (request.headers()['x-request-id'] ?? '') === firstLazyAttempt.requestId && response.ok();
  });
  await act('retry finding columns', retry, () => retry.click());
  const retryResponse = await retryResponsePromise;
  const lazyAttempts = suggestionAttempts.filter(candidate => candidate.kind === 'lazy');
  check('correctness', 'retry resends the same Explorer, snapshot, and Patient root suggestion request',
    lazyAttempts.length === 2 && lazyAttempts[1].explorer === explorer &&
      lazyAttempts[1].requestId === firstLazyAttempt.requestId &&
      JSON.stringify(lazyAttempts[1].body) === JSON.stringify(firstLazyAttempt.body) && retryResponse.ok(),
    { explorer, requestIds: lazyAttempts.map(candidate => candidate.requestId),
      bodies: lazyAttempts.map(candidate => candidate.body), retryStatus: retryResponse.status() });
  await failedLazyAlert.waitFor({ state: 'hidden' });
  await page.getByTestId('construction-action-add-columns').waitFor({ state: 'visible' });
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-add-columns"]');
    return button && !button.disabled;
  });
  const addColumns = page.getByRole('button', { name: /Add columns:/ });
  await act('open Add columns', addColumns, () => addColumns.click());
  await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' });
  const fieldsRelated = page.getByRole('button', { name: 'Fields and related data' });
  await act('choose Fields and related data', fieldsRelated, () => fieldsRelated.click());
  const rawFields = page.getByText('Raw FHIR fields (advanced)', { exact: true });
  await act('open Raw FHIR fields', rawFields, () => rawFields.click(), {
    after: async () => page.getByTestId('feature-catalog-raw-fields').waitFor({ state: 'visible' }),
  });
  const catalogChoices = rawFieldSection.locator('input[aria-label^="Select Patient."]');
  await catalogChoices.first().waitFor({ state: 'visible' });
  const candidateCount = await catalogChoices.count();
  report.target.candidateEvidence = {
    source: 'Builder rendered Patient candidate controls after the lazy suggestions retry',
    count: candidateCount,
    labels: await catalogChoices.evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label')).sort()),
  };
  const idCandidate = rawFieldSection.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
  await unique(idCandidate);
  await idCandidate.waitFor({ state: 'visible' });
  const idCandidateState = {
    visible: await idCandidate.isVisible(),
    enabled: await idCandidate.isEnabled(),
    checked: await idCandidate.isChecked(),
  };
  check('correctness', 'Patient candidates render after the successful lazy suggestion retry', Number(candidateCount) > 0,
    { candidateCount, labels: report.target.candidateEvidence.labels });
  check('usability', 'Patient ID candidate control is visible and actionable',
    idCandidateState.visible && idCandidateState.enabled && !idCandidateState.checked,
    idCandidateState);
  await act('select Patient ID candidate', idCandidate, () => idCandidate.check());
  const addSelectedFeature = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  await act('add selected Patient.id feature', addSelectedFeature, () => addSelectedFeature.click(), {
    after: async () => page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'visible' }),
  });
  const proposalPanel = page.getByTestId('construction-choice-proposal-panel');
  await page.waitForFunction(() => ['ready', 'error'].includes(
    document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus));
  const proposalEvidence = await proposalPanel.evaluate(panel => ({
    status: panel.dataset.proposalStatus,
    text: panel.innerText,
    coverage: [...panel.querySelectorAll('[data-testid="construction-preview-value-coverage"] li')]
      .map(item => item.innerText.trim()),
  }));
  report.target.patientIdProposal = proposalEvidence;
  const applyColumns = proposalPanel.getByRole('button', { name: 'Apply columns', exact: true });
  const proposalReady = proposalEvidence.status === 'ready' && /\b1 new column\b/.test(proposalEvidence.text) &&
    proposalEvidence.coverage.length === 1 && await applyColumns.isVisible() && await applyColumns.isEnabled();
  assert(proposalReady, `Patient.id proposal must be ready with one visible column before Apply: ${JSON.stringify(proposalEvidence)}`);
  await act('apply Patient.id column', applyColumns, () => applyColumns.click(), {
    after: async () => {
      await proposalPanel.waitFor({ state: 'hidden' });
      await page.waitForFunction(({ rowCount, columnCount }) => {
        const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
        return table?.getAttribute('aria-rowcount') === String(rowCount) &&
          table?.getAttribute('aria-colcount') === String(columnCount) &&
          !document.body.innerText.includes('Loading your table…');
      }, { rowCount: expectedIds.length + 1, columnCount: 2 });
    },
  });
  const settledRows = await page.getByTestId('preview-table-scroll').getByRole('row').all();
  const settledPatientFields = await Promise.all(settledRows.slice(1).map(async row => {
    const cells = await row.getByRole('cell').all();
    return cells.length >= 2 ? (await cells[1].innerText()).trim() : null;
  }));
  const settledPatientIds = await assertPreviewPatientIds(page, expectedIds);
  const settledPatientFieldIds = settledPatientFields.filter(value => value !== null).sort();
  const exactAppliedPatientIDs = JSON.stringify(settledPatientIds) === JSON.stringify(expectedIds) &&
    JSON.stringify(settledPatientFieldIds) === JSON.stringify(expectedIds);
  check('correctness', 'native Patient.id proposal applies and settled preview renders exact independent fixture IDs',
    exactAppliedPatientIDs,
    { expectedIds, visibleRowIDs: settledPatientIds, addedPatientIDColumnValues: settledPatientFieldIds,
      proposal: proposalEvidence });
  checkUnexpectedDiagnostics(report, target, failureRecord);

  const sourceSHA256After = createHash('sha256')
    .update(readFileSync(join(target.fixtureDir, 'Patient.ndjson'))).digest('hex');
  recordCheck(report, 'correctness', 'independent Patient source stayed unchanged during browser run',
    sourceSHA256After === sourceSHA256, { before: sourceSHA256, after: sourceSHA256After });
};
