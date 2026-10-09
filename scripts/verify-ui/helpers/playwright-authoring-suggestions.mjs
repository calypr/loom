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
  const suggestionsPath = `${projectPath}${encodeURIComponent(explorer)}/authoring/v2/suggestions`;
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
      suggestionAttempts.push({ kind: 'first-table', requestId, body });
      await route.continue();
      return;
    }
    if (typeof requestId === 'string' && requestId.startsWith('suggestions-')) {
      const attempt = { kind: 'lazy', requestId, body, explorer };
      suggestionAttempts.push(attempt);
      if (suggestionAttempts.filter(candidate => candidate.kind === 'lazy').length === 1) {
        attempt.injectedFault = true;
        await route.abort('failed');
        return;
      }
    }
    await route.continue();
  });

  const tableName = page.locator('#first-table-name');
  await act('name Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
  const firstTableSuggestionsResponse = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === suggestionsPath &&
      (request.headers()['x-request-id'] ?? '').startsWith('first-table-suggestions-');
  });
  await act('choose Patient rows', page.getByRole('button', { name: 'Choose Patient rows' }),
    () => page.getByRole('button', { name: 'Choose Patient rows' }).click());
  const firstTableResponse = await firstTableSuggestionsResponse;
  const firstTableAttempt = suggestionAttempts.find(candidate => candidate.kind === 'first-table');
  check('correctness', 'first-table Patient suggestions request succeeds before the lazy retry case',
    Boolean(firstTableAttempt && firstTableResponse.ok() && firstTableAttempt.requestId.startsWith('first-table-suggestions-')),
    { requestId: firstTableAttempt?.requestId ?? null, status: firstTableResponse.status() });
  await page.waitForFunction(rowCount =>
    document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
  expectedIds.length + 1);
  const initialPatientIds = await assertPreviewPatientIds(page, expectedIds);
  check('correctness', 'native Patient root preview renders exact independent fixture IDs before candidate selection',
    JSON.stringify(initialPatientIds) === JSON.stringify(expectedIds),
    { expectedIds, visibleIds: initialPatientIds });
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
  await act('open Raw FHIR fields', rawFields, () => rawFields.click());
  const rawFieldSection = page.getByTestId('feature-catalog-raw-fields');
  const failedLazyAlert = page.getByTestId('builder-suggestions-error');
  await failedLazyAlert.waitFor({ state: 'visible' });
  const lazyAttemptsBeforeRetry = suggestionAttempts.filter(candidate => candidate.kind === 'lazy');
  const firstLazyAttempt = lazyAttemptsBeforeRetry[0];
  assert(firstLazyAttempt?.injectedFault, 'The first lazy Patient suggestions request must be the injected transport failure.');
  const failureRecord = report.network.find(item => item.kind === 'network' &&
    item.requestDetails?.requestId === firstLazyAttempt.requestId && item.method === 'POST' &&
    item.url === `${uiOrigin}${suggestionsPath}` && item.rawURL === `${uiOrigin}${suggestionsPath}` &&
    item.errorText === 'net::ERR_FAILED' && typeof item.playwrightRequestId === 'string');
  assert(failureRecord, 'The report must retain the exact failed request signature for the injected lazy suggestion transport failure.');
  failureRecord.injectedFault = true;
  failureRecord.injectedAction = 'abort';
  failureRecord.injectedRequestId = firstLazyAttempt.requestId;
  failureRecord.injectedReason = 'the first lazy Patient suggestion request was intentionally aborted by this case';
  const retry = page.getByTestId('builder-suggestions-retry');
  await unique(retry);
  const retryState = { visible: await retry.isVisible(), enabled: await retry.isEnabled() };
  check('usability', 'one failed lazy suggestion request exposes an actionable retry control',
    retryState.visible && retryState.enabled && lazyAttemptsBeforeRetry.length === 1,
    { retry: retryState, lazyFailureRequestId: firstLazyAttempt.requestId });
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
