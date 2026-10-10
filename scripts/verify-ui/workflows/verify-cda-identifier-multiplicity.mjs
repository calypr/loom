import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';

export async function identifierMultiplicityWorkflow({ page, cda, expect }) {
const project = cda.project;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const seedExplorerId = process.env.LOOM_CDA_EXPLORER_SEED ?? cda.explorer ?? 'cda-builder-full-qa-1790440983382';
const explorerId = `identifier-multiplicity-${Date.now()}`;
const observationId = 'CGCI-BLGSP.BLGSP-71-06-00169.BLGSP-71-06-00169_diagnosis';
const evidenceDirectory = cda.evidence;
const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const base = `${root}/${encodeURIComponent(explorerId)}`;
const url = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const report = Object.assign(cda.report, { url, explorerId, seedExplorerId, clicks: [], responses: [], browserErrors: [], previews: [], cleanup: false });
let requestCapture;
let duplicateId;
const action = async (label, locator) => {
  await expect(locator, `${label}: expected one native control`).toHaveCount(1, { timeout: 5000 });
  const startedAt = Date.now();
  await cda.action(label, locator, () => locator.click({ timeout: 5000 }));
  const elapsedMs = Date.now() - startedAt;
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
const tableIds = () => cda.inspect(() => [...document.querySelectorAll('[data-testid^="construction-table-"]')].map(button => button.dataset.testid.slice('construction-table-'.length)));
const selectDuplicate = async () => {
  await cda.wait(({ id }) => Boolean(document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)), { id: duplicateId }, 5000);
  await action('Select temporary Specimen copy', page.locator(`[data-testid="construction-table-${duplicateId}"]`));
  await cda.wait(({ id }) => document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)?.getAttribute('aria-pressed') === 'true', { id: duplicateId }, 5000);
};
const visibleColumns = () => cda.inspect(() => [...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')]
  .filter(button => button.offsetParent !== null).map(button => button.innerText.trim()));

try {
  const clone = await api(root, { name: explorerId, title: 'CDA identifier multiplicity verification', sourceExplorerId: seedExplorerId });
  assert.equal(clone.name, explorerId);
  requestCapture = cda.captureRequests(root, { responsePaths: /commands|builder|preview|column|explorer/ });
  await cda.navigate(url);
  await cda.wait(() => document.body.innerText.includes('DATASET WORKSPACE'), {}, 5000);
  const sourceId = await cda.inspect(() => [...document.querySelectorAll('[data-testid^="construction-table-"]')]
    .find(button => button.innerText.trim().endsWith('Specimen'))?.dataset.testid);
  assert(sourceId, 'Source Specimen table is missing');
  await action('Select source Specimen table', page.locator(`[data-testid="${sourceId}"]`));
  const originalIds = await tableIds();
  await action('Duplicate Specimen table', page.locator('[data-testid="construction-duplicate-table"]'));
  await cda.wait(({ count }) => document.querySelectorAll('[data-testid^="construction-table-"]').length > count, { count: originalIds.length }, 5000);
  duplicateId = (await tableIds()).find(id => !originalIds.includes(id));
  assert(duplicateId, 'Duplicate output ID was not found');
  await cda.navigate(url);
  await selectDuplicate();
  report.initialColumns = await visibleColumns();
  assert(report.initialColumns.length >= 1, 'Duplicate has no selectable source columns');

  await action('Open Add columns', page.locator('[data-testid="construction-action-add-columns"]'));
  const diagnosisArticle = page.locator('[aria-label="Add columns editor"] article').filter({ hasText: 'https://cda.readthedocs.io/diagnosis' });
  await cda.wait(({ selector }) => Boolean(document.querySelector(selector)), { selector: '[aria-label="Add columns editor"]' }, 5000);
  await expect(diagnosisArticle, 'Diagnosis identifier feature card').toHaveCount(1, { timeout: 5000 });
  const identifierCheckbox = diagnosisArticle.getByRole('checkbox', { name: 'Select identifier[]', exact: true });
  await action('Select Condition diagnosis identifier', identifierCheckbox);
  await cda.wait(() => [...document.querySelectorAll('[aria-label="Add columns editor"] button')].some(button => button.innerText.trim() === 'Add 1 selected feature' && !button.disabled), {}, 5000);
  await action('Choose selected feature', page.getByRole('button', { name: 'Add 1 selected feature', exact: true }));
  const multiplicityDialog = page.getByRole('dialog');
  await cda.wait(() => [...document.querySelectorAll('[role="dialog"] input[type="radio"]')].some(input => input.getAttribute('aria-label')?.includes('via Subject then Subject')), {}, 5000);
  await action('Choose Specimen to Patient to Condition', multiplicityDialog.getByRole('radio', { name: /via Subject then Subject/ }));
  await cda.wait(() => [...document.querySelectorAll('[role="dialog"] input[type="radio"]')].some(input => input.getAttribute('aria-label')?.includes('Keep all matching values')), {}, 5000);
  await action('Keep all matching values', multiplicityDialog.getByRole('radio', { name: /Keep all matching values/ }));
  await cda.wait(() => [...document.querySelectorAll('[role="dialog"] button')].some(button => button.innerText.trim() === 'Add 1 column' && !button.disabled), {}, 5000);
  const proposalStart = Date.now();
  await action('Preview new column', multiplicityDialog.getByRole('button', { name: 'Add 1 column', exact: true }));
  await cda.wait(() => [...document.querySelectorAll('h3')].some(heading => heading.innerText === 'Preview new columns' && heading.parentElement?.innerText.includes('Apply columns')), {}, 5000);
  report.proposalMs = Date.now() - proposalStart;
  report.proposal = await cda.inspect(() => [...document.querySelectorAll('h3')].find(heading => heading.innerText === 'Preview new columns')?.parentElement?.innerText);
  assert(report.proposal.includes('25 of 25 displayed rows contain a value'), 'Proposal did not render expected CDA coverage');
  assert(report.proposalMs < 5000, `Proposal took ${report.proposalMs} ms`);
  const applyStart = report.nativeRequests.length;
  await action('Apply columns', page.getByRole('button', { name: 'Apply columns', exact: true }));
  const applyRequest = await requestCapture.waitFor(entry => entry.method === 'POST' && entry.path.endsWith('/commands'), { fromIndex: applyStart, timeoutMs: 5000 });
  assert.equal(applyRequest.status, 200, JSON.stringify(applyRequest));
  report.afterApplyInspection = await cda.inspect(() => ({ selected: [...document.querySelectorAll('[data-testid^="construction-table-"]')].filter(button => button.getAttribute('aria-pressed') === 'true').map(button => button.dataset.testid), columns: [...document.querySelectorAll('[data-testid^="construction-column-"]')].map(button => ({ testid: button.dataset.testid, text: button.innerText.trim(), visible: button.offsetParent !== null })), alerts: [...document.querySelectorAll('[role="alert"]')].map(item => item.innerText) }));
  await cda.wait(({ count }) => [...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')].filter(button => button.offsetParent !== null).length === count, { count: report.initialColumns.length + 1 }, 5000);
  report.appliedColumns = await visibleColumns();
  assert(report.appliedColumns.some(column => column.includes('identifier[]')), 'Applied identifier column is not visible');
  await action('Preview applied table', page.getByRole('button', { name: 'Preview', exact: true }));
  await cda.wait(() => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')), {}, 5000);
  report.previews.push(await cda.inspect(() => { const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'); return { headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()), rows: [...table.querySelectorAll('[role="row"]')].slice(1, 4).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())) }; }));
  const rendered = report.previews.at(-1);
  const identifierIndex = rendered.headers.findIndex(header => header.toLowerCase().includes('identifier[]'));
  assert(identifierIndex >= 0, 'Rendered table has no Condition identifier header');
  assert(rendered.rows[0]?.[identifierIndex]?.includes(observationId), 'Rendered first identifier differs from the CDA Condition source');

  await cda.navigate(url);
  await selectDuplicate();
  assert((await visibleColumns()).some(column => column.includes('identifier[]')), 'Identifier column disappeared on reload');
  await action('Reopen Add columns after reload', page.locator('button:not(:disabled)[data-testid="construction-action-add-columns"]'));
  await action('Close Add columns', page.locator('[data-testid="construction-close-operation-editor"]'));
  const removalLabel = await cda.inspect(() => [...document.querySelectorAll('button[aria-label^="Remove "]')].find(button => button.getAttribute('aria-label')?.includes('identifier[]'))?.getAttribute('aria-label'));
  assert(removalLabel, 'Saved identifier column has no removal control');
  await action('Remove identifier column', page.getByRole('button', { name: removalLabel, exact: true }));
  await cda.wait(({ count }) => [...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')].filter(button => button.offsetParent !== null).length === count, { count: report.initialColumns.length }, 5000);
  await cda.navigate(url);
  await selectDuplicate();
  report.restoredColumns = await visibleColumns();
  assert.deepEqual(report.restoredColumns, report.initialColumns, 'Identifier column remained after removal and reload');
  await requestCapture.flush();
  report.responses.push(...report.nativeRequests.map(request => ({ path: request.path, status: request.status, elapsedMs: request.completedAt - request.startedAt })));
  assert(report.nativeRequests.every(response => response.status < 400), 'An authoring request failed');
  assert.deepEqual(report.errors, [], 'Unexpected owned API or browser request failures occurred');
  assert.deepEqual(cda.diagnostics.console, [], 'Browser console errors occurred');
  assert.deepEqual(cda.diagnostics.pageErrors, [], 'Browser exceptions occurred');
  assert.deepEqual(cda.diagnostics.networkFailures, [], 'Browser network requests failed');
  assert.deepEqual(cda.diagnostics.httpFailures, [], 'Browser requests returned HTTP errors');
  report.outcome = 'passed';
} catch (error) {
  report.outcome = 'failed';
  report.failure = String(error.stack ?? error);
  report.browserErrors.push(report.failure);
  throw error;
} finally {
  if (duplicateId) {
    try {
      await cda.navigate(url);
      await selectDuplicate();
      page.once('dialog', dialog => dialog.accept());
      await action('Delete verifier duplicate', page.locator('[data-testid="construction-delete-table"]'));
      await cda.wait(({ id }) => !document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`), { id: duplicateId }, 5000);
      report.cleanup = true;
    } catch (error) {
      report.cleanupError = String(error);
      throw error;

    }
  }
  if (requestCapture) await requestCapture.flush();
  cda.includeBrowserDiagnostics();
  report.diagnostics = cda.diagnostics;
  await cda.attachReport('identifier-multiplicity-domain-report.json');
}
return report;
}
