import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = 'cda-builder-full-qa-1790440983382';
const observationId = '485e2567-b566-56f3-b5bd-5f025f37cd95';
const mode = process.argv[2] === 'string' ? 'string' : 'integer';
const expected = mode === 'string'
  ? [{ label: 'Specimen type', value: 'analyte' }, { label: 'Primary disease type', value: 'Ductal and lobular neoplasms' }]
  : [{ label: 'Days to collection', value: '162' }];
const baseURL = `http://127.0.0.1:30008/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const artifact = `.artifacts/cda-builder/bounded-coded-pivot-${mode}.json`;
const tableName = `Coded pivot ${mode} QA ${Date.now()}`;
const browser = await launchBrowser('/private/tmp');
const state = { tableName, observationId, mode, expected, clicks: 0, timingsMs: {}, requests: [] };
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
try {
  await navigate(browser.cdp, baseURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`);
  const selection = await browserEval(browser.cdp, `
    const base='/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}';
    const builder=await (await fetch(base+'/authoring/v2/builder')).json();
    const response=await fetch(base+'/selections',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({snapshotToken:builder.catalog.snapshotToken,idempotencyKey:'cda-coded-pivot-${Date.now()}',source:{kind:'resources',resources:{refs:[{project:'loom_dev_cda_fhir',generation:builder.catalog.generation,resourceType:'Observation',id:${JSON.stringify(observationId)}}]}}})});
    return {status:response.status,body:await response.json()};`);
  assert.equal(selection.status, 201, JSON.stringify(selection));
  selectedURL = `${baseURL}&selection=${encodeURIComponent(selection.body.id)}`;
  await navigate(browser.cdp, selectedURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Choose Observation rows"]:not(:disabled)'))`);
  await click(`const input=document.querySelector('#first-table-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(tableName)});input.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('button[aria-label="Choose Observation rows"]').click();return true;`);
  created = true;
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(${JSON.stringify(tableName)})`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  await click(`document.querySelector('[data-testid="construction-rows-settings-trigger"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"]'))`);
  await click(`[...document.querySelectorAll('[role="dialog"][aria-label="Row definition settings"] summary')].find(node=>node.innerText.startsWith('Starting collection:'))?.click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources'&&!button.disabled))`);
  await click(`[...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Starting collection"]')?.innerText.includes('constrain one row per Observation')`);
  await click(`document.querySelector('[role="dialog"][aria-label="Row definition settings"] button').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  await click(`document.querySelector('[data-testid="construction-rows-settings-trigger"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-action-pivot-rows"]'))`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-pivot-rows"]')?.disabled===false`);
  state.choice = await browserEval(browser.cdp, `const button=document.querySelector('[data-testid="construction-action-pivot-rows"]');return {disabled:button.disabled,text:button.innerText};`);
  assert.equal(state.choice.disabled, false, state.choice.text);
  await click(`document.querySelector('[data-testid="construction-action-pivot-rows"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('section[aria-label="Coded values as columns"]'))`);
  await waitForBrowser(browser.cdp, `!document.querySelector('section[aria-label="Coded values as columns"] [role="status"]')`);
  state.sources = await browserEval(browser.cdp, `return [...document.querySelectorAll('input[name="coded-pivot-source"]')].map(input=>({text:input.closest('label')?.innerText,checked:input.checked,disabled:input.disabled}));`);
  const matching = state.sources.findIndex(source => source.text?.includes('component') && source.text?.toLowerCase().includes(mode));
  assert(matching >= 0, `Direct component ${mode} source is missing`);
  await click(`document.querySelectorAll('input[name="coded-pivot-source"]')[${matching}].click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]')].find(input=>input.closest('label')?.innerText.toLowerCase().includes(${JSON.stringify(expected[0].label.toLowerCase())})))`);
  state.categories = await browserEval(browser.cdp, `return [...document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]')].map(input=>({text:input.closest('label')?.innerText,disabled:input.disabled}));`);
  const proposalStarted = Date.now();
  for (const pair of expected) {
    const category = state.categories.findIndex(item => item.text?.toLowerCase().includes(pair.label.toLowerCase()));
    assert(category >= 0 && !state.categories[category].disabled, JSON.stringify(state.categories));
    await click(`document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]')[${category}].click();return true;`);
  }
  await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`);
  state.timingsMs.proposal = Date.now() - proposalStarted;
  state.proposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
  assert.equal(state.proposal.status, 'ready', state.proposal.text);
  assert.equal(state.proposal.applyDisabled, false);
  state.proposal.visibleRows = await browserEval(browser.cdp, `return document.body.innerText.slice(-500);`);
  for (const pair of expected) assert(state.proposal.visibleRows?.includes(pair.value), JSON.stringify(state.proposal));
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`);
  await navigate(browser.cdp, selectedURL);
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
  const previewStarted = Date.now();
  await click(`[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(${JSON.stringify(expected.at(-1).value)})`);
  state.timingsMs.preview = Date.now() - previewStarted;
  state.saved = await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
  assert.equal(state.saved.rows.length, 1, JSON.stringify(state.saved));
  for (const pair of expected) assert(state.saved.rows[0].includes(pair.value), JSON.stringify(state.saved));
  await click(`document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`);
  await click(`document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`);
  await click(`document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`);
  await navigate(browser.cdp, selectedURL);
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
  state.restored = await browserEval(browser.cdp, `return {historyCount:document.querySelectorAll('[data-testid^="construction-history-step-"]').length,body:document.body.innerText.slice(0,900)};`);
  assert.equal(state.restored.historyCount, 0);
  assert.equal(state.requests.filter(request => request.status >= 400).length, 0, JSON.stringify(state.requests));
  assert(state.timingsMs.proposal < 5000 && state.timingsMs.preview < 5000, JSON.stringify(state.timingsMs));
  console.log(JSON.stringify({ outcome: 'passed', clicks: state.clicks, timingsMs: state.timingsMs, saved: state.saved, artifact }));
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
