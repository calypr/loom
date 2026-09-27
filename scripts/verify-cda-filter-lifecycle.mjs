import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = process.argv[2] ?? 'cda-builder-full-qa-1790440983382';
const uiOrigin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const apiOrigin = (process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188').replace(/\/$/, '');
const pageURL = `${uiOrigin}/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const authoringURL = `${apiOrigin}/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}/authoring/v2`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const tableName = `CDA filter QA ${Date.now()}`;
const state = { pageURL, tableName, clicks: [], timingsMs: {}, responses: [] };
const browser = await launchBrowser('/private/tmp');
let outputId;

browser.cdp.on('Network.responseReceived', ({ response }) => {
  if (response.url.includes('/authoring/v2/')) {
    state.responses.push({ path: new URL(response.url).pathname, status: response.status });
  }
});

const builder = async () => {
  const response = await fetch(`${authoringURL}/builder`, { signal: AbortSignal.timeout(30_000) });
  assert.equal(response.status, 200);
  return response.json();
};

const command = async (commands) => {
  const before = await builder();
  const response = await fetch(`${authoringURL}/commands`, {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      commandId: crypto.randomUUID(), semanticsVersion: 9,
      snapshotToken: before.catalog.snapshotToken,
      expectedDraftVersion: before.draftVersion,
      expectedDraftDigest: before.draftDigest,
      commands,
    }),
    signal: AbortSignal.timeout(30_000),
  });
  const body = await response.json();
  assert.equal(response.status, 200, JSON.stringify(body));
  return body;
};

const click = async (script, label) => {
  await browserEval(browser.cdp, script);
  state.clicks.push(label);
};

const setValue = async (selector, value) => browserEval(browser.cdp, `const input=document.querySelector(${JSON.stringify(selector)});if(!input)throw new Error('Input missing');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(value)});input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);

