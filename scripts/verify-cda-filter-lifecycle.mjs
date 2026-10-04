import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { startVerificationIdentity } from './lib/cda-verification-identity.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { inspectDOM, waitForDOM, navigatePage, selectControl, fillControl } from './lib/playwright-verification.mjs';
import { clickControl } from './lib/playwright-verification.mjs';
import { launchBrowser } from './lib/playwright-browser.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, arangoContainer, composeProject, sourceRoot });
const verificationIdentity = await startVerificationIdentity(sourceRoot, apiContainer);
const explorerId = `filter-lifecycle-${Date.now()}`;
const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const authoringURL = `${apiOrigin}${root}/${encodeURIComponent(explorerId)}/authoring/v2`;
const pageURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const evidenceDirectory = process.env.LOOM_VERIFY_OUTPUT ?? join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const tableName = `CDA filter QA ${Date.now()}`;
const state = { pageURL, explorerId, project, tableName, clicks: [], timingsMs: {}, responses: [], nativeRequests: [], errors: [] };
state.sourceFingerprint = verificationIdentity.sourceFingerprint;
state.apiBuildIdentity = verificationIdentity.apiBuildIdentity;
const tracker = { activeAction: undefined, actions: [] };
let browser;
let requestCapture;
let outputId;
let created = false;
let page;

