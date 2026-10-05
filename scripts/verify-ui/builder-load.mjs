import { expect } from '@playwright/test';
import { browserURL } from './builder-url.mjs';
import { recordCheck } from './report.mjs';

const retryLabel = /^(?:Try again|Retry(?:\s+(?:Builder|Explorer|capabilit)[\w ]*)?|Reload capabilities)$/i;

export const isLoadedBuilderSnapshot = (snapshot, expectedExplorer) => Boolean(
  snapshot && snapshot.selectedExplorerId === expectedExplorer &&
  (snapshot.emptyWorkspaceVisible || (snapshot.tableCount > 0 && snapshot.previewStatus === 'ready')),
);
const ownedRead = (target, caseName) => {
  const explorers = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  return {
    method: 'GET',
    path: caseName === 'list'
      ? explorers
      : `${explorers}/${encodeURIComponent(target.bootstrapExplorerId)}/authoring/v2/builder`,
  };
};

const isExpectedResponse = (response, target, read) => {
  const url = new URL(response.url());
  return url.origin === new URL(target.uiUrl).origin && url.pathname === read.path &&
    response.request().method() === read.method;
};

export const builderLoadWorkflow = async ({ page, report, action, fault, testInfo }, context, caseName) => {
  const target = context.target;
  const read = ownedRead(target, caseName);
  const loadedBuilder = async () => {
    const handle = await page.waitForFunction(expectedExplorerId => {
      const visible = element => Boolean(element && element.getClientRects().length &&
        getComputedStyle(element).visibility !== 'hidden' && getComputedStyle(element).display !== 'none');
      const explorer = document.querySelector('select[aria-label="Explorer"]');
      const emptyHeading = [...document.querySelectorAll('h1,h2,h3')]
        .find(element => element.textContent.trim() === 'Build your first table');
      const tables = [...document.querySelectorAll('button[data-testid^="construction-table-"]')]
        .filter(visible);
      const previewStatus = document.querySelector('[data-testid="construction-preview"]')?.getAttribute('data-preview-status') ?? null;
      const snapshot = {
        selectedExplorerId: explorer?.value ?? null,
        emptyWorkspaceVisible: visible(emptyHeading),
        tableCount: tables.length,
        previewStatus,
      };
      return snapshot.selectedExplorerId === expectedExplorerId &&
        (snapshot.emptyWorkspaceVisible || (snapshot.tableCount > 0 && snapshot.previewStatus === 'ready'))
        ? snapshot
        : false;
    }, target.bootstrapExplorerId, { timeout: 5000 });
    const snapshot = await handle.jsonValue();
    if (!isLoadedBuilderSnapshot(snapshot, target.bootstrapExplorerId)) {
      throw new Error(`Builder did not load the selected Explorer workspace: ${JSON.stringify(snapshot)}`);
    }
    report.target.loadedBuilder = snapshot;
    return snapshot;
  };
  const injection = await fault(read);
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded',
  });
  const errorAlert = page.getByRole('alert').filter({ hasText: 'Couldn’t load this dataset.' });
  await errorAlert.waitFor({ state: 'visible', timeout: 5000 });
  recordCheck(report, 'correctness', 'builder failure is exposed in an alert', true,
    { alert: await errorAlert.innerText(), failedRead: read, project: target.fixtureProject, explorer: target.bootstrapExplorerId });
  recordCheck(report, 'correctness', 'specific builder read fault was injected', injection.count() === 1,
    { ...read, browserRequestOrigin: new URL(target.uiUrl).origin, project: target.fixtureProject, explorer: target.bootstrapExplorerId, count: injection.count() });

  const retry = page.getByRole('button', { name: retryLabel });
  let retryExpectationsPassed = true;
  try {
    await expect(retry).toHaveCount(1, { timeout: 5000 });
    await expect(retry).toBeVisible({ timeout: 5000 });
    await expect(retry).toBeEnabled({ timeout: 5000 });
  } catch {
    retryExpectationsPassed = false;
  }
  const count = await retry.count();
  const retryState = {
    count,
    visible: count === 1 ? await retry.isVisible() : false,
    enabled: count === 1 ? await retry.isEnabled() : false,
    locator: retry.toString(),
    expectationsPassed: retryExpectationsPassed,
  };
  const actionable = retryExpectationsPassed && retryState.count === 1 && retryState.visible && retryState.enabled;
  recordCheck(report, 'usability', 'builder failure exposes an actionable in-app retry', actionable,
    retryState);
  if (actionable) {
    const recoveredResponse = page.waitForResponse(response => {
      const request = response.request();
      const url = new URL(response.url());
      return request.method() === read.method && url.origin === new URL(target.uiUrl).origin && url.pathname === read.path;
    }, { timeout: 5000 });
    await action('builder in-app Retry', retry, () => retry.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await Promise.all([
          recoveredResponse,
          loadedBuilder(),
          errorAlert.waitFor({ state: 'hidden', timeout: 5000 }),
        ]);
      },
    });
    const response = await recoveredResponse;
    recordCheck(report, 'persistence', 'builder recovered through in-app Retry', response.status() >= 200 && response.status() < 300,
      { method: response.request().method(), browserRequestOrigin: new URL(response.url()).origin, path: new URL(response.url()).pathname, status: response.status(), project: target.fixtureProject, explorer: target.bootstrapExplorerId });
    return;
  }

  const unavailableRetry = new Error('Builder error did not expose a unique actionable Retry button');
  const retryUnavailableEvidence = {
    phase: 'retry-unavailable',
    message: unavailableRetry.message,
    failedRead: read,
    retry: retryState,
  };
  report.target.retryUnavailableEvidence = retryUnavailableEvidence;
  await testInfo.attach('builder-retry-unavailable.json', {
    body: JSON.stringify(retryUnavailableEvidence, null, 2),
    contentType: 'application/json',
  });

  const response = page.waitForResponse(candidate => {
    return isExpectedResponse(candidate, target, read);
  }, { timeout: 5000 });
  await page.reload({ waitUntil: 'domcontentloaded' });
  const recovery = await response;
  await loadedBuilder();
  await errorAlert.waitFor({ state: 'hidden', timeout: 5000 });
  const reloadRecovery = {
    method: recovery.request().method(),
    browserRequestOrigin: new URL(recovery.url()).origin,
    path: new URL(recovery.url()).pathname,
    status: recovery.status(),
    project: target.fixtureProject,
    explorer: target.bootstrapExplorerId,
    loadedBuilder: report.target.loadedBuilder,
  };
  recordCheck(report, 'persistence', 'reload separately restored the one-shot failed read',
    recovery.status() >= 200 && recovery.status() < 300, reloadRecovery);

  const retryUnavailableRecoveryEvidence = { ...retryUnavailableEvidence, reloadRecovery };
  report.target.retryUnavailableRecoveryEvidence = retryUnavailableRecoveryEvidence;
  await testInfo.attach('builder-retry-unavailable-recovery.json', {
    body: JSON.stringify(retryUnavailableRecoveryEvidence, null, 2),
    contentType: 'application/json',
  });
  throw unavailableRetry;
};