const selectTable = async () => {
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30_000);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`, 'Select temporary table');
};

const preview = async (stage, expectedID) => {
  const started = Date.now();
  await click(`[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview'&&!button.disabled).click();return true;`, `Preview ${stage}`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') && !document.body.innerText.includes('Loading the preview…')`, 30_000);
  const result = await browserEval(browser.cdp, `const scroll=document.querySelector('[data-testid="preview-table-scroll"]');return {rowCount:scroll.querySelector('[role="table"]')?.getAttribute('aria-rowcount'),headers:[...scroll.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()),rows:[...scroll.querySelectorAll('[role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length)};`);
  state.timingsMs[stage] = Date.now() - started;
  assert(state.timingsMs[stage] < 5000, `${stage} preview took ${state.timingsMs[stage]} ms`);
  if (expectedID) {
    assert.equal(result.rowCount, '2', `${stage} must have exactly one data row`);
    assert.deepEqual(result.rows, [[expectedID]], `${stage} does not show the selected CDA Patient`);
  }
  state[stage] = result;
  return result;
};

const rawPatients = (ids) => {
  const query = `FOR d IN Patient FILTER d.project == "loom_dev_cda_fhir" AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN d.id`;
  const output = execFileSync('rtk', ['docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`], { encoding: 'utf8', maxBuffer: 200000 });
  return JSON.parse(output.slice(output.indexOf('[')));
};

const filterEditor = async () => {
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]:not(:disabled)'))`, 30_000);
  const choices = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-filter-editor"]');return {columns:[...panel.querySelector('select[aria-label="Column"]').options].map(option=>({label:option.textContent.trim(),value:option.value,disabled:option.disabled})),conditions:[...panel.querySelector('select[aria-label="Condition"]').options].map(option=>({label:option.textContent.trim(),value:option.value,disabled:option.disabled}))};`);
  const id = choices.columns.find((choice) => choice.label.startsWith('Patient ID'));
  const equals = choices.conditions.find((choice) => choice.value === 'EQUALS');
  assert(id && !id.disabled, 'Patient ID cannot be filtered');
  assert(equals && !equals.disabled, 'Patient ID equality is unavailable');
  return { choices, id };
};

const setEquals = async (id, value) => {
  await click(`const select=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');select.value=${JSON.stringify(id.value)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`, 'Choose Patient ID column');
  await click(`const select=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]');select.value='EQUALS';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`, 'Choose equals condition');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]:not(:disabled)'))`, 30_000);
  await setValue('[data-testid="construction-filter-editor"] input[aria-label="Value"]', value);
  state.clicks.push('Enter Patient ID value');
  const started = Date.now();
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready' && document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.includes(${JSON.stringify(value)})`, 30_000);
  const proposed = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-proposal-preview"]');return {rows:[...panel.querySelectorAll('tbody tr')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim())),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
  assert.deepEqual(proposed.rows, [[value]]);
  assert.equal(proposed.applyDisabled, false);
  return { proposed, elapsedMs: Date.now() - started };
};

try {
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30_000);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`, 'New table');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Choose Patient rows"]:not(:disabled)'))`, 30_000);
  await setValue('#first-table-name', tableName);
  await click(`document.querySelector('button[aria-label="Choose Patient rows"]').click();return true;`, 'Choose Patient rows');
  const created = await builder();
  outputId = created.workspace.documents.find((document) => document.output.title === tableName)?.output.id;
  assert(outputId, 'Temporary Patient table was not created');
  const baseline = await preview('baseline');
  assert.equal(baseline.headers[0], 'PATIENT ID');
  assert(baseline.rows.length >= 2, 'Baseline preview did not show two Patient IDs');
  const ids = baseline.rows.slice(0, 2).map((row) => row[0]);
  assert.deepEqual(new Set(rawPatients(ids)), new Set(ids), 'Preview IDs are absent from CDA Patient records');
  state.sourceIDs = ids;

  await click(`document.querySelector('button[aria-label^="Filter rows:"]').click();return true;`, 'Open Filter rows');
  const { choices, id } = await filterEditor();
  state.filterChoices = choices;
  state.firstProposal = await setEquals(id, ids[0]);
  state.timingsMs.firstProposal = state.firstProposal.elapsedMs;
  assert(state.timingsMs.firstProposal < 5000);
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`, 'Apply Patient ID filter');
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30_000);
  await navigate(browser.cdp, pageURL);
  await selectTable();
  await preview('saved', ids[0]);

  await click(`document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`, 'Select saved Filter rows step');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30_000);
  await click(`document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`, 'Edit Filter rows step');
  await filterEditor();
  const original = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]')?.value;`);
  assert.equal(original, ids[0], 'Saved filter did not reopen with its exact value');
  await setValue('[data-testid="construction-filter-editor"] input[aria-label="Value"]', ids[1]);
  state.clicks.push('Change Patient ID value');
  const editStarted = Date.now();
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready' && document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.includes(${JSON.stringify(ids[1])})`, 30_000);
  state.timingsMs.editProposal = Date.now() - editStarted;
  assert(state.timingsMs.editProposal < 5000);
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`, 'Apply edited Filter rows step');
  await navigate(browser.cdp, pageURL);
  await selectTable();
  await preview('edited', ids[1]);

  await click(`document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`, 'Select edited Filter rows step');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30_000);
  await click(`document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`, 'Remove Filter rows step');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 30_000);
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`, 'Apply step removal');
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`, 30_000);
  await navigate(browser.cdp, pageURL);
  await selectTable();
  const restored = await preview('restored');
  assert(restored.rows.length >= 2, 'Removing the filter did not restore multiple Patient rows');
  assert.deepEqual(new Set(rawPatients(restored.rows.map((row) => row[0]))), new Set(restored.rows.map((row) => row[0])));
  assert(state.responses.every((response) => response.status < 400), 'Browser received an authoring API error');
} catch (error) {
  state.error = error instanceof Error ? error.message : String(error);
  state.failureUI = await browserEval(browser.cdp, `return {text:document.body.innerText.slice(0,3500),alerts:[...document.querySelectorAll('[role="alert"]')].map(node=>node.innerText)};`).catch(() => undefined);
  throw error;
} finally {
  if (outputId) {
    try { await command([{ type: 'DELETE_TABLE', outputId }]); state.cleanup = 'temporary table deleted'; }
    catch (error) { state.cleanup = error instanceof Error ? error.message : String(error); }
  }
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'filter-lifecycle.json'), JSON.stringify(state, null, 2));
  await browser.close();
  console.log(JSON.stringify({ evidenceDirectory, clicks: state.clicks.length, timingsMs: state.timingsMs, error: state.error, cleanup: state.cleanup }, null, 2));
}