const builder = async () => {
  const response = await fetch(`${authoringURL}/builder`, { signal: AbortSignal.timeout(30000) });
  assert.equal(response.status, 200);
  return response.json();
};
const apiCreate = async () => {
  const response = await fetch(`${apiOrigin}${root}`, { method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ name: explorerId, title: 'CDA filter lifecycle verification' }), signal: AbortSignal.timeout(30000) });
  const body = await response.json();
  state.responses.push({ path: root, status: response.status });
  assert(response.ok, JSON.stringify(body));
  return body;
};
const rawPatients = (ids, generation) => {
  const query = `FOR d IN Patient FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(generation)} AND d.id IN ${JSON.stringify(ids)} RETURN d.id`;
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`;
  const output = execFileSync('docker', ['exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', script], { encoding: 'utf8', timeout: 30000, maxBuffer: 200000 });
  const start = output.indexOf('[');
  assert(start >= 0, `Arango returned no JSON array: ${output.slice(-1000)}`);
  return JSON.parse(output.slice(start));
};
const selectTable = async () => {
  await waitForDOM(page, ({ name }) => [...document.querySelectorAll('[data-testid^="construction-table-"]')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
  await clickControl(tracker, page, '[data-testid^="construction-table-"]', { includes: tableName });
};
const preview = async (stage, expectedID) => {
  const started = Date.now();
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')) && !document.body.innerText.includes('Loading the preview…'), {}, 5000);
  const result = await inspectDOM(page, () => {
    const scroll = document.querySelector('[data-testid="preview-table-scroll"]');
    return { rowCount: scroll.querySelector('[role="table"]')?.getAttribute('aria-rowcount'), headers: [...scroll.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()), rows: [...scroll.querySelectorAll('[role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length) };
  });
  state.timingsMs[stage] = Date.now() - started;
  assert(state.timingsMs[stage] < 5000, `${stage} preview took ${state.timingsMs[stage]} ms`);
  if (expectedID) {
    assert.equal(result.rowCount, '2', `${stage} must have exactly one data row`);
    assert.deepEqual(result.rows, [[expectedID]], `${stage} does not show the selected CDA Patient`);
  }
  state[stage] = result;
  return result;
};
const filterEditor = async () => {
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]:not(:disabled)')), {}, 5000);
  const choices = await inspectDOM(page, () => {
    const panel = document.querySelector('[data-testid="construction-filter-editor"]');
    return { columns: [...panel.querySelector('select[aria-label="Column"]').options].map(option => ({ label: option.textContent.trim(), value: option.value, disabled: option.disabled })), conditions: [...panel.querySelector('select[aria-label="Condition"]').options].map(option => ({ label: option.textContent.trim(), value: option.value, disabled: option.disabled })) };
  });
  const id = choices.columns.find(choice => choice.label.startsWith('Patient ID'));
  const equals = choices.conditions.find(choice => choice.value === 'EQUALS');
  assert(id && !id.disabled, 'Patient ID cannot be filtered');
  assert(equals && !equals.disabled, 'Patient ID equality is unavailable');
  return { choices, id };
};
const setEquals = async (id, value, label) => {
  await selectControl(tracker, page, '[data-testid="construction-filter-editor"] select[aria-label="Column"]', id.value);
  state.clicks.push('Choose Patient ID column');
  await selectControl(tracker, page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
  state.clicks.push('Choose equals condition');
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]:not(:disabled)')), {}, 5000);
  const started = Date.now();
  const requestStart = state.nativeRequests.length;
  await fillControl(tracker, page, '[data-testid="construction-filter-editor"] input[aria-label="Value"]', value);
  state.clicks.push(label);
  const [request] = await Promise.all([
    requestCapture.waitFor(entry => entry.method === 'POST' && entry.path.endsWith('/construction-proposals'), { fromIndex: requestStart, timeoutMs: 5000 }),
    waitForDOM(page, ({ value: expectedValue }) => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready' && document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.includes(expectedValue), { value }, 5000),
  ]);
  const proposed = await inspectDOM(page, () => { const panel = document.querySelector('[data-testid="construction-proposal-preview"]'); return { rows: [...panel.querySelectorAll('tbody tr')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())), applyDisabled: document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled }; });
  assert.equal(request.status, 200, JSON.stringify(request));
  assert(request.response?.proposalId, `${label} proposal must include a fresh receipt`);
  assert.equal(request.response.outputId, outputId);
  assert.deepEqual(proposed.rows, [[value]]);
  assert.equal(proposed.applyDisabled, false);
  const elapsedMs = Date.now() - started;
  state.timingsMs[label] = elapsedMs;
  assert(elapsedMs < 5000, `${label} preview took ${elapsedMs} ms`);
  return { proposed, elapsedMs, request };
};

try {
  await mkdir(evidenceDirectory, { recursive: true });
  await apiCreate();
  browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [apiOrigin, uiOrigin], noAuth: true });
  page = browser.page;
  requestCapture = captureCDARequests(page, { apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: root, report: state, responsePaths: /commands|builder|construction-proposals|preview|explorers/ });
  await navigatePage(page, pageURL);
  await waitForDOM(page, () => document.body.innerText.includes('DATASET WORKSPACE'), {}, 5000);
  await clickControl(tracker, page, 'button', { name: 'New table' });
  state.clicks.push('New table');
  await waitForDOM(page, () => Boolean(document.querySelector('button[aria-label="Choose Patient rows"]:not(:disabled)')), {}, 5000);
  await fillControl(tracker, page, '#first-table-name', tableName);
  await clickControl(tracker, page, 'button[aria-label="Choose Patient rows"]');
  state.clicks.push('Choose Patient rows');
  created = true;
  const createdBuilder = await builder();
  outputId = createdBuilder.workspace.documents.find(document => document.output.title === tableName)?.output.id;
  assert(outputId, 'Temporary Patient table was not created');
  const generation = createdBuilder.catalog.generation;
  assert(generation, 'CDA catalog must expose the active generation');
  const baseline = await preview('baseline');
  assert.equal(baseline.headers[0], 'PATIENT ID');
  assert(baseline.rows.length >= 2, 'Baseline preview did not show two Patient IDs');
  const ids = baseline.rows.slice(0, 2).map(row => row[0]);
  assert.deepEqual(new Set(rawPatients(ids, generation)), new Set(ids), 'Preview IDs are absent from independent project/generation scoped CDA Patient records');
  state.sourceIDs = ids;
  state.generation = generation;

  await clickControl(tracker, page, 'button[aria-label^="Filter rows:"]');
  state.clicks.push('Open Filter rows');
  const { choices, id } = await filterEditor();
  state.filterChoices = choices;
  state.firstProposal = await setEquals(id, ids[0], 'Enter Patient ID value');
  await clickControl(tracker, page, '[data-testid="construction-apply-proposal"]');
  state.clicks.push('Apply Patient ID filter');
  await waitForDOM(page, () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 5000);
  const firstSaved = await builder();
  assert.deepEqual(firstSaved.workspace.documents.find(document => document.output.id === outputId)?.construction, state.firstProposal.request.response.candidateConstruction, 'Applied filter must persist the proposed construction');
  await navigatePage(page, pageURL);
  await selectTable();
  await preview('saved', ids[0]);

  await clickControl(tracker, page, '[data-testid^="construction-history-step-"]');
  state.clicks.push('Select saved Filter rows step');
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid^="construction-edit-step-"]')), {}, 5000);
  await clickControl(tracker, page, '[data-testid^="construction-edit-step-"]');
  state.clicks.push('Edit Filter rows step');
  await filterEditor();
  const original = await inspectDOM(page, () => document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]')?.value);
  assert.equal(original, ids[0], 'Saved filter did not reopen with its exact value');
  state.editedProposal = await setEquals(id, ids[1], 'Change Patient ID value');
  await clickControl(tracker, page, '[data-testid="construction-apply-proposal"]');
  state.clicks.push('Apply edited Filter rows step');
  await waitForDOM(page, () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 5000);
  const editedSaved = await builder();
  assert.deepEqual(editedSaved.workspace.documents.find(document => document.output.id === outputId)?.construction, state.editedProposal.request.response.candidateConstruction, 'Edited filter must persist its proposed construction');
  await navigatePage(page, pageURL);
  await selectTable();
  await preview('edited', ids[1]);

  await clickControl(tracker, page, '[data-testid^="construction-history-step-"]');
  state.clicks.push('Select edited Filter rows step');
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid^="construction-remove-step-"]')), {}, 5000);
  const removeStart = state.nativeRequests.length;
  await clickControl(tracker, page, '[data-testid^="construction-remove-step-"]');
  state.clicks.push('Remove Filter rows step');
  await requestCapture.waitFor(entry => entry.method === 'POST' && entry.path.endsWith('/construction-proposals'), { fromIndex: removeStart, timeoutMs: 5000 });
  await waitForDOM(page, () => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', {}, 5000);
  await clickControl(tracker, page, '[data-testid="construction-apply-proposal"]');
  state.clicks.push('Apply step removal');
  await waitForDOM(page, () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0, {}, 5000);
  await navigatePage(page, pageURL);
  await selectTable();
  const restored = await preview('restored');
  assert(restored.rows.length >= 2, 'Removing the filter did not restore multiple Patient rows');
  assert.deepEqual(new Set(rawPatients(restored.rows.map(row => row[0]), generation)), new Set(restored.rows.map(row => row[0])));
  await requestCapture.flush();
  assert(state.nativeRequests.every(response => response.status < 400), 'Browser received an authoring API error');
  assert.deepEqual(state.errors, [], 'Unexpected owned API/browser failures occurred');
  assert.deepEqual(browser.diagnostics.console, [], 'Unexpected browser console errors');
  assert.deepEqual(browser.diagnostics.pageErrors, [], 'Unexpected browser page errors');
  assert.deepEqual(browser.diagnostics.networkFailures, [], 'Unexpected browser network failures');
  assert.deepEqual(browser.diagnostics.httpFailures, [], 'Unexpected browser HTTP failures');
  state.outcome = 'passed';
} catch (error) {
  state.outcome = 'failed';
  state.error = error instanceof Error ? error.message : String(error);
  state.failureUI = browser ? await inspectDOM(browser.page, () => ({ text: document.body.innerText.slice(0, 3500), alerts: [...document.querySelectorAll('[role="alert"]')].map(node => node.innerText) })).catch(() => undefined) : undefined;
  if (browser) await browser.captureFailure(error, { phase: 'filter-lifecycle', action: tracker.activeAction, elapsedMs: tracker.activeAction ? Date.now() - tracker.activeAction.startedAt : undefined, state: { tableName, outputId, ids: state.sourceIDs, responses: state.nativeRequests } });
  process.exitCode = 1;
} finally {
  if (browser && outputId) {
    try {
      await navigatePage(page, pageURL);
      await waitForDOM(page, ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 10000);
      await clickControl(tracker, page, '[data-testid^="construction-table-"]', { includes: tableName });
      await waitForDOM(page, () => document.querySelector('[data-testid="construction-delete-table"]')?.disabled === false, {}, 10000);
      browser.page.once('dialog', dialog => dialog.accept());
      await clickControl(tracker, page, '[data-testid="construction-delete-table"]');
      await waitForDOM(page, ({ name }) => ![...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 10000);
      state.cleanup = 'temporary table deleted';
    } catch (error) {
      state.cleanup = error instanceof Error ? error.message : String(error);
      await browser.captureFailure(error, { phase: 'filter-cleanup', action: tracker.activeAction, elapsedMs: tracker.activeAction ? Date.now() - tracker.activeAction.startedAt : undefined, state: { tableName, outputId } });
      process.exitCode = 1;
    }
  }
  if (requestCapture) await requestCapture.flush();
  if (browser) {
    state.responses.push(...state.nativeRequests.map(response => ({ path: response.path, status: response.status })));
    state.diagnostics = browser.diagnostics;
    await browser.close();
  }
  try { state.verificationIdentity = await verificationIdentity.finish(); }
  catch (error) { state.identityError = String(error); state.outcome = 'invalidated'; process.exitCode = 1; }
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'filter-lifecycle.json'), JSON.stringify(state, null, 2));
  console.log(JSON.stringify({ evidenceDirectory, clicks: state.clicks.length, timingsMs: state.timingsMs, error: state.error, cleanup: state.cleanup, outcome: state.outcome }, null, 2));
}
