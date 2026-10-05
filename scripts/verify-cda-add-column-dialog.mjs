import assert from 'node:assert/strict';
import { actionScopeContains, cancellationScope, matchesOwnedCatalogRequest } from './lib/action-scoped-cancellations.mjs';
import { sanitizeBody } from './lib/playwright-browser.mjs';

export async function addColumnDialogWorkflow({ page, cda, expect }) {
  const { project, explorer, apiOrigin, uiOrigin } = cda;
  const evidence = cda.evidence;
  const base = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
  const url = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  const report = Object.assign(cda.report, { project, explorer, evidence, url, dialogs: [], transitions: [] });

  async function readBuilder() {
    const response = await cda.request.get(`${apiOrigin}${base}/builder`, { timeout: 30000 });
    const body = await response.text();
    report.apiReads ??= [];
    report.apiReads.push({ path: `${base}/builder`, status: response.status(), ...(response.ok() ? {} : { body: sanitizeBody(body) }) });
    assert(response.ok(), `Builder read returned ${response.status()}: ${sanitizeBody(body)}`);
    return JSON.parse(body);
  }

  const expectedAbortEndpoints = new Set(['frame-source-options', 'semantic-inventory', 'construction-choice-proposals']);
  const pendingActionRequests = new Map();
  const armedCancellationScopes = [];
  let currentCancellationAction;
  function requestPayload(request) {
    try { return request.postDataJSON(); } catch { return undefined; }
  }
  function matchesExpectedRequest(request, endpoint) {
    return matchesOwnedCatalogRequest(request, endpoint, {
      uiOrigin, pathPrefix: base, snapshotToken: report.before?.catalog?.snapshotToken,
      documents: report.before?.workspace?.documents ?? [],
      draftVersion: report.before?.draftVersion, draftDigest: report.before?.draftDigest,
    });
  }
  function armExpectedCancellations(action, endpoints) {
    const candidates = [...pendingActionRequests.keys()].filter(request => endpoints.some(endpoint => matchesExpectedRequest(request, endpoint)));
    const scope = cancellationScope(action, endpoints, candidates);
    armedCancellationScopes.push(scope);
    currentCancellationAction = action;
    report.cancellationArms ??= [];
    report.cancellationArms.push({ action, pendingMatched: candidates.length, endpoints: [...endpoints], expiryMs: 5000 });
  }
  function classifyExpectedRequestFailure(request) {
    if (request.failure()?.errorText !== 'net::ERR_ABORTED') return undefined;
    for (const scope of armedCancellationScopes) {
      if (!actionScopeContains(scope, request, currentCancellationAction)) continue;
      scope.requests.delete(request);
      const entry = pendingActionRequests.get(request);
      pendingActionRequests.delete(request);
      if (!entry) return undefined;
      return `UI cancellation after ${scope.action}; endpoint=${entry.endpoint}; outputId=${entry.payload.outputId}`;
    }
    return undefined;
  }

  const timedAction = async (name, locator, method, settled, arg = null, cancellationEndpoints = []) => {
    const startedAt = Date.now();
    currentCancellationAction = name;
    report.activeAction = { label: name, locator: locator.toString(), startedAt };
    if (cancellationEndpoints.length) armExpectedCancellations(name, cancellationEndpoints);
    await cda.action(name, locator, () => method(locator), {
      timeout: 5000,
      after: () => page.waitForFunction(settled, arg, { timeout: 5000 }),
    });
    const elapsedMs = Date.now() - startedAt;
    report.transitions.push({ name, elapsedMs, limitMs: 5000, passed: elapsedMs <= 5000 });
    assert(elapsedMs <= 5000, `${name} took ${elapsedMs} ms to render`);
  };

  const requestCapture = cda.captureRequests(base, { responsePaths: /frame-source-options|semantic-inventory|construction-choice-proposals|builder/ });
  page.on('request', request => {
    if (request.method() !== 'POST') return;
    const requestURL = new URL(request.url());
    const prefix = `${base}/`;
    if (requestURL.origin !== uiOrigin || !requestURL.pathname.startsWith(prefix)) return;
    const endpoint = requestURL.pathname.slice(prefix.length);
    if (!expectedAbortEndpoints.has(endpoint)) return;
    const payload = requestPayload(request);
    if (!payload) return;
    pendingActionRequests.set(request, { endpoint, payload });
    for (const scope of armedCancellationScopes) {
      if (report.activeAction?.label === scope.action && Date.now() - scope.armedAt <= 5000 &&
          matchesExpectedRequest(request, endpoint) && scope.endpoints.includes(endpoint)) {
        scope.requests.add(request);
      }
    }
  });
  page.on('requestfinished', request => pendingActionRequests.delete(request));
  page.on('requestfailed', request => {
    const entry = pendingActionRequests.get(request);
    if (entry) {
      report.requestFailureAudit ??= [];
      report.requestFailureAudit.push({
        endpoint: entry.endpoint,
        requestId: request.headers()['x-request-id'] ?? null,
        action: currentCancellationAction ?? null,
        matchedScopes: armedCancellationScopes.filter(scope => scope.requests.has(request))
          .map(scope => ({ action: scope.action, ageMs: Date.now() - scope.armedAt })),
      });
    }
    const expectedCancellation = classifyExpectedRequestFailure(request);
    if (expectedCancellation) {
      cda.expectCanceledRequest(request, expectedCancellation, {
        endpoint: entry?.endpoint,
        outputId: entry?.payload?.outputId,
        action: currentCancellationAction,
      });
      report.expectedCancellations ??= [];
      report.expectedCancellations.push({ url: request.url(), requestId: request.headers()['x-request-id'] ?? null,
        method: request.method(), action: currentCancellationAction, reason: expectedCancellation });
    }
    pendingActionRequests.delete(request);
  });

  await page.setViewportSize({ width: 1280, height: 900 });
  report.before = await readBuilder();
  await cda.navigate(url);
  await page.getByText('Dataset workspace', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
  const openSuggestions = async phase => {
    await timedAction(`${phase}: open Add columns editor`, page.getByTestId('construction-action-add-columns'), target => target.click(),
      () => document.querySelector('[data-testid="construction-operation-editor"]')?.getAttribute('data-operation-family') === 'ADD_COLUMNS', null,
      ['frame-source-options', 'semantic-inventory']);
    const fieldsTab = page.getByRole('button', { name: 'Fields and related data', exact: true });
    await timedAction(`${phase}: open Fields and related data`, fieldsTab, target => target.click(),
      () => document.querySelector('[data-testid="construction-add-columns-source"]') !== null, null,
      ['semantic-inventory', 'frame-source-options']);
    const codedTab = page.getByRole('button', { name: 'Coded values', exact: true });
    await timedAction(`${phase}: show coded suggestions`, codedTab, target => target.click(),
      count => document.querySelectorAll('[data-testid^="paired-column-suggestion-"]').length >= count, report.suggestionCount ?? 3,
      ['semantic-inventory']);
  };
  await openSuggestions('initial');
  const suggestions = page.locator('[data-testid^="paired-column-suggestion-"]');
  const suggestionIdentities = await suggestions.evaluateAll(buttons => buttons.map(button => ({ accessibleName: button.getAttribute('aria-label'), text: button.innerText.trim() })));
  const suggestionCount = suggestionIdentities.length;
  assert(suggestionCount >= 3, `The current CDA table has ${suggestionCount} ready coded-value suggestions; expected at least three`);
  assert.equal(new Set(suggestionIdentities.map(item => item.accessibleName)).size, suggestionCount, 'Semantic coded-value suggestion names must be unique');
  report.suggestionCount = suggestionCount;

  for (let index = 0; index < suggestionIdentities.length; index += 1) {
    const suggestionIdentity = suggestionIdentities[index];
    const suggestion = page.getByRole('button', { name: suggestionIdentity.accessibleName, exact: true });
    await expect(suggestion).toBeVisible({ timeout: 5000 });
    await expect(suggestion).toBeEnabled({ timeout: 5000 });
    const startedAt = Date.now();
    const openLabel = `open suggestion ${suggestionIdentity.accessibleName}`;
    armExpectedCancellations(openLabel, ['semantic-inventory']);
    report.activeAction = { label: openLabel, locator: suggestion.toString(), startedAt };
    await cda.action(`open ${suggestionIdentity.accessibleName}`, suggestion, () => suggestion.click());
    const dialog = page.getByRole('dialog');
    await dialog.waitFor({ state: 'visible', timeout: 5000 });
    await expectUnique(dialog, `dialog for ${suggestionIdentity.accessibleName}`);
    const openElapsedMs = Date.now() - startedAt;
    report.transitions.push({ name: `dialog-open-${suggestionIdentity.accessibleName}`, elapsedMs: openElapsedMs, limitMs: 5000, passed: openElapsedMs <= 5000 });
    assert(openElapsedMs <= 5000, `Dialog for ${suggestionIdentity.accessibleName} rendered in ${openElapsedMs} ms`);
    const details = await dialog.evaluate(element => {
      const rect = element.getBoundingClientRect();
      return {
        text: element.innerText.slice(0, 240),
        parent: element.parentElement?.parentElement?.tagName,
        rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
        viewport: { width: innerWidth, height: innerHeight },
        routeChoices: element.querySelectorAll('input[type="radio"]').length,
      };
    });
    assert(details.text.includes('Choose how to add these fields'), `${suggestionIdentity.accessibleName} opened an unexpected dialog`);
    assert.equal(details.parent, 'BODY', `${suggestionIdentity.accessibleName} dialog must be portaled outside a disclosure`);
    assert(details.rect.width > 0 && details.rect.height > 0 && details.rect.y >= 0 && details.rect.y < details.viewport.height, 'Dialog must be visible within the viewport');
    assert(details.routeChoices > 0, `${suggestionIdentity.accessibleName} has no route choice`);
    if (process.env.LOOM_VERIFY_SCREENSHOTS === '1') {
      await page.screenshot({ path: `${evidence}/dialog-${index + 1}.png`, fullPage: true });
    }
    const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
    const closeStartedAt = Date.now();
    const cancelLabel = `cancel ${suggestionIdentity.accessibleName} dialog`;
    armExpectedCancellations(cancelLabel, ['construction-choice-proposals']);
    report.activeAction = { label: cancelLabel, locator: cancel.toString(), startedAt: closeStartedAt };
    await cda.action(`cancel ${suggestionIdentity.accessibleName} dialog`, cancel, () => cancel.click());
    await dialog.waitFor({ state: 'hidden', timeout: 5000 });
    const closeElapsedMs = Date.now() - closeStartedAt;
    report.transitions.push({ name: `dialog-close-${suggestionIdentity.accessibleName}`, elapsedMs: closeElapsedMs, limitMs: 5000, passed: closeElapsedMs <= 5000 });
    assert(closeElapsedMs <= 5000, `Dialog for ${suggestionIdentity.accessibleName} closed in ${closeElapsedMs} ms`);
    const current = await readBuilder();
    assert.deepEqual(current.workspace, report.before.workspace, 'Cancel must leave the Builder workspace unchanged');
    assert.equal(current.draftVersion, report.before.draftVersion, 'Cancel must not advance the draft version');
    assert.equal(current.draftDigest, report.before.draftDigest, 'Cancel must not change the draft digest');
    report.dialogs.push({ suggestion: suggestionIdentity, ...details, cancelled: true, workspaceUnchanged: true });
    await timedAction(`after cancel ${index + 1}: return to Builder table`, page.getByTestId('construction-close-operation-editor'), target => target.click(),
      () => document.querySelector('[data-testid="construction-operation-editor"]') === null, null,
      ['frame-source-options', 'semantic-inventory', 'construction-choice-proposals']);
    await openSuggestions(`after cancel ${index + 1}`);
    for (const initialIdentity of suggestionIdentities) {
      const currentSuggestion = page.getByRole('button', { name: initialIdentity.accessibleName, exact: true });
      await currentSuggestion.waitFor({ state: 'visible', timeout: 5000 });
      await expectUnique(currentSuggestion, `restored coded suggestion ${initialIdentity.accessibleName}`);
    }
    report.cancelledInventoryChecks = (report.cancelledInventoryChecks ?? 0) + 1;
  }
  await requestCapture.flush();
  cda.includeBrowserDiagnostics();
  assert.deepEqual(cda.diagnostics.console, [], 'Unexpected browser console errors');
  assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected browser exceptions');
  assert.deepEqual(cda.diagnostics.httpFailures, [], 'Unexpected HTTP responses');
  const unexpectedNetworkFailures = cda.diagnostics.networkFailures.filter(failure => !failure.expected);
  assert.deepEqual(unexpectedNetworkFailures, [], 'Unexpected local network failures');
  report.status = 'passed';
  return report;
}

async function expectUnique(locator, label) {
  await expect(locator, `${label}: expected a unique target`).toHaveCount(1, { timeout: 5000 });
}
