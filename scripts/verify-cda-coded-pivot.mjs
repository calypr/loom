import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
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
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, arangoContainer, composeProject, sourceRoot });
const verificationIdentity = await startVerificationIdentity(sourceRoot, apiContainer);
const observationId = '485e2567-b566-56f3-b5bd-5f025f37cd95';
const mode = process.argv[2] === 'string' ? 'string' : 'integer';
const expected = mode === 'string'
  ? [{ label: 'Specimen type', code: 'specimen_type', value: 'analyte' }, { label: 'Primary disease type', code: 'primary_disease_type', value: 'Ductal and lobular neoplasms' }]
  : [{ label: 'Days to collection', code: 'days_to_collection', value: '162' }];
const explorerId = `coded-pivot-${mode}-${Date.now()}`;
const tableName = `Coded pivot ${mode} QA ${Date.now()}`;
const evidence = process.env.LOOM_VERIFY_OUTPUT ?? `/tmp/loom-coded-pivot-${Date.now()}`;
const artifact = join(evidence, `bounded-coded-pivot-${mode}.json`);
const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const base = `${root}/${encodeURIComponent(explorerId)}`;
const report = { explorerId, tableName, observationId, project, mode, expected, clicks: 0, timingsMs: {}, requests: [], nativeRequests: [], errors: [], failures: [] };
let browser;
let requestCapture;
let created = false;
let selectedURL;
const tracker = { activeAction: undefined, actions: [] };
const action = async (label, locator) => {
  report.clicks++;
  await requireUnique(locator, label);
  return performAction(tracker, label, locator, (target, options) => target.click(options), { timeout: 5000 });
};
const fill = async (label, locator, value) => {
  await requireUnique(locator, label);
  return performAction(tracker, label, locator, (target, options) => target.fill(value, options), { timeout: 5000, editable: true });
};
const select = async (label, locator, value) => {
  await requireUnique(locator, label);
  return performAction(tracker, label, locator, (target, options) => target.selectOption(value, options), { timeout: 5000 });
};
const timing = (name, started) => {
  report.timingsMs[name] = Date.now() - started;
  assert(report.timingsMs[name] < 5000, `${name} took ${report.timingsMs[name]} ms`);
};
const api = async (path, body) => {
  const requestId = `coded-pivot-${Date.now()}-${report.requests.length + 1}`;
  const startedAt = Date.now();
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, method: body === undefined ? 'GET' : 'POST', requestId, status: response.status, elapsedMs: Date.now() - startedAt, request: body, response: value });
  assert(response.ok, `${path}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};
const rawObservation = () => {
  const query = `FOR d IN Observation FILTER d.id == ${JSON.stringify(observationId)} AND d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(report.generation)} LIMIT 2 RETURN {id:d.id,project:d.project,generation:d.dataset_generation,resourceType:d.payload.resourceType,status:d.payload.status,component:d.payload.component,code:d.payload.code,valueQuantity:d.payload.valueQuantity}`;
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('docker', ['exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', script], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Arango returned no JSON array: ${result.stdout.slice(-1000)}`);
  const matches = JSON.parse(result.stdout.slice(start));
  assert.equal(matches.length, 1, 'The project/generation/ID source oracle must resolve exactly one Observation');
  const source = matches[0];
  assert.equal(source.id, observationId);
  assert.equal(source.project, project);
  assert.equal(source.generation, report.generation);
  assert.equal(source.resourceType, 'Observation');
  const values = expected.map(pair => {
    const component = (source.component ?? []).filter(item => (item.code?.coding ?? []).some(coding => coding.system === 'https://cda.readthedocs.io' && coding.code === pair.code));
    assert.equal(component.length, 1);
    return mode === 'integer' ? String(component[0].valueQuantity?.value) : component[0].valueString;
  });
  assert.deepEqual(values, expected.map(pair => pair.value), 'Independent raw FHIR Coding/value oracle differs from expected fixture');
  report.oracle = { source: 'Arango Observation payload scoped by exact project, generation and ID; paired by Coding.system and Coding.code', id: source.id, project: source.project, generation: source.generation, status: source.status, values };
};

