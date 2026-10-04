import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { startVerificationIdentity } from './lib/cda-verification-identity.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { inspectDOM, waitForDOM, navigatePage } from './lib/playwright-verification.mjs';
import { performAction, requireUnique } from './lib/playwright-actions.mjs';
import { launchBrowser } from './lib/playwright-browser.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot });
const verificationIdentity = await startVerificationIdentity(sourceRoot, apiContainer);
const seedExplorerId = process.env.LOOM_CDA_EXPLORER_SEED ?? 'cda-builder-full-qa-1790440983382';
const explorerId = `identifier-multiplicity-${Date.now()}`;
const observationId = 'CGCI-BLGSP.BLGSP-71-06-00169.BLGSP-71-06-00169_diagnosis';
const evidenceDirectory = process.env.LOOM_VERIFY_OUTPUT ?? join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const base = `${root}/${encodeURIComponent(explorerId)}`;
const url = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const report = { url, explorerId, seedExplorerId, clicks: [], responses: [], browserErrors: [], previews: [], cleanup: false, nativeRequests: [], errors: [] };
report.sourceFingerprint = verificationIdentity.sourceFingerprint;
report.apiBuildIdentity = verificationIdentity.apiBuildIdentity;
const tracker = { activeAction: undefined, actions: [] };
let browser;
let requestCapture;
let duplicateId;
const action = async (label, locator) => {
  await requireUnique(locator, label);
  const elapsedMs = await performAction(tracker, label, locator, (target, options) => target.click(options), { timeout: 5000 });
  report.clicks.push({ label, elapsedMs });
  return elapsedMs;
};
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `identifier-multiplicity-${Date.now()}-${report.responses.length + 1}` },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.responses.push({ path, status: response.status });
  assert(response.ok, `${path}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};
let page;
const tableIds = () => inspectDOM(page, () => [...document.querySelectorAll('[data-testid^="construction-table-"]')].map(button => button.dataset.testid.slice('construction-table-'.length)));
const selectDuplicate = async () => {
  await waitForDOM(page, ({ id }) => Boolean(document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)), { id: duplicateId }, 30000);
  await action('Select temporary Specimen copy', page.locator(`[data-testid="construction-table-${duplicateId}"]`));
  await waitForDOM(page, ({ id }) => document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)?.getAttribute('aria-pressed') === 'true', { id: duplicateId }, 30000);
};
const visibleColumns = () => inspectDOM(page, () => [...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')]
  .filter(button => button.offsetParent !== null).map(button => button.innerText.trim()));

try {
  await mkdir(evidenceDirectory, { recursive: true });
  const clone = await api(root, { name: explorerId, title: 'CDA identifier multiplicity verification', sourceExplorerId: seedExplorerId });
  assert.equal(clone.name, explorerId);
  browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [apiOrigin, uiOrigin], noAuth: true });
  page = browser.page;
  requestCapture = captureCDARequests(page, { apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: root, report, responsePaths: /commands|builder|preview|column|explorer/ });
  await navigatePage(page, url);
  await waitForDOM(page, () => document.body.innerText.includes('DATASET WORKSPACE'), {}, 30000);
  const sourceId = await inspectDOM(page, () => [...document.querySelectorAll('[data-testid^="construction-table-"]')]
    .find(button => button.innerText.trim().endsWith('Specimen'))?.dataset.testid);
  assert(sourceId, 'Source Specimen table is missing');
  await action('Select source Specimen table', page.locator(`[data-testid="${sourceId}"]`));
  const originalIds = await tableIds();
  await action('Duplicate Specimen table', page.locator('[data-testid="construction-duplicate-table"]'));
  await waitForDOM(page, ({ count }) => document.querySelectorAll('[data-testid^="construction-table-"]').length > count, { count: originalIds.length }, 30000);
  duplicateId = (await tableIds()).find(id => !originalIds.includes(id));
  assert(duplicateId, 'Duplicate output ID was not found');
  await navigatePage(page, url);
  await selectDuplicate();
  report.initialColumns = await visibleColumns();
  assert(report.initialColumns.length >= 1, 'Duplicate has no selectable source columns');

  await action('Open Add columns', page.locator('[data-testid="construction-action-add-columns"]'));
  const diagnosisArticle = page.locator('[aria-label="Add columns editor"] article').filter({ hasText: 'https://cda.readthedocs.io/diagnosis' });
  await waitForDOM(page, ({ selector }) => Boolean(document.querySelector(selector)), { selector: '[aria-label="Add columns editor"]' }, 30000);
  await requireUnique(diagnosisArticle, 'Diagnosis identifier feature card');
  const identifierCheckbox = diagnosisArticle.getByRole('checkbox', { name: 'Select identifier[]', exact: true });
  await action('Select Condition diagnosis identifier', identifierCheckbox);
  await waitForDOM(page, () => [...document.querySelectorAll('[aria-label="Add columns editor"] button')].some(button => button.innerText.trim() === 'Add 1 selected feature' && !button.disabled), {}, 10000);
  await action('Choose selected feature', page.getByRole('button', { name: 'Add 1 selected feature', exact: true }));
  const multiplicityDialog = page.getByRole('dialog');
  await waitForDOM(page, () => [...document.querySelectorAll('[role="dialog"] input[type="radio"]')].some(input => input.getAttribute('aria-label')?.includes('via Subject then Subject')), {}, 30000);
  await action('Choose Specimen to Patient to Condition', multiplicityDialog.getByRole('radio', { name: /via Subject then Subject/ }));
  await waitForDOM(page, () => [...document.querySelectorAll('[role="dialog"] input[type="radio"]')].some(input => input.getAttribute('aria-label')?.includes('Keep all matching values')), {}, 30000);
  await action('Keep all matching values', multiplicityDialog.getByRole('radio', { name: /Keep all matching values/ }));
  await waitForDOM(page, () => [...document.querySelectorAll('[role="dialog"] button')].some(button => button.innerText.trim() === 'Add 1 column' && !button.disabled), {}, 10000);
  const proposalStart = Date.now();
  await action('Preview new column', multiplicityDialog.getByRole('button', { name: 'Add 1 column', exact: true }));
  await waitForDOM(page, () => [...document.querySelectorAll('h3')].some(heading => heading.innerText === 'Preview new columns' && heading.parentElement?.innerText.includes('Apply columns')), {}, 30000);
  report.proposalMs = Date.now() - proposalStart;
  report.proposal = await inspectDOM(page, () => [...document.querySelectorAll('h3')].find(heading => heading.innerText === 'Preview new columns')?.parentElement?.innerText);
  assert(report.proposal.includes('25 of 25 displayed rows contain a value'), 'Proposal did not render expected CDA coverage');
  assert(report.proposalMs < 5000, `Proposal took ${report.proposalMs} ms`);
  const applyStart = report.nativeRequests.length;
  await action('Apply columns', page.getByRole('button', { name: 'Apply columns', exact: true }));
  const applyRequest = await requestCapture.waitFor(entry => entry.method === 'POST' && entry.path.endsWith('/commands'), { fromIndex: applyStart, timeoutMs: 5000 });
  assert.equal(applyRequest.status, 200, JSON.stringify(applyRequest));
  report.afterApplyInspection = await inspectDOM(page, () => ({ selected: [...document.querySelectorAll('[data-testid^="construction-table-"]')].filter(button => button.getAttribute('aria-pressed') === 'true').map(button => button.dataset.testid), columns: [...document.querySelectorAll('[data-testid^="construction-column-"]')].map(button => ({ testid: button.dataset.testid, text: button.innerText.trim(), visible: button.offsetParent !== null })), alerts: [...document.querySelectorAll('[role="alert"]')].map(item => item.innerText) }));
  await waitForDOM(page, ({ count }) => [...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')].filter(button => button.offsetParent !== null).length === count, { count: report.initialColumns.length + 1 }, 30000);
  report.appliedColumns = await visibleColumns();
  assert(report.appliedColumns.some(column => column.includes('identifier[]')), 'Applied identifier column is not visible');
  await action('Preview applied table', page.getByRole('button', { name: 'Preview', exact: true }));
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')), {}, 30000);
  report.previews.push(await inspectDOM(page, () => { const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'); return { headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()), rows: [...table.querySelectorAll('[role="row"]')].slice(1, 4).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())) }; }));
  const rendered = report.previews.at(-1);
  const identifierIndex = rendered.headers.findIndex(header => header.toLowerCase().includes('identifier[]'));
  assert(identifierIndex >= 0, 'Rendered table has no Condition identifier header');
  assert(rendered.rows[0]?.[identifierIndex]?.includes(observationId), 'Rendered first identifier differs from the CDA Condition source');

  await navigatePage(page, url);
  await selectDuplicate();
  assert((await visibleColumns()).some(column => column.includes('identifier[]')), 'Identifier column disappeared on reload');
  await action('Reopen Add columns after reload', page.locator('button:not(:disabled)[data-testid="construction-action-add-columns"]'));
  await action('Close Add columns', page.locator('[data-testid="construction-close-operation-editor"]'));
  const removalLabel = await inspectDOM(page, () => [...document.querySelectorAll('button[aria-label^="Remove "]')].find(button => button.getAttribute('aria-label')?.includes('identifier[]'))?.getAttribute('aria-label'));
  assert(removalLabel, 'Saved identifier column has no removal control');
  await action('Remove identifier column', page.getByRole('button', { name: removalLabel, exact: true }));
  await waitForDOM(page, ({ count }) => [...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')].filter(button => button.offsetParent !== null).length === count, { count: report.initialColumns.length }, 30000);
  await navigatePage(page, url);
  await selectDuplicate();
  report.restoredColumns = await visibleColumns();
  assert.deepEqual(report.restoredColumns, report.initialColumns, 'Identifier column remained after removal and reload');
  await requestCapture.flush();
  report.responses.push(...report.nativeRequests.map(request => ({ path: request.path, status: request.status, elapsedMs: request.completedAt - request.startedAt })));
  assert(report.nativeRequests.every(response => response.status < 400), 'An authoring request failed');
  assert.deepEqual(report.errors, [], 'Unexpected owned API or browser request failures occurred');
  assert.deepEqual(browser.diagnostics.console, [], 'Browser console errors occurred');
  assert.deepEqual(browser.diagnostics.pageErrors, [], 'Browser exceptions occurred');
  assert.deepEqual(browser.diagnostics.networkFailures, [], 'Browser network requests failed');
  assert.deepEqual(browser.diagnostics.httpFailures, [], 'Browser requests returned HTTP errors');
  report.outcome = 'passed';
} catch (error) {
  report.outcome = 'failed';
  report.failure = String(error.stack ?? error);
  report.browserErrors.push(report.failure);
  if (browser) await browser.captureFailure(error, { phase: 'identifier-multiplicity-lifecycle', action: tracker.activeAction, elapsedMs: tracker.activeAction ? Date.now() - tracker.activeAction.startedAt : undefined, state: { explorerId, duplicateId, requests: report.nativeRequests } });
  process.exitCode = 1;
} finally {
  if (browser && duplicateId) {
    try {
      await navigatePage(page, url);
      await selectDuplicate();
      browser.page.once('dialog', dialog => dialog.accept());
      await action('Delete verifier duplicate', page.locator('[data-testid="construction-delete-table"]'));
      await waitForDOM(page, ({ id }) => !document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`), { id: duplicateId }, 30000);
      report.cleanup = true;
    } catch (error) {
      report.cleanupError = String(error);
      await browser.captureFailure(error, { phase: 'identifier-multiplicity-cleanup', action: tracker.activeAction,
        elapsedMs: tracker.activeAction ? Date.now() - tracker.activeAction.startedAt : undefined, state: { explorerId, duplicateId } });
      process.exitCode = 1;
    }
  }
  if (requestCapture) await requestCapture.flush();
  if (browser) {
    report.diagnostics = browser.diagnostics;
    await browser.close();
  }
  try { report.verificationIdentity = await verificationIdentity.finish(); }
  catch (error) { report.identityError = String(error); report.outcome = 'invalidated'; process.exitCode = 1; }
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'identifier-multiplicity-lifecycle.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ evidenceDirectory, clicks: report.clicks.length, proposalMs: report.proposalMs, cleanup: report.cleanup, outcome: report.outcome, errors: report.browserErrors, cleanupError: report.cleanupError }, null, 2));
}
