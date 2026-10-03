import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { setTimeout as pause } from 'node:timers/promises';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = 'cda-builder-full-qa-1790440983382';
const related = process.argv[2]?.startsWith('related') ?? false;
const relatedSourceKey = process.argv[2] === 'related-source-key';
const resourceType = related ? 'Specimen' : 'Observation';
const observationId = related ? '230b352c-99de-50f3-a3b1-a4f6680615ab' : '485e2567-b566-56f3-b5bd-5f025f37cd95';
const baseURL = `http://127.0.0.1:30008/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const artifact = `.artifacts/cda-builder/implicit-pivot-inputs-${relatedSourceKey ? 'related-source-key' : related ? 'related' : 'root'}.json`;
const tableName = `Implicit pivot fields QA ${Date.now()}`;
const rawOracle=execFileSync('rtk',['proxy','docker','exec','loom-dev-6d7df93d6a37-arangodb-1','arangosh','--server.database','loom_dev','--javascript.execute-string', 'var doc=db.Observation.firstExample({id:"485e2567-b566-56f3-b5bd-5f025f37cd95"});print(JSON.stringify({id:doc.id,status:doc.payload.status,subject:doc.payload.subject.reference,specimen:doc.payload.specimen.reference}))'],{encoding:'utf8'});
const oracle=JSON.parse(rawOracle.trim().split(/\r?\n/).find(line=>line.startsWith('{')));
assert.equal(oracle.status,'final');
assert.equal(oracle.specimen,'Specimen/230b352c-99de-50f3-a3b1-a4f6680615ab');
const browser = await launchBrowser('/private/tmp');
const state = { tableName, observationId, related, relatedSourceKey, oracle, clicks: 0, timingsMs: {}, requests: [] };
let selectedURL = baseURL;
let created = false;
const started = new Map();
browser.cdp.on('Network.requestWillBeSent', (event) => {
  if (event.request.url.includes('/authoring/v2/')) {
    started.set(event.requestId, Date.now());
    if (event.request.url.includes('construction-proposals')) state.sentProposal = true;
  }
});
browser.cdp.on('Network.responseReceived', (event) => {
  if (event.response.url.includes('/authoring/v2/')) state.requests.push({
    path: new URL(event.response.url).pathname,
    status: event.response.status,
    elapsedMs: Date.now() - (started.get(event.requestId) ?? Date.now()),
  });
});
const click = async (source) => { state.clicks++; return browserEval(browser.cdp, source); };
const proposalResponses = () => state.requests.filter(request => request.path.endsWith('/construction-proposals') && request.status === 200).length;
const apply = async () => {
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
};
try {
  await navigate(browser.cdp, baseURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`);
  assert.equal(await browserEval(browser.cdp, `return [...document.querySelectorAll('button')].some(button=>button.innerText.trim()==='Preview');`), false, 'Manual Preview button should not exist');
  const selection = await browserEval(browser.cdp, `
    const base='/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}';
    const builder=await (await fetch(base+'/authoring/v2/builder')).json();
    const response=await fetch(base+'/selections',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({snapshotToken:builder.catalog.snapshotToken,idempotencyKey:'cda-coded-pivot-${Date.now()}',source:{kind:'resources',resources:{refs:[{project:'loom_dev_cda_fhir',generation:builder.catalog.generation,resourceType:${JSON.stringify(resourceType)},id:${JSON.stringify(observationId)}}]}}})});
    return {status:response.status,body:await response.json()};`);
  assert.equal(selection.status, 201, JSON.stringify(selection));
  selectedURL = `${baseURL}&selection=${encodeURIComponent(selection.body.id)}`;
  await navigate(browser.cdp, selectedURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Choose ${resourceType} rows"]:not(:disabled)'))`);
  await click(`const input=document.querySelector('#first-table-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(tableName)});input.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('button[aria-label="Choose ${resourceType} rows"]').click();return true;`);
  created = true;
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(${JSON.stringify(tableName)})`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  await click(`document.querySelector('[data-testid="construction-rows-settings-trigger"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"]'))`);
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources'&&!button.disabled))`);
  await click(`[...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes('1 ${resourceType} resources attached')`);
  await click(`document.querySelector('[role="dialog"][aria-label="Row definition settings"] button').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  await click(`document.querySelector('[data-testid="construction-rows-settings-trigger"]').click();return true;`);
  if (related) {
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false`);
    await click(`document.querySelector('[data-testid="construction-action-related-rows"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]'))`);
    await click(`const select=document.querySelector('[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]');Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(select,'Observation');select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[data-testid="construction-related-expand-editor"] input[type="radio"]')].find(input=>input.getAttribute('aria-label')?.toLowerCase().includes('specimen')&&!input.disabled))`);
    await click(`[...document.querySelectorAll('[data-testid="construction-related-expand-editor"] input[type="radio"]')].find(input=>input.getAttribute('aria-label')?.toLowerCase().includes('specimen')).click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`,30000);
    await apply();
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
    await click(`document.querySelector('[data-testid="construction-rows-settings-trigger"]').click();return true;`);
  }
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-action-pivot-rows"]'))`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-pivot-rows"]')?.disabled===false`);
  state.choice = await browserEval(browser.cdp, `const button=document.querySelector('[data-testid="construction-action-pivot-rows"]');return {disabled:button.disabled,text:button.innerText};`);
  assert.equal(state.choice.disabled, false, state.choice.text);
  await click(`document.querySelector('[data-testid="construction-action-pivot-rows"]').click();return true;`);
  if (!related) {
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find(button=>button.innerText==='Change row operation'))`);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText==='Change row operation').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-choice-pivot"]:not(:disabled)'))`);
  await click(`document.querySelector('[data-testid="construction-reshape-choice-pivot"]').click();return true;`);
  }
  await waitForBrowser(browser.cdp, `document.querySelectorAll('select[aria-label="Pivot category field"] optgroup option').length>0`);
  state.options=await browserEval(browser.cdp, `return [...document.querySelectorAll('select[aria-label="Pivot category field"] option')].map(option=>({label:option.text,value:option.value}));`);
  const category=state.options.find(option=>option.label.toLowerCase().includes('status'));
  const value=state.options.find(option=>option.label.toLowerCase().includes('subject.reference'));
  assert(category&&value, JSON.stringify(state.options));
  const select=async(label, val)=>click(`const select=document.querySelector('select[aria-label="${label}"]');Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(select,${JSON.stringify(val)});select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
  await select('Pivot values field',value.value);
  if (related && !relatedSourceKey) {
    await click(`const input=document.querySelector('input[aria-label="Pivot group ${resourceType} ID"]');if(!input.checked)input.click();return true;`);
  } else {
    if (relatedSourceKey) await click(`for(const input of document.querySelectorAll('input[aria-label^="Pivot group "]'))if(input.checked)input.click();return true;`);
    const group=state.options.find(option=>option.label.toLowerCase().includes('specimen.reference'));
    assert(group,JSON.stringify(state.options));
    await select('Add pivot group field',group.value);
  }
  await select('Pivot category field',category.value);
  const scanStart=Date.now();
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText==='Find category values').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))`,30000);
  state.timingsMs.categories=Date.now()-scanStart;
  await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`,30000);
  state.proposal=await browserEval(browser.cdp,`return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.body.innerText.slice(-1600)};`);
  assert.equal(state.proposal.status,'ready',state.proposal.text);
  assert(state.proposal.text.includes('Patient/da65b4e6-3946-50d9-ab1a-65af2e560b1c'),state.proposal.text);
  await apply();
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===${related ? 2 : 1}`);
  const reload=async()=>{await navigate(browser.cdp,selectedURL);await waitForBrowser(browser.cdp,`[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`);await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);};
  await reload();
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes('Patient/da65b4e6-3946-50d9-ab1a-65af2e560b1c')`);
  state.saved=await browserEval(browser.cdp,`return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
  assert.deepEqual(state.saved.rows,[[related && !relatedSourceKey ? observationId : oracle.specimen,oracle.subject]]);
  await click(`[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].at(-1).click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]:not(:disabled)'))`);
  await click(`document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Pivot category field"]'))`);
  state.reopened=await browserEval(browser.cdp,`return [...document.querySelectorAll('select[aria-label^="Pivot"]')].map(select=>({label:select.getAttribute('aria-label'),text:select.selectedOptions[0]?.text,value:select.value}));`);
  assert(state.reopened.find(select=>select.label==='Pivot category field')?.text.toLowerCase().includes('status'),JSON.stringify(state.reopened));
  await click(`document.querySelector('[data-testid="construction-reshape-pivot-advanced"] summary').click();return true;`);
  const beforeEdit = proposalResponses();
  await select('Pivot missing cell policy','ERROR');
  const editStarted = Date.now();
  while (proposalResponses() <= beforeEdit && Date.now() - editStarted < 30000) await pause(200);
  assert(proposalResponses() > beforeEdit, 'Changed Pivot settings did not obtain a new proposal');
  await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`,30000);
  assert.equal(await browserEval(browser.cdp,`return document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status');`),'ready');
  await apply();
  await reload();
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes('Patient/da65b4e6-3946-50d9-ab1a-65af2e560b1c')`);
  await click(`[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].at(-1).click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]:not(:disabled)'))`);
  await click(`document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`);
  await apply();
  await reload();
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(${JSON.stringify(observationId)})`);
  state.restored=await browserEval(browser.cdp,`return {historyCount:document.querySelectorAll('[data-testid^="construction-history-step-"]').length,headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText)};`);
  assert.equal(state.restored.historyCount,related ? 1 : 0);
  assert.deepEqual(state.restored.headers,related ? ['SPECIMEN ID','OBSERVATION FHIR RESOURCE ID'] : ['OBSERVATION ID']);
  assert.equal(state.requests.filter(request=>request.status>=400).length,0,JSON.stringify(state.requests));
  console.log(JSON.stringify({outcome:'passed',clicks:state.clicks,timingsMs:state.timingsMs,saved:state.saved,artifact}));
} catch (error) {
  state.failure = String(error);
  state.dom = await browserEval(browser.cdp, `return document.body.innerText.slice(0,3500);`).catch(() => undefined);
  console.error(JSON.stringify({ outcome: 'failed', failure: state.failure, proposal: state.proposal, artifact }));
  process.exitCode = 1;
} finally {
  if (created) {
    await navigate(browser.cdp, selectedURL).catch(() => {});
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`).catch(() => {});
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))?.click();return true;`).catch(() => {});
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(${JSON.stringify(tableName)})`).catch(() => {});
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-delete-table"]')?.disabled===false`, 10000).catch(() => {});
    await browserEval(browser.cdp, `window.confirm=()=>true;document.querySelector('[data-testid="construction-delete-table"]')?.click();return true;`).catch(() => {});
    state.cleanup = await waitForBrowser(browser.cdp, `![...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 10000).then(() => 'deleted', () => 'failed');
    if (state.cleanup !== 'deleted') process.exitCode = 1;
  }
  await mkdir('.artifacts/cda-builder', { recursive: true });
  await writeFile(artifact, JSON.stringify(state, null, 2));
  await browser.close();
}