await mkdir(evidence, { recursive: true });
try {
  const createdExplorer = await api(root, { name: explorerId, title: `CDA coded pivot ${mode} verification` });
  assert.equal(createdExplorer.name, explorerId);
  const builder = await api(`${base}/authoring/v2/builder`);
  report.generation = builder.catalog.generation;
  assert(report.generation, 'The isolated project must have a loaded dataset generation');
  rawObservation();
  const selection = await api(`${base}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: explorerId,
    source: { kind: 'resources', resources: { refs: [{ project, generation: report.generation, resourceType: 'Observation', id: observationId }] } },
  });
  assert(selection.id);
  const baseURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
  selectedURL = `${baseURL}&selection=${encodeURIComponent(selection.id)}`;
  browser = await launchBrowser({ evidence, appOrigins: [apiOrigin, uiOrigin], noAuth: true });
  const page = browser.page;
  requestCapture = captureCDARequests(page, { apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: root, report, responsePaths: /construction-proposals|commands|builder|selections|preview/ });
  await navigatePage(page, baseURL);
  await waitForDOM(page, () => document.body.innerText.includes('DATASET WORKSPACE'), {}, 30000);
  assert.equal(await inspectDOM(page, () => [...document.querySelectorAll('button')].some(button => button.innerText.trim() === 'Preview')), false, 'Manual Preview button should not exist');
  await navigatePage(page, selectedURL);
  await waitForDOM(page, () => document.body.innerText.includes('DATASET WORKSPACE'), {}, 30000);
  await action('Open new table', page.getByRole('button', { name: 'New table', exact: true }));
  await waitForDOM(page, () => Boolean(document.querySelector('button[aria-label="Choose Observation rows"]:not(:disabled)')), {}, 30000);
  await fill('Name the table', page.locator('#first-table-name'), tableName);
  await action('Choose Observation rows', page.getByRole('button', { name: 'Choose Observation rows', exact: true }));
  created = true;
  await waitForDOM(page, ({ tableName: name }) => document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(name), { tableName }, 30000);
  await waitForDOM(page, () => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false, {}, 30000);
  await action('Open row settings', page.locator('[data-testid="construction-rows-settings-trigger"]'));
  await waitForDOM(page, () => Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"]')), {}, 5000);
  await waitForDOM(page, () => [...document.querySelectorAll('[aria-label="Starting collection"] button')].some(button => button.innerText === 'Use selected resources' && !button.disabled), {}, 30000);
  const useSelected = page.getByRole('button', { name: 'Use selected resources', exact: true });
  await action('Use selected resources', useSelected);
  await waitForDOM(page, () => document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes('1 Observation resources attached'), {}, 30000);
  await action('Close row settings', page.getByRole('dialog', { name: 'Row definition settings' }).getByRole('button', { name: 'Back to table', exact: true }));
  await waitForDOM(page, () => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false, {}, 30000);
  await action('Reopen row settings', page.locator('[data-testid="construction-rows-settings-trigger"]'));
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid="construction-action-pivot-rows"]:not(:disabled)')), {}, 30000);
  report.choice = await inspectDOM(page, () => { const button = document.querySelector('[data-testid="construction-action-pivot-rows"]'); return { disabled: button?.disabled, text: button?.innerText }; });
  assert.equal(report.choice.disabled, false, report.choice.text);
  await action('Choose coded values as columns', page.locator('[data-testid="construction-action-pivot-rows"]'));
  await waitForDOM(page, () => Boolean(document.querySelector('section[aria-label="Coded values as columns"]')), {}, 30000);
  await waitForDOM(page, () => !document.querySelector('section[aria-label="Coded values as columns"] [role="status"]'), {}, 30000);
  report.sources = await inspectDOM(page, () => [...document.querySelectorAll('input[name="coded-pivot-source"]')].map(input => ({ text: input.closest('label')?.innerText, checked: input.checked, disabled: input.disabled })));
  const matchingSource = report.sources.find(source => source.text?.includes('component') && source.text.toLowerCase().includes(mode));
  assert(matchingSource, `Direct component ${mode} source is missing`);
  await action(`Select ${mode} coded source`, page.locator('section[aria-label="Coded values as columns"] label').filter({ hasText: matchingSource.text }));
  await waitForDOM(page, ({ label }) => [...document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]')].some(input => input.closest('label')?.innerText.toLowerCase().includes(label)), { label: expected[0].label.toLowerCase() }, 30000);
  report.categories = await inspectDOM(page, () => [...document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]')].map(input => ({ text: input.closest('label')?.innerText, disabled: input.disabled })));
  const proposalStarted = Date.now();
  for (const pair of expected) {
    const match = report.categories.filter(category => category.text?.toLowerCase().includes(pair.label.toLowerCase()));
    assert.equal(match.length, 1, `Expected one ${pair.label} category`);
    assert.equal(match[0].disabled, false, `${pair.label} category must be enabled`);
    await action(`Select ${pair.label}`, page.locator('section[aria-label="Coded values as columns"] label').filter({ hasText: pair.label }));
  }
  const proposal = await requestCapture.waitFor(entry => entry.path.endsWith('/construction-proposals') && entry.method === 'POST', { fromIndex: 0, timeoutMs: Math.max(1, 5000 - (Date.now() - proposalStarted)) });
  await waitForDOM(page, () => ['ready', 'error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')), {}, Math.max(1, 5000 - (Date.now() - proposalStarted)));
  report.timingsMs.proposal = Date.now() - proposalStarted;
  assert(report.timingsMs.proposal < 5000, `Proposal rendered in ${report.timingsMs.proposal} ms`);
  assert.equal(proposal.status, 200);
  assert(proposal.response?.proposalId, 'Browser proposal must return an applicable receipt');
  report.proposal = await inspectDOM(page, () => ({ status: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'), text: document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText, applyDisabled: document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled, visibleRows: document.body.innerText.slice(-500) }));
  assert.equal(report.proposal.status, 'ready', report.proposal.text);
  assert.equal(report.proposal.applyDisabled, false);
  for (const pair of expected) assert(report.proposal.visibleRows.includes(pair.value), JSON.stringify(report.proposal));
  await action('Apply coded pivot', page.locator('[data-testid="construction-apply-proposal"]'));
  await waitForDOM(page, () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 30000);
  await navigatePage(page, selectedURL);
  await waitForDOM(page, ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 30000);
  await action('Open coded pivot table', page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
  const previewStarted = Date.now();
  await waitForDOM(page, ({ expectedValue }) => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(expectedValue), { expectedValue: expected.at(-1).value }, 30000);
  timing('preview', previewStarted);
  report.saved = await inspectDOM(page, () => ({ headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText), rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText)) }));
  assert.equal(report.saved.rows.length, 1, JSON.stringify(report.saved));
  for (const pair of expected) assert(report.saved.rows[0].includes(pair.value), JSON.stringify(report.saved));
  await action('Select coded pivot history', page.locator('[data-testid^="construction-history-step-"]'));
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid^="construction-edit-step-"]:not(:disabled)')), {}, 30000);
  await action('Edit coded pivot', page.locator('[data-testid^="construction-edit-step-"]:not(:disabled)'));
  await waitForDOM(page, () => Boolean(document.querySelector('section[aria-label="Coded values as columns"] select')), {}, 30000);
  await waitForDOM(page, ({ count }) => document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]:checked').length === count, { count: expected.length }, 30000);
  report.reopened = await inspectDOM(page, () => { const section = document.querySelector('section[aria-label="Coded values as columns"]'); return { selected: [...section.querySelectorAll('input[type="checkbox"]:checked')].map(input => input.closest('label')?.innerText), source: [...section.querySelectorAll('input[name="coded-pivot-source"]:checked')].map(input => input.closest('label')?.innerText), policies: [...section.querySelectorAll('select')].map(input => input.value) }; });
  assert.equal(report.reopened.selected.length, expected.length, JSON.stringify(report.reopened));
  assert.equal(report.reopened.source.length, 1, JSON.stringify(report.reopened));
  const section = page.locator('section[aria-label="Coded values as columns"]');
  await action('Expand coded pivot options', section.locator('details summary'));
  await select('Set missing value policy', section.locator('select').nth(1), 'ERROR');
  const editStarted = Date.now();
  const editRequestStart = report.nativeRequests.length;
  await requestCapture.waitFor(entry => entry.path.endsWith('/construction-proposals') && entry.method === 'POST', { fromIndex: editRequestStart, timeoutMs: 5000 });
  await waitForDOM(page, () => ['ready', 'error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')), {}, 5000);
  timing('editProposal', editStarted);
  report.editedProposal = await inspectDOM(page, () => ({ status: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'), text: document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText }));
  assert.equal(report.editedProposal.status, 'ready', report.editedProposal.text);
  await waitForDOM(page, () => document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled === false, {}, 5000);
  await action('Apply edited coded pivot', page.locator('[data-testid="construction-apply-proposal"]'));
  await waitForDOM(page, () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1 && !document.querySelector('section[aria-label="Coded values as columns"]'), {}, 30000);
  await navigatePage(page, selectedURL);
  await waitForDOM(page, ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 30000);
  await action('Reopen coded pivot table', page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
  await action('Select coded pivot history after reload', page.locator('[data-testid^="construction-history-step-"]'));
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid^="construction-edit-step-"]:not(:disabled)')), {}, 30000);
  await action('Reopen coded pivot editor', page.locator('[data-testid^="construction-edit-step-"]:not(:disabled)'));
  await waitForDOM(page, () => document.querySelectorAll('section[aria-label="Coded values as columns"] select')[1]?.value === 'ERROR', {}, 30000);
  report.editedReload = await inspectDOM(page, () => ({ policies: [...document.querySelectorAll('section[aria-label="Coded values as columns"] select')].map(input => input.value) }));
  await action('Back to table', page.getByRole('button', { name: 'Back to table', exact: true }));
  await waitForDOM(page, () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 30000);
  report.editedReload.historyCount = 1;
  const editedPreviewStarted = Date.now();
  await waitForDOM(page, ({ expectedValue }) => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(expectedValue), { expectedValue: expected.at(-1).value }, 30000);
  timing('editedPreview', editedPreviewStarted);
  report.editedSaved = await inspectDOM(page, () => ({ headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText), rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText)) }));
  assert.deepEqual(report.editedSaved, report.saved, 'Changing missing-value handling changed populated CDA values');
  const remove = page.locator('[data-testid^="construction-remove-step-"]');
  if (!(await remove.count())) await action('Select coded group before remove', page.locator('[data-testid^="construction-history-step-"]'));
  await waitForDOM(page, () => Boolean(document.querySelector('[data-testid^="construction-remove-step-"]')), {}, 30000);
  const removalStart = report.nativeRequests.length;
  await action('Remove coded pivot group', page.locator('[data-testid^="construction-remove-step-"]'));
  await requestCapture.waitFor(entry => entry.path.endsWith('/construction-proposals') && entry.method === 'POST', { fromIndex: removalStart, timeoutMs: 5000 });
  await waitForDOM(page, () => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', {}, 30000);
  await action('Apply coded pivot removal', page.locator('[data-testid="construction-apply-proposal"]'));
  await waitForDOM(page, () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0, {}, 30000);
  await navigatePage(page, selectedURL);
  await waitForDOM(page, ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 30000);
  await action('Open restored table', page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
  report.restored = await inspectDOM(page, () => ({ historyCount: document.querySelectorAll('[data-testid^="construction-history-step-"]').length, body: document.body.innerText.slice(0, 900) }));
  assert.equal(report.restored.historyCount, 0);
  await waitForDOM(page, ({ id }) => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(id), { id: observationId }, 30000);
  report.restored.headers = await inspectDOM(page, () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText));
  assert.deepEqual(report.restored.headers, ['OBSERVATION ID']);
  await requestCapture.flush();
  assert.equal(report.nativeRequests.filter(request => request.status >= 400).length, 0, JSON.stringify(report.nativeRequests));
  assert.deepEqual(browser.diagnostics.console, []);
  assert.deepEqual(browser.diagnostics.pageErrors, []);
  assert.deepEqual(browser.diagnostics.networkFailures, []);
  assert.deepEqual(browser.diagnostics.httpFailures, []);
  assert(Object.values(report.timingsMs).every(ms => ms < 5000), JSON.stringify(report.timingsMs));
  report.outcome = 'passed';
} catch (error) {
  report.outcome = 'failed';
  report.failure = String(error.stack ?? error);
  report.failures.push(report.failure);
  if (browser) await browser.captureFailure(error, { phase: 'coded-pivot-lifecycle', action: tracker.activeAction, elapsedMs: tracker.activeAction ? Date.now() - tracker.activeAction.startedAt : undefined, state: { tableName, mode, timingsMs: report.timingsMs, requests: report.nativeRequests } });
  process.exitCode = 1;
} finally {
  if (browser && created) {
    try {
      await navigatePage(browser.page, selectedURL);
      await waitForDOM(browser.page, ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 10000);
      await action('Open table for cleanup', browser.page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
      await waitForDOM(browser.page, () => document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(tableName), {}, 10000);
      await waitForDOM(browser.page, () => document.querySelector('[data-testid="construction-delete-table"]')?.disabled === false, {}, 10000);
      browser.page.once('dialog', dialog => dialog.accept());
      await action('Delete verifier table', browser.page.locator('[data-testid="construction-delete-table"]'));
      await waitForDOM(browser.page, ({ name }) => ![...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 10000);
      report.cleanup = 'deleted';
    } catch (error) {
      report.cleanup = 'failed';
      report.cleanupFailure = String(error);
      process.exitCode = 1;
    }
  }
  if (requestCapture) await requestCapture.flush();
  if (browser) {
    report.diagnostics = browser.diagnostics;
    await browser.close();
  }
  try { report.verificationIdentity = await verificationIdentity.finish(); }
  catch (error) { report.outcome = 'invalidated'; report.identityFailure = String(error); process.exitCode = 1; }
  await writeFile(artifact, JSON.stringify(report, null, 2));
}
console.log(JSON.stringify({ outcome: report.outcome, clicks: report.clicks, timingsMs: report.timingsMs, evidence, artifact }));
