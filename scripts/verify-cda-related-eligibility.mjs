import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = 'cda-builder-full-qa-1790440983382';
const selectionId = 'selection_550bceffc9cda4631174bea2ae519df97996fae4b04c1b34cf445d9ffc83d09a';
const manyId = '02f8e963-73b8-50ea-b840-c4a80719a06a';
const zeroId = '54b50ad3-aa10-5483-85e2-5382aac7d374';
const uiOrigin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const apiOrigin = (process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188').replace(/\/$/, '');
const pageURL = `${uiOrigin}/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder&selection=${selectionId}`;
const authoringURL = `${apiOrigin}/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}/authoring/v2`;
const tableName = `CDA related eligibility QA ${Date.now()}`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const state = { pageURL, tableName, clicks: [], timingsMs: {}, responses: [] };
const browser = await launchBrowser('/private/tmp');
let outputId;

browser.cdp.on('Network.responseReceived', ({ response, requestId }) => {
  if (response.url.includes('/authoring/v2/')) state.responses.push({ requestId, path: new URL(response.url).pathname, status: response.status });
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
    body: JSON.stringify({ commandId: crypto.randomUUID(), semanticsVersion: 10,
      snapshotToken: before.catalog.snapshotToken,
      expectedDraftVersion: before.draftVersion, expectedDraftDigest: before.draftDigest, commands }),
    signal: AbortSignal.timeout(30_000),
  });
  const body = await response.json();
  assert.equal(response.status, 200, JSON.stringify(body));
};
const click = async (script, label) => {
  state.lastActionStarted = Date.now();
  await browserEval(browser.cdp, script);
  state.clicks.push(label);
};
const selectTable = async () => {
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30_000);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`, 'Select temporary table');
};
const preview = async (label, expected) => {
  const started = state.lastActionStarted ?? Date.now();
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))&&!document.body.innerText.includes('Loading the preview…')`, 30_000);
  await waitForBrowser(browser.cdp, `(()=>{const rows=[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>row.querySelector('[role="cell"]')?.innerText.trim()).filter(Boolean).sort();return JSON.stringify(rows)===JSON.stringify(${JSON.stringify([...expected].sort())});})()`, 30000);
  const result = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="preview-table-scroll"]');return {headers:[...panel.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()),rows:[...panel.querySelectorAll('[role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length)};`);
  state.timingsMs[label] = Date.now() - started;
  assert(state.timingsMs[label] < 5000, `${label} preview took ${state.timingsMs[label]} ms`);
  assert.deepEqual(result.rows.map(row=>row[0]).sort(), [...expected].sort(), `${label} CDA rows differ`);
  state[label] = result;
};
const propose = async (label, expected) => {
  const started = state.lastActionStarted ?? Date.now();
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 30_000);
  const result = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-proposal-preview"]');return {headers:[...panel.querySelectorAll('thead th')].map(cell=>cell.innerText.trim()),rows:[...panel.querySelectorAll('tbody tr')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim())),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
  state.timingsMs[label] = Date.now() - started;
  assert(state.timingsMs[label] < 5000, `${label} proposal took ${state.timingsMs[label]} ms`);
  assert.equal(result.applyDisabled, false);
  assert.deepEqual(result.rows.map(row=>row[0]).sort(), [...expected].sort(), `${label} proposed CDA rows differ`);
  assert.deepEqual(result.headers.map(header=>header.split('\n')[0].toUpperCase()), state.baseline.headers, 'Eligibility changed the output columns');
  state[label] = result;
};
const editSaved = async () => {
  await click(`document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`, 'Select saved eligibility step');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30_000);
  await click(`document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`, 'Edit saved eligibility step');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-related-eligibility-editor"]'))`, 30_000);
};
const setRule = async (rule) => click(`const select=document.querySelector('[aria-label="Related eligibility rule"]');select.value=${JSON.stringify(rule)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`, `Choose ${rule} rule`);
const rawCounts = () => {
  const query = `FOR p IN Patient FILTER p.project == "loom_dev_cda_fhir" AND p.dataset_generation == "cda-fhir-v1" AND p.id IN ${JSON.stringify([manyId,zeroId])} LET n = LENGTH(UNIQUE(FOR e IN fhir_edge FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient" AND e.project == "loom_dev_cda_fhir" AND e.dataset_generation == "cda-fhir-v1" RETURN e._from)) RETURN {id:p.id,count:n}`;
  const output = execFileSync('rtk', ['proxy', 'docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`], { encoding: 'utf8', maxBuffer: 200000 });
  return JSON.parse(output.slice(output.indexOf('[')));
};

try {
  state.rawCounts = rawCounts();
  assert.deepEqual(Object.fromEntries(state.rawCounts.map(item=>[item.id,item.count])), { [manyId]:38, [zeroId]:0 });
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30_000);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`, 'New table');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Choose Patient rows"]:not(:disabled)'))`, 30_000);
  await browserEval(browser.cdp, `const input=document.querySelector('#first-table-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(tableName)});input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
  await click(`document.querySelector('button[aria-label="Choose Patient rows"]').click();return true;`, 'Choose Patient rows');
  const created = await builder();
  outputId = created.workspace.documents.find(document=>document.output.title===tableName)?.output.id;
  assert(outputId, 'Temporary table missing');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`,30000);
  await click(`document.querySelector('[data-testid="construction-rows-settings-trigger"]').click();return true;`, 'Open source setup');
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources'&&!button.disabled))`, 30_000);
  await click(`[...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources').click();return true;`, 'Use selected CDA patients');
  await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes('2 Patient resources attached')`,30000);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='Back to table').click();return true;`, 'Back to table');
  await preview('baseline', [manyId,zeroId]);
  await click(`document.querySelector('button[aria-label^="Filter rows:"]').click();return true;`, 'Open Filter rows');
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='Related records'&&!button.disabled))`, 30_000);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='Related records').click();return true;`, 'Filter by related records');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[aria-label="Related eligibility record type"]'))`, 30_000);
  await click(`const select=document.querySelector('[aria-label="Related eligibility record type"]');select.value='Observation';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`, 'Choose Observation');
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[data-testid="construction-related-eligibility-editor"] input[type="radio"]')].find(input=>input.getAttribute('aria-label')?.includes('subject')))`, 30_000);
  state.routes = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="construction-related-eligibility-editor"] input[type="radio"]')].map(input=>({label:input.getAttribute('aria-label'),disabled:input.disabled}));`);
  await click(`[...document.querySelectorAll('[data-testid="construction-related-eligibility-editor"] input[type="radio"]')].find(input=>input.getAttribute('aria-label')?.includes('subject')).click();return true;`, 'Choose Subject route');
  await propose('existsProposal', [manyId]);
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`, 'Apply related eligibility');
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30_000);
  await navigate(browser.cdp,pageURL); await selectTable(); await preview('savedExists',[manyId]);
  await editSaved(); await setRule('ABSENT'); await propose('absentProposal',[zeroId]);
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`, 'Apply absent rule');
  await navigate(browser.cdp,pageURL); await selectTable(); await preview('savedAbsent',[zeroId]);
  await editSaved(); await setRule('COUNT_AT_LEAST'); await propose('countProposal',[manyId]);
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`, 'Apply count rule');
  await navigate(browser.cdp,pageURL); await selectTable(); await preview('savedCount',[manyId]);
  await click(`document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`, 'Select eligibility for removal');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30_000);
  await click(`document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`, 'Remove eligibility step');
  await propose('removeProposal',[manyId,zeroId]);
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`, 'Apply step removal');
  await navigate(browser.cdp,pageURL); await selectTable(); await preview('restored',[manyId,zeroId]);
  assert(state.responses.every(response=>response.status<400), 'Browser received authoring API errors');
} catch (error) {
  state.failedResponseBodies = await Promise.all(state.responses.filter(r=>r.status>=400).map(async r=>({path:r.path,...await browser.cdp.send('Network.getResponseBody',{requestId:r.requestId}).catch(e=>({captureError:String(e)}))})));
  state.error = error instanceof Error ? error.message : String(error);
  state.failureUI = await browserEval(browser.cdp, `return {body:document.body.innerText.slice(0,5000),alerts:[...document.querySelectorAll('[role="alert"]')].map(item=>item.innerText)};`).catch(()=>undefined);
  throw error;
} finally {
  if (outputId) { try { await command([{type:'DELETE_TABLE',outputId}]); state.cleanup='temporary table deleted'; } catch (error) { state.cleanup=String(error); } }
  await mkdir(evidenceDirectory,{recursive:true});
  await writeFile(join(evidenceDirectory,'related-eligibility-single-step.json'),JSON.stringify(state,null,2));
  await browser.close();
  console.log(JSON.stringify({evidenceDirectory,clicks:state.clicks,timingsMs:state.timingsMs,rawCounts:state.rawCounts,cleanup:state.cleanup,error:state.error,responses:state.responses.filter(response=>response.status>=400)},null,2));
}
