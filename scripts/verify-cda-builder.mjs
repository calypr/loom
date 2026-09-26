import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const action = process.argv[2] ?? 'Keep rows';
const explorerId = process.argv[3] ?? 'cda-builder-full-qa-1790439585678';
const pageURL = `http://127.0.0.1:30002/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const browser = await launchBrowser('/private/tmp');
const responses = [];
const requests = [];
browser.cdp.on('Network.requestWillBeSent', (event) => {
  if (event.request.url.includes('/authoring/v2/commands')) requests.push({ requestId: event.requestId, postData: event.request.postData });
});
browser.cdp.on('Network.responseReceived', (event) => {
  if (event.response.url.includes('/authoring/v2/')) {
    responses.push({ requestId: event.requestId, path: new URL(event.response.url).pathname, status: event.response.status });
  }
});

try {
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
  if (action === 'Publish current dataset') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
    const before=await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Publish');return {disabled:button?.disabled,text:document.body.innerText.slice(0,800),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText)};`);
    assert.equal(before.disabled,false,'Publish is disabled after successful preview');
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Publish').click();return true;`);
    const deadline=Date.now()+300000;
    while (!responses.some(response=>response.path.endsWith('/publish'))&&Date.now()<deadline) await new Promise(resolve=>setTimeout(resolve,250));
    const published=responses.find(response=>response.path.endsWith('/publish'));
    const after=await browserEval(browser.cdp, `return {text:document.body.innerText.slice(0,1400),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText),buttons:[...document.querySelectorAll('button')].filter(button=>['Publish','Viewer'].includes(button.textContent?.trim())).map(button=>({text:button.textContent?.trim(),disabled:button.disabled}))};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'publish-current-dataset.json'),JSON.stringify({pageURL,before,after,requests,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,before,after,published,responses:responses.filter(response=>response.path.endsWith('/publish')||response.path.endsWith('/preview'))},null,2));
    assert(published,'Publish did not return');
    assert.equal(published.status,200);
  } else if (action === 'Inspect published Viewer') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Viewer')?.click();return true;`);
    await new Promise(resolve=>setTimeout(resolve,2500));
    const state=await browserEval(browser.cdp, `return {url:location.href,text:document.body.innerText.slice(0,5000),tables:[...document.querySelectorAll('[role="table"],table')].map(table=>({rows:table.querySelectorAll('[role="row"],tr').length,text:table.innerText.slice(0,900)})),buttons:[...document.querySelectorAll('button')].filter(button=>button.offsetParent!==null).map(button=>({text:button.innerText.trim(),disabled:button.disabled})).filter(button=>button.text)};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'published-viewer.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.status>=400)},null,2));
  } else if (action === 'Switch explorers') {
    const first=await browserEval(browser.cdp, `return {selected:document.querySelector('select[aria-label="Explorer"]')?.value,tables:[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim())};`);
    const target=explorerId==='cda-builder-full-qa-1790439585678'?'cda-builder-full-qa-1790440983382':'cda-builder-full-qa-1790439585678';
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Explorer"]');select.value=${JSON.stringify(target)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Explorer"]')?.value===${JSON.stringify(target)} && ${target==='cda-builder-full-qa-1790440983382' ? "Boolean(document.querySelector('button[aria-label=\"Rename Body structures QA\"]'))" : "document.querySelectorAll('button[aria-label^=\"Rename Body structures QA\"]').length===0 && document.body.innerText.includes('HOW THIS TABLE IS MADE')"}`, 30000);
    const switched=await browserEval(browser.cdp, `return {selected:document.querySelector('select[aria-label="Explorer"]')?.value,tables:[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim())};`);
    assert.notEqual(first.selected,switched.selected);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Explorer"]')?.value===${JSON.stringify(explorerId)}`, 30000);
    const restored=await browserEval(browser.cdp, `return {selected:document.querySelector('select[aria-label="Explorer"]')?.value,tables:[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim())};`);
    assert.deepEqual(restored,first);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'switch-explorers.json'),JSON.stringify({pageURL,first,switched,restored,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,first,switched,restored,responses:responses.filter(response=>response.path.endsWith('/builder'))},null,2));
  } else if (action === 'Review dataset after preview' || action === 'Review dataset') {
    if (action === 'Review dataset after preview') {
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview')?.click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
    }
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Review dataset').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('#dataset-review-panel'))`, 30000);
    const state=await browserEval(browser.cdp, `const panel=document.querySelector('#dataset-review-panel');return {text:panel?.innerText.slice(0,6500),buttons:[...panel.querySelectorAll('button')].map(button=>({text:button.innerText,label:button.getAttribute('aria-label'),disabled:button.disabled})),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText)};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,action==='Review dataset after preview'?'review-after-preview.json':'review-dataset.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.path.includes('/authoring/v2/'))},null,2));
  } else if (action === 'Inspect row settings') {
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]')?.open === true`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Configure rows')?.click();return true;`);
    await new Promise(resolve=>setTimeout(resolve,800));
    const state=await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-source-setup"]');return {text:panel?.innerText.slice(0,3500),controls:[...panel.querySelectorAll('button,select,input')].filter(element=>element.offsetParent!==null).slice(0,35).map(element=>({tag:element.tagName,label:element.getAttribute('aria-label'),text:element.innerText?.slice(0,90),disabled:element.disabled,value:element.value}))};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'row-settings.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.path.includes('row-definition')||response.path.includes('population'))},null,2));
  } else if (action === 'Inspect source setup') {
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]')?.open === true`, 30000);
    const state=await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-source-setup"]');return {text:panel?.innerText.slice(0,5000),controls:[...panel.querySelectorAll('button,select,input')].filter(element=>element.offsetParent!==null).map(element=>({tag:element.tagName,label:element.getAttribute('aria-label'),text:element.innerText?.slice(0,90),disabled:element.disabled,value:element.value})).filter(item=>item.label||item.text?.includes('row')||item.text?.includes('population'))};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'source-setup.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses},null,2));
  } else if (action === 'Inspect new table') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`);
    await new Promise(resolve=>setTimeout(resolve,1200));
    const state=await browserEval(browser.cdp, `return {text:document.body.innerText.slice(0,4200),choices:[...document.querySelectorAll('button[aria-label^="Choose "][aria-label$=" rows"]')].map(button=>({label:button.getAttribute('aria-label'),disabled:button.disabled}))};`);
    console.log(JSON.stringify({state,responses},null,2));
  } else if (action === 'Create BodyStructure table') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Choose BodyStructure rows"]:not(:disabled)'))`, 30000);
    const before=await browserEval(browser.cdp, `return document.body.innerText.slice(document.body.innerText.indexOf('Build another table'),document.body.innerText.indexOf('Build another table')+1000);`);
    await browserEval(browser.cdp, `document.querySelector('button[aria-label="Choose BodyStructure rows"]').click();return true;`);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('BodyStructure'))`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('BodyStructure'))`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith('BodyStructure')).click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE\\n\\nBodyStructure')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '26'`, 30000);
    const state=await browserEval(browser.cdp, `return {tables:[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim()),preview:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText};`);
    assert(state.tables.some(table=>table.endsWith('BodyStructure')));
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'new-table.json'),JSON.stringify({pageURL,before,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,before,state,responses:responses.filter(response=>response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Rename reorder undo table') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='▤\\nBodyStructure').click();window.prompt=()=> 'Body structures QA';return true;`);
    await browserEval(browser.cdp, `document.querySelector('button[aria-label="Rename BodyStructure"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Rename Body structures QA"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('button[aria-label="Move Body structures QA up"]').click();return true;`);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim())[0]?.endsWith('Body structures QA')`, 30000);
    const moved=await browserEval(browser.cdp, `return [...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim());`);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Undo').click();return true;`);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim())[1]?.endsWith('Body structures QA')`, 30000);
    const undone=await browserEval(browser.cdp, `return [...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim());`);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Rename Body structures QA"]'))`, 30000);
    const reloaded=await browserEval(browser.cdp, `return [...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim());`);
    assert.deepEqual(reloaded,undone);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'table-rename-reorder-undo.json'),JSON.stringify({pageURL,moved,undone,reloaded,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,moved,undone,reloaded,responses:responses.filter(response=>response.path.endsWith('/commands'))},null,2));
  } else if (action === 'Apply missing') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Keep rows:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Column"]:not(:disabled)'))`, 30000);
    const target=await browserEval(browser.cdp, `return [...document.querySelector('select[aria-label="Column"]').options].find(option=>option.textContent.includes('collection.bodySite'))?.value;`);
    assert(target);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Column"]');select.value=${JSON.stringify(target)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Column"]')?.value === ${JSON.stringify(target)}`, 30000);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Condition"]');select.value='MISSING';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 2`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 2`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '26'`, 30000);
    const state=await browserEval(browser.cdp, `return {history:document.body.innerText.slice(document.body.innerText.indexOf('HOW THIS TABLE IS MADE'),document.body.innerText.indexOf('DATASET WORKSPACE')),preview:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,1500)};`);
    assert(state.history.includes('is missing'));
    assert(!state.preview.includes('77d5efff-e239-57d9-88ac-bbb6394872fe'));
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'missing-applied.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Missing proposal') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Keep rows:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Column"]:not(:disabled)'))`, 30000);
    const target=await browserEval(browser.cdp, `return [...document.querySelector('select[aria-label="Column"]').options].find(option=>option.textContent.includes('collection.bodySite'))?.value;`);
    assert(target);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Column"]');select.value=${JSON.stringify(target)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Column"]')?.value === ${JSON.stringify(target)}`, 30000);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Condition"]');select.value='MISSING';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
    const state=await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),panel:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,2000),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText)};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'missing-proposal.json'),JSON.stringify({pageURL,target,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,target,state,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Remove saved filter') {
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    const proposed=await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,400);`);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label^="Keep rows:"]'))`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '26'`, 30000);
    const restored=await browserEval(browser.cdp, `return {steps:document.querySelectorAll('[data-testid^="construction-history-step-"]').length,rows:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,450)};`);
    assert.equal(restored.steps,0);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'filter-removed.json'),JSON.stringify({pageURL,proposed,restored,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,proposed,restored,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Edit saved filter') {
    const nextId='77d5efff-e239-57d9-88ac-bbb6394872fe';
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]:not(:disabled)'))`, 30000);
    const original=await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]').value;`);
    assert.equal(original,'b7cad184-db67-5542-a975-10fffa3e89e7');
    await browserEval(browser.cdp, `setInput('Value',${JSON.stringify(nextId)});return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    const proposed=await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText;`);
    assert(proposed.includes(nextId));
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes(${JSON.stringify(`Keep rows where Specimen ID equals “${nextId}”.`)})`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes(${JSON.stringify(`Keep rows where Specimen ID equals “${nextId}”.`)})`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '2'`, 30000);
    const saved=await browserEval(browser.cdp, `return document.querySelector('[data-testid="preview-table-scroll"]')?.innerText;`);
    assert(saved.includes(nextId));
    assert(!saved.includes(original));
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'filter-edited.json'),JSON.stringify({pageURL,original,nextId,proposed,saved,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,original,nextId,proposed,saved,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Apply known filter') {
    const knownId='b7cad184-db67-5542-a975-10fffa3e89e7';
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Keep rows:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]:not(:disabled)'))`, 30000);
    await browserEval(browser.cdp, `setInput('Value',${JSON.stringify(knownId)});return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    const proposed=await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText;`);
    assert(proposed.includes(knownId));
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '2'`, 30000);
    const saved=await browserEval(browser.cdp, `return {text:document.body.innerText.slice(0,1500),rows:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText};`);
    assert(saved.rows.includes(knownId));
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'filter-applied.json'),JSON.stringify({pageURL,knownId,proposed,saved,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,knownId,proposed,saved,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Remove expand') {
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 2`, 30000);
    await browserEval(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]')[1].click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    const removal=await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,600);`);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '5'`, 30000);
    const headers=await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText);`);
    assert(headers.includes('TYPE.CODING[].CODE'));
    assert(!headers.some(header=>header.includes('ITEM')));
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'expand-removed.json'),JSON.stringify({pageURL,removal,headers,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,removal,headers,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Inspect expand removal') {
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 2`, 30000);
    await browserEval(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]')[1].click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
    await new Promise(resolve=>setTimeout(resolve,5000));
    const state=await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),panel:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText),text:document.body.innerText.slice(-1800)};`);
    const proposalResponse=responses.find(response=>response.path.endsWith('/construction-proposals'));
    const body=proposalResponse ? await browser.cdp.send('Network.getResponseBody',{requestId:proposalResponse.requestId}) : undefined;
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'expand-remove-failure.json'),JSON.stringify({pageURL,state,responses,body},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses,body},null,2));
  } else if (action === 'Edit and remove expand') {
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 2`, 30000);
    await browserEval(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]')[1].click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Empty list policy"]'))`, 30000);
    const editor=await browserEval(browser.cdp, `return {policy:document.querySelector('select[aria-label="Empty list policy"]')?.value,field:document.querySelector('select[aria-label="Repeated field"]')?.selectedOptions[0]?.textContent};`);
    assert.equal(editor.policy,'PRESERVE_PARENT');
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    const removal=await browserEval(browser.cdp, `return {panel:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,550)};`);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '5'`, 30000);
    const restored=await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText);`);
    assert(restored.includes('TYPE.CODING[].CODE'));
    assert(!restored.some(header=>header.includes('ITEM')));
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'expand-edit-remove.json'),JSON.stringify({pageURL,editor,removal,restored,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,editor,removal,restored,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Apply expand') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Expand a repeated value')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value')).click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Empty list policy"]'))`, 30000);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Empty list policy"]');select.value='PRESERVE_PARENT';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('STEP 2')`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('STEP 2')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '5'`, 30000);
    const state=await browserEval(browser.cdp, `return {history:document.body.innerText.slice(document.body.innerText.indexOf('HOW THIS TABLE IS MADE'),document.body.innerText.indexOf('DATASET WORKSPACE')),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText),firstRow:document.querySelector('[data-testid="preview-table-scroll"] [role="row"][aria-rowindex="2"]')?.innerText};`);
    assert(state.headers.some(header=>header.includes('ITEM')));
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'expand-applied.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Expand proposal') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Expand a repeated value')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value')).click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Empty list policy"]'))`, 30000);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Empty list policy"]');select.value='PRESERVE_PARENT';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
    const state=await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),panel:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,2500),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText)};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'expand-proposal.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Inspect expand') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Expand a repeated value')`, 30000);
    const option=await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value'));return {disabled:button?.disabled,text:button?.innerText};`);
    if (!option.disabled) await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value')).click();return true;`);
    await new Promise(resolve=>setTimeout(resolve,1000));
    const state=await browserEval(browser.cdp, `return {text:document.body.innerText.slice(document.body.innerText.indexOf('PROPOSED CHANGE'),document.body.innerText.indexOf('Source and column setup')),controls:[...document.querySelectorAll('select,input,button')].filter(element=>element.offsetParent!==null).map(element=>({tag:element.tagName,label:element.getAttribute('aria-label')??element.innerText.slice(0,60),disabled:element.disabled??false,value:element.value??undefined})).filter(item=>item.label?.includes('Expand')||item.label?.includes('empty')||item.label?.includes('Value')||item.label?.includes('Apply'))};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'expand-editor.json'),JSON.stringify({pageURL,option,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,option,state,responses},null,2));
  } else if (action === 'Apply and remove group') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='▤\\nSpecimen')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE\\n\\nSpecimen')`, 30000);
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Summarize into groups')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Summarize into groups')).click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    const proposal=await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText;`);
    assert(proposal.includes('742505'));
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='▤\\nSpecimen')?.click();return true;`);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '2'`, 30000);
    const saved=await browserEval(browser.cdp, `return document.querySelector('[data-testid="preview-table-scroll"]')?.innerText;`);
    assert(saved.includes('742505'));
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='▤\\nSpecimen')?.click();return true;`);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '26'`, 30000);
    const restored=await browserEval(browser.cdp, `return document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,300);`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'group-applied-removed.json'),JSON.stringify({pageURL,proposal,saved,restored,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,proposal,saved,restored,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (['Inspect pivot editor','Inspect unpivot editor','Inspect related expand editor'].includes(action)) {
    const option = {
      'Inspect pivot editor':'Turn categories into columns',
      'Inspect unpivot editor':'Turn columns into rows',
      'Inspect related expand editor':'Expand related records',
    }[action];
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find(button=>button.innerText.startsWith(${JSON.stringify(option)})))`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith(${JSON.stringify(option)})).click();return true;`);
    await new Promise(resolve=>setTimeout(resolve,1000));
    const state=await browserEval(browser.cdp, `const editor=document.querySelector('[aria-label="Reshape editor"]')??document.querySelector('[data-testid="construction-operation-editor"]');return {text:document.body.innerText.slice(document.body.innerText.indexOf('PROPOSED CHANGE'),document.body.innerText.indexOf('Source and column setup')),controls:[...document.querySelectorAll('select,input,button')].filter(element=>element.offsetParent!==null).slice(-90).map(element=>({tag:element.tagName,label:element.getAttribute('aria-label')??element.innerText?.slice(0,100),disabled:element.disabled??false,value:element.value,options:element.tagName==='SELECT'?[...element.options].slice(0,15).map(option=>option.textContent):undefined})),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText)};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,`${action.toLowerCase().replaceAll(' ','-')}.json`),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.path.includes('construction')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Inspect reshape options') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Turn categories into columns')`, 30000);
    const state=await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-reshape-editor"]');return {text:panel?.innerText.slice(0,4500),options:[...panel.querySelectorAll('button')].map(button=>({text:button.innerText,disabled:button.disabled,title:button.getAttribute('title')}))};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'reshape-options.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.path.endsWith('/construction-capabilities'))},null,2));
  } else if (action === 'Group proposal') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Summarize into groups')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Summarize into groups')).click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'`, 30000);
    const state=await browserEval(browser.cdp, `return {panel:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText,applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'group-proposal.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.path.endsWith('/construction-proposals')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Inspect group') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Summarize into groups')`, 30000);
    const groupOption=await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Summarize into groups'));return {exists:!!button,disabled:button?.disabled,text:button?.innerText};`);
    if (groupOption.exists && !groupOption.disabled) await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Summarize into groups')).click();return true;`);
    await new Promise(resolve=>setTimeout(resolve,1000));
    const state=await browserEval(browser.cdp, `return {text:document.body.innerText.slice(document.body.innerText.indexOf('PROPOSED CHANGE'),document.body.innerText.indexOf('Source and column setup')),controls:[...document.querySelectorAll('select,input,button')].filter(element=>element.offsetParent!==null).map(element=>({tag:element.tagName,label:element.getAttribute('aria-label')??element.innerText.slice(0,60),disabled:element.disabled??false})).filter(item=>item.label?.includes('Group')||item.label?.includes('Summary')||item.label?.includes('Apply')||item.label?.includes('Count'))};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'group-editor.json'),JSON.stringify({pageURL,groupOption,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,groupOption,state,responses},null,2));
  } else if (action === 'Inspect Observation concepts') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Add columns source"]'))`, 30000);
    const sources = await browserEval(browser.cdp, `return [...document.querySelector('select[aria-label="Add columns source"]').options].map(option=>({label:option.textContent,value:option.value}));`);
    const observation = sources.find(source=>source.label?.startsWith('Observation'));
    assert(observation);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Add columns source"]');select.value=${JSON.stringify(observation.value)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Concepts on Observation')`, 30000);
    await waitForBrowser(browser.cdp, `!document.body.innerText.includes('Loading concepts…')`, 30000);
    const state=await browserEval(browser.cdp, `return {text:document.querySelector('[aria-label="Add columns editor"]')?.innerText.slice(-7000)??document.body.innerText.slice(-7000),concepts:[...document.querySelectorAll('input[aria-label^="Select "]')].filter(element=>element.closest('section')?.innerText.includes('Concepts on Observation')).slice(0,30).map(element=>({label:element.getAttribute('aria-label'),disabled:element.disabled}))};`);
    const semanticResponses=[];
    for (const response of responses.filter(response=>response.path.endsWith('/semantic-inventory'))) {
      try { semanticResponses.push({response,body:JSON.parse((await browser.cdp.send('Network.getResponseBody',{requestId:response.requestId})).body)}); } catch { /* Preserve the DOM evidence when CDP has released an earlier response body. */ }
    }
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'observation-concepts.json'),JSON.stringify({pageURL,sources,state,responses,semanticResponses},null,2));
    console.log(JSON.stringify({evidenceDirectory,source:observation,state,responses:responses.filter(response=>response.path.endsWith('/semantic-inventory')),semanticResponseShape:semanticResponses.map(item=>({requestId:item.response.requestId,keys:Object.keys(item.body)}))},null,2));
  } else if (action === 'Apply Observation concept' || action === 'Add Observation concept') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Add columns source"]'))`, 30000);
    const source=await browserEval(browser.cdp, `return [...document.querySelector('select[aria-label="Add columns source"]').options].find(option=>option.textContent.startsWith('Observation'))?.value;`);
    assert(source);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Add columns source"]');select.value=${JSON.stringify(source)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Select primary_disease_type"]:not(:disabled)'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('input[aria-label="Select primary_disease_type"]').click();return true;`);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.textContent?.trim()==='Add 1 selected feature'&&!button.disabled)`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Add 1 selected feature').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 30000);
    if (action === 'Apply Observation concept') {
      await browserEval(browser.cdp, `document.querySelector('[role="dialog"] input[aria-label="primary_disease_type route 2: component[].valueString"]').click();return true;`);
      await waitForBrowser(browser.cdp, `!document.querySelector('[role="dialog"] button:last-child')?.disabled`, 30000);
      await browserEval(browser.cdp, `document.querySelector('[role="dialog"] button:last-child').click();return true;`);
      await new Promise(resolve=>setTimeout(resolve,1500));
    }
    const state=await browserEval(browser.cdp, `return {dialog:[...document.querySelectorAll('[role="dialog"]')].map(element=>({text:element.innerText.slice(0,6000),controls:[...element.querySelectorAll('button,select,input')].map(control=>({tag:control.tagName,label:control.getAttribute('aria-label')??control.innerText?.slice(0,90),disabled:control.disabled,value:control.value}))})),editor:document.querySelector('[aria-label="Add columns editor"]')?.innerText.slice(-1200),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText)};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,action === 'Apply Observation concept'?'apply-observation-concept.json':'add-observation-concept.json'),JSON.stringify({pageURL,source,state,requests,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,source,state,responses:responses.filter(response=>response.path.endsWith('/commands')||response.path.endsWith('/construction-choices')||response.path.endsWith('/construction-proposals'))},null,2));
  } else if (action === 'Inspect Observation concept selection') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Add columns source"]'))`, 30000);
    const source=await browserEval(browser.cdp, `return [...document.querySelector('select[aria-label="Add columns source"]').options].find(option=>option.textContent.startsWith('Observation'))?.value;`);
    assert(source);
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Add columns source"]');select.value=${JSON.stringify(source)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Select primary_disease_type"]:not(:disabled)'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('input[aria-label="Select primary_disease_type"]').click();return true;`);
    const state=await browserEval(browser.cdp, `const editor=document.querySelector('[aria-label="Add columns editor"]');return {text:editor?.innerText.slice(-4500),buttons:[...editor.querySelectorAll('button')].filter(button=>button.offsetParent!==null).map(button=>({text:button.innerText.slice(0,90),disabled:button.disabled})).slice(-25),selected:document.querySelector('input[aria-label="Select primary_disease_type"]')?.checked};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'observation-concept-selection.json'),JSON.stringify({pageURL,source,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,source,state,responses:responses.filter(response=>response.path.endsWith('/semantic-inventory')||response.path.endsWith('/construction-choices'))},null,2));
  } else if (action === 'Verify duplicate and delete') {
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('Specimen copy'))`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith('Specimen copy')).click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE\\n\\nSpecimen copy')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '5'`, 30000);
    const copied = await browserEval(browser.cdp, `return {count:document.querySelector('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-colcount'),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText)};`);
    await browserEval(browser.cdp, `document.querySelector('button[aria-label="Delete table"]').click();return true;`);
    await waitForBrowser(browser.cdp, `![...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('Specimen copy'))`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('Specimen'))`, 30000);
    const remaining = await browserEval(browser.cdp, `return [...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim());`);
    assert.deepEqual(remaining,['▤\nSpecimen']);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'duplicate-delete.json'),JSON.stringify({pageURL,copied,remaining,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,copied,remaining,responses:responses.filter(response=>response.path.endsWith('/commands'))},null,2));
  } else if (action === 'Inspect tables') {
    const state=await browserEval(browser.cdp, `return {inputs:[...document.querySelectorAll('input')].map(input=>({label:input.getAttribute('aria-label'),value:input.value})),buttons:[...document.querySelectorAll('button')].map(button=>({label:button.getAttribute('aria-label'),text:button.innerText.slice(0,50)})).filter(item=>item.text.includes('Specimen')||item.text.includes('copy')||item.text.includes('Delete')||item.label?.includes('table')),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText),text:document.body.innerText.slice(0,1200)};`);
    console.log(JSON.stringify({state,responses},null,2));
  } else if (action === 'Duplicate table') {
    assert(!await browserEval(browser.cdp, `return Boolean(document.querySelector('input[aria-label="Table name for Specimen copy"]'));`));
    await browserEval(browser.cdp, `document.querySelector('button[aria-label="Duplicate table"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Table name for Specimen copy"]'))`, 30000);
    await navigate(browser.cdp, pageURL);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Table name for Specimen copy"]'))`, 30000);
    const duplicate = await browserEval(browser.cdp, `return [...document.querySelectorAll('input[aria-label^="Table name for"]')].map(input=>input.value);`);
    assert.deepEqual(duplicate,['Specimen','Specimen copy']);
    assert.equal(responses.filter(response=>response.path.endsWith('/commands') && response.status===200).length,1);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'duplicate-table.json'),JSON.stringify({pageURL,duplicate,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,duplicate,responses:responses.filter(response=>response.path.endsWith('/commands'))},null,2));
  } else if (action === 'Preview limits') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '26'`, 30000);
    const limits = [];
    for (const limit of [50, 100, 500, 1000]) {
      const started = Date.now();
      await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Preview row limit"]');select.value=${JSON.stringify(String(limit))};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
      await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(limit+1))}`, 120000);
      limits.push({limit,durationMs:Date.now()-started});
    }
    assert(responses.filter(response=>response.path.endsWith('/preview') && response.status===200).length >= 5);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'preview-limits.json'),JSON.stringify({pageURL,limits,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,limits,responses:responses.filter(response=>response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Toggle filter flag') {
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]').open=true;return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Use Specimen ID as filter"]:not(:disabled)'))`, 30000);
    const initial = await browserEval(browser.cdp, `return document.querySelector('input[aria-label="Use Specimen ID as filter"]').checked;`);
    await browserEval(browser.cdp, `document.querySelector('input[aria-label="Use Specimen ID as filter"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Use Specimen ID as filter"]')?.checked === ${!initial}`, 30000);
    await navigate(browser.cdp, pageURL);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Use Specimen ID as filter"]'))`, 30000);
    const changed = await browserEval(browser.cdp, `return document.querySelector('input[aria-label="Use Specimen ID as filter"]').checked;`);
    assert.equal(changed, !initial);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]').open=true;document.querySelector('input[aria-label="Use Specimen ID as filter"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Use Specimen ID as filter"]')?.checked === ${initial}`, 30000);
    await navigate(browser.cdp, pageURL);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Use Specimen ID as filter"]'))`, 30000);
    const restored = await browserEval(browser.cdp, `return document.querySelector('input[aria-label="Use Specimen ID as filter"]').checked;`);
    assert.equal(restored, initial);
    assert.equal(responses.filter(response => response.path.endsWith('/commands') && response.status === 200).length, 2);
    await mkdir(evidenceDirectory, { recursive: true });
    await writeFile(join(evidenceDirectory, 'filter-flag.json'), JSON.stringify({pageURL,initial,changed,restored,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,initial,changed,restored,responses:responses.filter(response=>response.path.endsWith('/commands'))},null,2));
  } else if (action === 'Inspect filters') {
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]').open=true;return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label^="Use "]'))`, 30000);
    const state = await browserEval(browser.cdp, `return [...document.querySelectorAll('input[aria-label^="Use "]')].map(element=>({label:element.getAttribute('aria-label'),checked:element.checked,disabled:element.disabled,description:element.closest('[role="row"]')?.innerText}));`);
    await mkdir(evidenceDirectory, { recursive: true });
    await writeFile(join(evidenceDirectory, 'filters.json'), JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses},null,2));
  } else if (action === 'Toggle source visibility') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Preview')?.click(); return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 120000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Columns')?.click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="list"][aria-label="Table columns"]'))`, 30000);
    const wasVisible = await browserEval(browser.cdp, `return [...document.querySelectorAll('[role="list"][aria-label="Table columns"] [role="listitem"]')].find(element=>element.innerText.includes('subject.reference')).querySelector('input[type="checkbox"]').checked;`);
    if (!wasVisible) {
      await browserEval(browser.cdp, `const item=[...document.querySelectorAll('[role="list"][aria-label="Table columns"] [role="listitem"]')].find(element=>element.innerText.includes('subject.reference'));item.querySelector('input[type="checkbox"]').click();return true;`);
      await waitForBrowser(browser.cdp, `document.body.innerText.includes('Choose a row resource and at least one visible column, then preview.')`, 30000);
      await navigate(browser.cdp, pageURL);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label^="Keep rows:"]'))`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Preview')?.click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Columns')?.click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="list"][aria-label="Table columns"]'))`, 30000);
    }
    await browserEval(browser.cdp, `const item=[...document.querySelectorAll('[role="list"][aria-label="Table columns"] [role="listitem"]')].find(element=>element.innerText.includes('subject.reference'));item.querySelector('input[type="checkbox"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Choose a row resource and at least one visible column, then preview.')`, 30000);
    assert(responses.some(response => response.path.endsWith('/commands') && response.status === 200));
    await navigate(browser.cdp, pageURL);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label^="Keep rows:"]'))`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Preview')?.click(); return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '4'`, 30000);
    const hidden = await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText),count:document.querySelector('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-colcount')};`);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Columns')?.click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="list"][aria-label="Table columns"]'))`, 30000);
    await browserEval(browser.cdp, `const item=[...document.querySelectorAll('[role="list"][aria-label="Table columns"] [role="listitem"]')].find(element=>element.innerText.includes('subject.reference'));item.querySelector('input[type="checkbox"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Choose a row resource and at least one visible column, then preview.')`, 30000);
    await navigate(browser.cdp, pageURL);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label^="Keep rows:"]'))`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Preview')?.click(); return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '5'`, 30000);
    const restored = await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText),count:document.querySelector('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-colcount')};`);
    assert(!hidden.headers.includes('SUBJECT.REFERENCE'));
    assert(restored.headers.includes('SUBJECT.REFERENCE'));
    await mkdir(evidenceDirectory, { recursive: true });
    await writeFile(join(evidenceDirectory, 'source-visibility.json'), JSON.stringify({pageURL,hidden,restored,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,hidden,restored,responses},null,2));
  } else if (action === 'Verify constructed column rename') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
    const original=await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].at(-1)?.innerText;`);
    assert(original);
    const originalLabel=original==='ID'?'id':original==='PATIENT ID QA'?'Patient ID QA':original;
    const nextLabel=original==='ID'?'Patient ID QA':'Patient ID QA 2';
    const restoredLabel=original==='PATIENT ID QA'?'id':originalLabel;
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Display name for construction output ${originalLabel}"]`)}))`, 30000);
    await browserEval(browser.cdp, `setInput(${JSON.stringify(`Display name for construction output ${originalLabel}`)},${JSON.stringify(nextLabel)});inputByLabel(${JSON.stringify(`Display name for construction output ${originalLabel}`)}).focus();return true;`);
    await browser.cdp.send('Input.dispatchKeyEvent',{type:'keyDown',key:'Enter',code:'Enter'});
    await browser.cdp.send('Input.dispatchKeyEvent',{type:'keyUp',key:'Enter',code:'Enter'});
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(element=>element.innerText===${JSON.stringify(nextLabel.toUpperCase())})`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()==='Preview')?.click();return true;`);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(element=>element.innerText===${JSON.stringify(nextLabel.toUpperCase())})`, 30000);
    const renamed=await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText);`);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Display name for construction output ${nextLabel}"]`)}))`, 30000);
    await browserEval(browser.cdp, `setInput(${JSON.stringify(`Display name for construction output ${nextLabel}`)},${JSON.stringify(restoredLabel)});inputByLabel(${JSON.stringify(`Display name for construction output ${nextLabel}`)}).focus();return true;`);
    await browser.cdp.send('Input.dispatchKeyEvent',{type:'keyDown',key:'Enter',code:'Enter'});
    await browser.cdp.send('Input.dispatchKeyEvent',{type:'keyUp',key:'Enter',code:'Enter'});
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(element=>element.innerText===${JSON.stringify(restoredLabel.toUpperCase())})`, 30000);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'constructed-column-rename.json'),JSON.stringify({pageURL,renamed,requests,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,renamed,responses:responses.filter(response=>response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Verify constructed column presentation') {
    const commandCount = () => responses.filter(response=>response.path.endsWith('/commands')).length;
    const waitForCommand = async (previousCount) => {
      const deadline = Date.now()+30000;
      while (commandCount()<=previousCount && Date.now()<deadline) await new Promise(resolve=>setTimeout(resolve,50));
      assert(commandCount()>previousCount,'presentation command did not complete');
      assert.equal(responses.filter(response=>response.path.endsWith('/commands')).at(-1).status,200);
    };
    const preview = async () => {
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Preview')?.click(); return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
    };
    const headers = () => browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText);`);
    await preview();
    const before = await headers();
    assert(before.includes('ID'));
    const beforeHideCommands = commandCount();
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Columns')?.click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="list"][aria-label="Table columns"]'))`, 30000);
    await browserEval(browser.cdp, `const item=[...document.querySelectorAll('[role="list"][aria-label="Table columns"] [role="listitem"]')].find(element=>element.innerText.trim().endsWith('id'));item.querySelector('input[type="checkbox"]').click();return true;`);
    await waitForCommand(beforeHideCommands);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')===${JSON.stringify(String(before.length-1))}`, 30000);
    const hiddenBeforeRefresh = await headers();
    await navigate(browser.cdp, pageURL);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
    await preview();
    const hiddenAfterReload = await headers();
    assert(!hiddenAfterReload.includes('ID'));
    const beforeRestoreCommands = commandCount();
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Columns')?.click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="list"][aria-label="Table columns"]'))`, 30000);
    await browserEval(browser.cdp, `const item=[...document.querySelectorAll('[role="list"][aria-label="Table columns"] [role="listitem"]')].find(element=>element.innerText.trim().endsWith('id'));item.querySelector('input[type="checkbox"]').click();return true;`);
    await waitForCommand(beforeRestoreCommands);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')===${JSON.stringify(String(before.length))}`, 30000);
    const restoredBeforeRefresh = await headers();
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
    await preview();
    const restored = await headers();
    assert.deepEqual(restored, before);
    await mkdir(evidenceDirectory, {recursive:true});
    await writeFile(join(evidenceDirectory,'constructed-column-presentation.json'),JSON.stringify({pageURL,before,hiddenBeforeRefresh,hiddenAfterReload,restoredBeforeRefresh,restored,requests,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,before,hiddenBeforeRefresh,hiddenAfterReload,restoredBeforeRefresh,restored,responses:responses.filter(response=>response.path.endsWith('/commands')||response.path.endsWith('/preview'))},null,2));
  } else if (action === 'Inspect columns') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Preview')?.click(); return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 120000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Columns')?.click(); return true;`);
    const state = await browserEval(browser.cdp, `const list=document.querySelector('[role="list"][aria-label="Table columns"]'); return {items:[...list.querySelectorAll('[role="listitem"]')].map(item=>({text:item.innerText,checkbox:item.querySelector('input[type="checkbox"]')?.checked,disabled:item.querySelector('input[type="checkbox"]')?.disabled})),visibleHeaders:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText)};`);
    await mkdir(evidenceDirectory, { recursive: true });
    await writeFile(join(evidenceDirectory, 'columns.json'), JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses},null,2));
  } else if (action === 'Preview') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(element => element.textContent?.trim() === 'Preview')?.click(); return true;`);
    await waitForBrowser(browser.cdp, `/Preview rows\\s+25/.test(document.body.innerText)`, 120000);
    const state = await browserEval(browser.cdp, `const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');const scroll=document.querySelector('[data-testid="preview-table-scroll"]');return {text:document.body.innerText.slice(0, 6000),ariaColumnCount:table?.getAttribute('aria-colcount'),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(element=>element.innerText),scrollWidth:scroll?.scrollWidth,clientWidth:scroll?.clientWidth,alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText)};`);
    const latestPreview=responses.filter(response=>response.path.endsWith('/preview')).at(-1);
    let rawPreview;
    if (latestPreview) {
      const body=await browser.cdp.send('Network.getResponseBody',{requestId:latestPreview.requestId});
      const parsed=JSON.parse(body.body);
      rawPreview={columns:parsed.columns,firstRow:parsed.rows?.[0],secondRow:parsed.rows?.[1]};
    }
    await mkdir(evidenceDirectory, { recursive: true });
    await writeFile(join(evidenceDirectory, 'preview.json'), JSON.stringify({pageURL,state,rawPreview,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,rawPreview,responses},null,2));
    process.exitCode = responses.some(response => response.status >= 400) ? 1 : 0;
  } else {
  await browserEval(browser.cdp, `document.querySelector(${JSON.stringify(`button[aria-label^="${action}:"]`)})?.click(); return true;`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('PROPOSED CHANGE')`, 30000);
  await waitForBrowser(browser.cdp, `!document.body.innerText.includes('Loading the current table columns and operation support')`, 60000);
  await new Promise(resolve => setTimeout(resolve, 1500));
  const state = await browserEval(browser.cdp, `return {
    text: document.body.innerText.slice(0, 12000),
    firstField: (() => { const element=document.querySelector('input[aria-label="Select Specimen.subject.reference"]'); return element ? {disabled:element.disabled,describedBy:element.getAttribute('aria-describedby'),description:element.getAttribute('aria-describedby')?.split(' ').map(id=>document.getElementById(id)?.innerText)} : null; })(),
    controls: [...document.querySelectorAll('input, select, button')].filter(element => element.offsetParent !== null).map(element => ({tag: element.tagName, type: element.type ?? null, label: element.getAttribute('aria-label') ?? element.textContent?.trim().slice(0, 100), disabled: element.disabled ?? false, checked: element.checked ?? null})).slice(0, 160)
  };`);
  assert(state.text.includes(`PROPOSED CHANGE\n\n${action}`));
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, `${action.toLowerCase().replaceAll(' ', '-')}-baseline.json`), JSON.stringify({ pageURL, state, responses }, null, 2));
  console.log(JSON.stringify({ evidenceDirectory, editorText: state.text.slice(state.text.indexOf('PROPOSED CHANGE'), state.text.indexOf('Source and column setup')), firstField:state.firstField, controls: state.controls.filter(control => control.label?.includes('filter') || control.label?.includes('condition') || control.label?.includes('column') || control.label?.includes('Apply')).slice(0, 60), responses }, null, 2));
  }
} finally {
  await browser.close();
}
