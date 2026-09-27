import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const action = process.argv[2] ?? 'Keep rows';
const explorerId = process.argv[3] ?? 'cda-builder-full-qa-1790439585678';
const pageURL = `http://127.0.0.1:30002/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const browser = await launchBrowser('/private/tmp');
if (action === 'Verify related source chooser' || action === 'Inspect selected Patient route') {
  await browser.cdp.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
}
const responses = [];
const requests = [];
const proposalRequests = [];
const chooseRelatedSource = async (resourceType) => {
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))`, 30000);
  const sources = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="construction-add-columns-source-option"]')].map(button=>({label:button.getAttribute('aria-label'),key:button.getAttribute('data-source-key'),kind:button.getAttribute('data-source-kind'),selected:button.getAttribute('aria-pressed')==='true',visible:button.offsetParent!==null}));`);
  const source = sources.find((item) => item.kind === 'RELATED' && item.label?.startsWith(`${resourceType},`));
  assert(source, `${resourceType} is missing from the visible related source list`);
  await browserEval(browser.cdp, `document.querySelector('[data-source-key=${JSON.stringify(source.key)}]').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-source-key=${JSON.stringify(source.key)}]')?.getAttribute('aria-pressed')==='true'`, 30000);
  return { source, sources };
};
browser.cdp.on('Network.requestWillBeSent', (event) => {
  if (event.request.url.includes('/authoring/v2/commands')) requests.push({ requestId: event.requestId, postData: event.request.postData });
  if (event.request.url.includes('/authoring/v2/construction-proposals')) proposalRequests.push({ requestId: event.requestId, postData: event.request.postData });
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
  } else if (action === 'Edit and remove Patient related column') {
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Output column label"]:not(:disabled)'))`, 30000);
    const originalLabel=await browserEval(browser.cdp, `return document.querySelector('input[aria-label="Output column label"]').value;`);
    await browserEval(browser.cdp, `const input=document.querySelector('input[aria-label="Output column label"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'Patient ID QA');input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 60000);
    const editedProposal=await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,600),disabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
    assert.equal(editedProposal.disabled,false);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(cell=>cell.innerText==='PATIENT ID QA')`, 30000);
    const edited=await browserEval(browser.cdp, `return {columns:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1,4).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
    for (const row of edited.rows) assert.equal(row.at(-1),row[1]?.replace(/^Patient\//,''));
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 60000);
    const removalProposal=await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,600),disabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
    assert.equal(removalProposal.disabled,false);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`, 30000);
    await navigate(browser.cdp,pageURL);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE') && document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0 && [...document.querySelectorAll('button')].some(button=>button.textContent?.trim()==='Preview')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='3'`, 30000);
    const restored=await browserEval(browser.cdp, `return {columns:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1,4).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
    assert.equal(restored.rows.length,3);
    assert.equal(restored.rows[0]?.[0],edited.rows[0]?.[0]);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'patient-related-edited-removed.json'),JSON.stringify({pageURL,originalLabel,editedProposal,edited,removalProposal,restored,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,originalLabel,editedProposal,edited,removalProposal,restored,responses:responses.filter(response=>response.status>=400)},null,2));
  } else if (action === 'Inspect saved related step' || action === 'Inspect related edit') {
    await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
    if (action === 'Inspect related edit') {
      await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
      await new Promise(resolve=>setTimeout(resolve,1000));
    }
    const state=await browserEval(browser.cdp, `return {text:document.body.innerText.slice(0,4500),controls:[...document.querySelectorAll('[data-testid^="construction-edit-step-"],[data-testid^="construction-remove-step-"],button[aria-label^="Edit"],button[aria-label^="Remove"]')].filter(button=>button.offsetParent!==null).map(button=>({testId:button.getAttribute('data-testid'),label:button.getAttribute('aria-label'),text:button.innerText,disabled:button.disabled}))};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,action === 'Inspect related edit' ? 'related-edit.json' : 'saved-related-step.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.status>=400)},null,2));
  } else if (action === 'Inspect published Viewer') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Viewer')?.click();return true;`);
    await new Promise(resolve=>setTimeout(resolve,2500));
    const state=await browserEval(browser.cdp, `return {url:location.href,text:document.body.innerText.slice(0,5000),tables:[...document.querySelectorAll('[role="table"],table')].map(table=>({rows:table.querySelectorAll('[role="row"],tr').length,text:table.innerText.slice(0,900)})),buttons:[...document.querySelectorAll('button')].filter(button=>button.offsetParent!==null).map(button=>({text:button.innerText.trim(),disabled:button.disabled})).filter(button=>button.text)};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'published-viewer.json'),JSON.stringify({pageURL,state,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.status>=400)},null,2));
  } else if (action === 'Inspect export') {
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Viewer')?.click();return true;`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Download dataset')`, 30000);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Download dataset')?.click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]')) || document.body.innerText.includes('Dataset download failed')`, 30000);
    const state=await browserEval(browser.cdp, `return {text:document.body.innerText.slice(0,2500),dialogs:[...document.querySelectorAll('[role="dialog"]')].map(dialog=>dialog.innerText),buttons:[...document.querySelectorAll('button')].filter(button=>button.offsetParent!==null).map(button=>({text:button.innerText.trim(),disabled:button.disabled})).filter(button=>button.text).slice(0,25)};`);
    const download=await browserEval(browser.cdp, `const link=document.querySelector('[role="dialog"] a[download]');if(!link)return null;const response=await fetch(link.href);const bytes=new Uint8Array(await response.arrayBuffer());return {status:response.status,contentType:response.headers.get('content-type'),bytes:bytes.length,signature:[...bytes.slice(0,4)]};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'export-options.json'),JSON.stringify({pageURL,state,download,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,dialogs:state.dialogs,download,responses:responses.filter(response=>response.status>=400)},null,2));
    assert.equal(download?.status,200);
    assert.deepEqual(download?.signature,[80,75,3,4]);
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
  } else if (action === 'Inspect bounded Patient row choices' || action === 'Inspect bounded BodyStructure row choices' || action === 'Verify bounded BodyStructure row definition' || action === 'Inspect bounded BodyStructure fields' || action === 'Inspect bounded BodyStructure field choice' || action === 'Verify bounded BodyStructure group' || action === 'Verify bounded BodyStructure expansion' || action === 'Inspect bounded Observation component cases') {
    const resourceType = action.includes('BodyStructure') ? 'BodyStructure' : action.includes('Observation') ? 'Observation' : 'Patient';
    const tableName = `${resourceType} row choice QA ${Date.now()}`;
    const journeyStarted = Date.now();
    let created = false;
    try {
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label=${JSON.stringify(`Choose ${resourceType} rows`)}]:not(:disabled)'))`, 30000);
      await browserEval(browser.cdp, `const input=document.querySelector('#first-table-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(tableName)});input.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('button[aria-label=${JSON.stringify(`Choose ${resourceType} rows`)}]').click();return true;`);
      created = true;
      await waitForBrowser(browser.cdp, `document.body.innerText.includes(${JSON.stringify(tableName)})`, 30000);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label^="Filter rows:"]:not(:disabled)')) && !document.body.innerText.includes('Loading the preview…')`, 120000);
      await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
      await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]')?.open===true`, 30000);
      await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Configure rows');if(!button)throw new Error('Configure rows missing');button.click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="New row shape"]'))`, 30000);
      const state = await browserEval(browser.cdp, `return {dialog:document.querySelector('[role="dialog"]')?.innerText,options:[...document.querySelector('select[aria-label="New row shape"]').options].map(option=>({value:option.value,label:option.textContent})),tables:[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim())};`);
      state.timingsMs = { rowChoices: Date.now() - journeyStarted };
      if (action === 'Inspect bounded Observation component cases') {
        await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Cancel').click();return true;`);
        await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Filter rows:"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Column"]:not(:disabled)'))`, 30000);
        state.filterChoices = await browserEval(browser.cdp, `return {columns:[...document.querySelector('select[aria-label="Column"]').options].map(option=>({label:option.textContent,value:option.value})),conditions:[...document.querySelector('select[aria-label="Condition"]').options].map(option=>({label:option.textContent,value:option.value}))};`);
        const idColumn = state.filterChoices.columns.find(option=>option.label.includes('Observation ID'));
        assert(idColumn, 'Observation ID is missing from Filter rows');
        const target = '485e2567-b566-56f3-b5bd-5f025f37cd95';
        await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Column"]');select.value=${JSON.stringify(idColumn.value)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Value"]:not(:disabled)'))`, 30000);
        await browserEval(browser.cdp, `const input=document.querySelector('input[aria-label="Value"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(target)});input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
        await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
        state.filterProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,1000),preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,1200),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
        await mkdir(evidenceDirectory,{recursive:true});
        await writeFile(join(evidenceDirectory,'bounded-observation-components.json'),JSON.stringify({pageURL,state,responses},null,2));
        assert(state.filterProposal.preview.includes(target) && !state.filterProposal.applyDisabled);
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Value"]:not(:disabled)'))`, 30000);
        const zeroTarget = '3ea21633-23e1-599b-91fc-b7666953ea26';
        await browserEval(browser.cdp, `const input=document.querySelector('input[aria-label="Value"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(zeroTarget)});input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
        await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')) && document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.includes(${JSON.stringify(zeroTarget)})`, 30000);
        state.zeroProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,800),preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,1200),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
        assert.equal(state.zeroProposal.applyDisabled, false);
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1 && Boolean(document.querySelector('button[aria-label^="Add columns:"]:not(:disabled)'))`, 30000);
        await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
        await new Promise(resolve=>setTimeout(resolve,1000));
        state.componentFields = await browserEval(browser.cdp, `return {editor:document.querySelector('[aria-label="Add columns editor"]')?.innerText.slice(0,3800),operation:document.querySelector('[aria-label="Proposed change"]')?.innerText.slice(0,1600),body:document.body.innerText.slice(0,2800),buttons:[...document.querySelectorAll('button[aria-label^="Add columns:"]')].map(button=>({disabled:button.disabled,aria:button.getAttribute('aria-label')})),fields:[...document.querySelectorAll('[aria-label="Add columns editor"] input[aria-label^="Select Observation."]')].map(input=>({label:input.getAttribute('aria-label'),disabled:input.disabled})),alerts:[...document.querySelectorAll('[role="alert"]')].map(item=>item.innerText)};`);
        assert(state.componentFields.fields.some(field=>field.label==='Select Observation.component[].code.coding[].code'&&!field.disabled));
        await browserEval(browser.cdp, `document.querySelector('[aria-label="Add columns editor"] input[aria-label="Select Observation.component[].code.coding[].code"]').click();[...document.querySelectorAll('[aria-label="Add columns editor"] button')].find(button=>button.textContent?.trim()==='Add 1 selected feature').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 30000);
        state.componentFieldChoice = await browserEval(browser.cdp, `return {dialog:document.querySelector('[role="dialog"]')?.innerText.slice(0,2200),radios:[...document.querySelectorAll('[role="dialog"] input[type="radio"]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked}))};`);
        await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.innerText.trim()==='Add 1 column').click();return true;`);
        await waitForBrowser(browser.cdp, `document.body.innerText.includes('2 configured')`, 30000);
        await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value')))`, 30000);
        state.expandChoice = await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value'));return {disabled:button?.disabled,text:button?.innerText};`);
        if (!state.expandChoice.disabled) {
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value')).click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Empty list policy"]'))`, 30000);
          state.expandEditor = await browserEval(browser.cdp, `return {fields:[...document.querySelector('select[aria-label="Repeated field"]').options].map(option=>({label:option.textContent,selected:option.selected})),text:document.querySelector('[data-testid="construction-reshape-expand"]')?.innerText.slice(0,1200)};`);
          await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Empty list policy"]');select.value='PRESERVE_PARENT';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
          await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
          state.preserveProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,850),preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,1300),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
          assert(state.preserveProposal.preview.includes('3ea21633-23e1-599b-91fc-b7666953ea26') && state.preserveProposal.preview.includes('—'));
          await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Empty list policy"]');select.value='EXCLUDE';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready' && document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.includes('0 rows')`, 30000);
          state.excludeProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,850),preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,900),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
          await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Empty list policy"]');select.value='ERROR';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='error'`, 30000);
          state.errorProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,1000),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
          assert(state.errorProposal.text.includes('Drop the original row') && state.errorProposal.text.includes('Keep the row with a missing item'), 'Empty-list error does not explain the available choices');
          await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Empty list policy"]');select.value='PRESERVE_PARENT';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready' && document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.includes('3ea21633-23e1-599b-91fc-b7666953ea26')`, 30000);
          await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
          await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===2`, 30000);
          await navigate(browser.cdp,pageURL);
          await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'`, 30000);
          state.savedPreserve = await browserEval(browser.cdp, `return {rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText)),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText)};`);
          assert.equal(state.savedPreserve.rows.length,1);
          assert(state.savedPreserve.rows[0].includes('3ea21633-23e1-599b-91fc-b7666953ea26'));
          await browserEval(browser.cdp, `[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].find(button=>button.innerText.includes('Expand')).click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
          await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Empty list policy"]'))`, 30000);
          state.savedPolicy = await browserEval(browser.cdp, `return document.querySelector('select[aria-label="Empty list policy"]').value;`);
          assert.equal(state.savedPolicy,'PRESERVE_PARENT');
          await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Empty list policy"]');select.value='EXCLUDE';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready' && document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.includes('0 rows')`, 30000);
          state.editExclude = await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,600),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
          assert.equal(state.editExclude.applyDisabled,false);
          await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
          await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].some(button=>button.innerText.includes('Empty lists: exclude'))`, 30000);
          await navigate(browser.cdp,pageURL);
          await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
          await new Promise(resolve=>setTimeout(resolve,5000));
          state.savedExclude = await browserEval(browser.cdp, `return {rowCount:document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount'),body:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,500),page:document.body.innerText.slice(0,2000),alerts:[...document.querySelectorAll('[role="alert"]')].map(item=>item.innerText)};`);
          await writeFile(join(evidenceDirectory,'bounded-observation-components.json'),JSON.stringify({pageURL,state,responses},null,2));
          assert.equal(state.savedExclude.rowCount,'1','Saved zero-row preview did not render an empty table');
          await browserEval(browser.cdp, `[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].find(button=>button.innerText.includes('Expand')).click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
          await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready' && document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.includes('1 rows')`, 30000);
          state.removeProposal = await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,600),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
          assert.equal(state.removeProposal.applyDisabled,false);
          await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
          await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1 && ![...document.querySelectorAll('[data-testid^="construction-history-step-"]')].some(button=>button.innerText.includes('Expand'))`, 30000);
          await navigate(browser.cdp,pageURL);
          await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'`, 30000);
          state.restored = await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText)),history:[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>button.innerText)};`);
          assert(state.restored.headers.includes('COMPONENT[].CODE.CODING[].CODE') && state.restored.history.length===1);
          state.interactions={clicksAndSelections:37,textEdits:2,includesRowChoiceInspection:true,includesTemporaryTableCleanup:true};
          state.timingsMs.totalBeforeCleanup=Date.now()-journeyStarted;
        }
        await writeFile(join(evidenceDirectory,'bounded-observation-components.json'),JSON.stringify({pageURL,state,responses},null,2));
      }
      if (action === 'Inspect bounded BodyStructure fields' || action === 'Inspect bounded BodyStructure field choice' || action === 'Verify bounded BodyStructure group' || action === 'Verify bounded BodyStructure expansion') {
        await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Cancel').click();return true;`);
        await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Select BodyStructure.resourceType"]'))`, 30000);
        state.fields = await browserEval(browser.cdp, `return [...document.querySelectorAll('input[aria-label^="Select BodyStructure."]')].map(input=>({label:input.getAttribute('aria-label'),disabled:input.disabled}));`);
        if (action === 'Inspect bounded BodyStructure field choice' || action === 'Verify bounded BodyStructure group' || action === 'Verify bounded BodyStructure expansion') {
          const fieldPath = action === 'Verify bounded BodyStructure expansion' ? 'extension[].url' : 'includedStructure[].structure.coding[].system';
          await browserEval(browser.cdp, `document.querySelector('[aria-label="Add columns editor"] input[aria-label=${JSON.stringify(`Select BodyStructure.${fieldPath}`)}]').click();return true;`);
          await browserEval(browser.cdp, `[...document.querySelectorAll('[aria-label="Add columns editor"] button')].find(button=>button.textContent?.trim()==='Add 1 selected feature').click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]')) || Boolean(document.querySelector('[data-testid="construction-proposal-panel"]'))`, 30000);
          state.fieldChoice = await browserEval(browser.cdp, `return {dialog:document.querySelector('[role="dialog"]')?.innerText.slice(0,3000),radios:[...document.querySelectorAll('[role="dialog"] input[type="radio"]')].map(input=>({label:input.getAttribute('aria-label'),text:input.closest('label')?.innerText,disabled:input.disabled})),proposal:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,1500)};`);
        }
      }
      if (action === 'Verify bounded BodyStructure group') {
        const oracleOutput = execFileSync('rtk', ['docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', 'var rows=db.BodyStructure.all().toArray().filter(d=>d.project==="loom_dev_cda_fhir"&&d.dataset_generation==="cda-fhir-v1");print(JSON.stringify(rows.map(d=>({id:d.id,systems:(d.payload.includedStructure||[]).flatMap(item=>(item.structure?.coding||[]).map(c=>c.system))}))))'], { encoding: 'utf8', maxBuffer: 2_000_000 });
        const rawRows = JSON.parse(oracleOutput.slice(oracleOutput.indexOf('[')));
        const expectedGroups = Object.groupBy(rawRows, row => row.systems[0] ?? '');
        state.rawOracle = { sourceRecords: rawRows.length, groups: Object.fromEntries(Object.entries(expectedGroups).map(([key, rows]) => [key, rows.length])) };
        assert(rawRows.length > 0 && Object.keys(expectedGroups).length > 0);
        await browserEval(browser.cdp, `document.querySelector('[role="dialog"] input[aria-label="includedStructure[].structure.coding[].system: Use the first value"]').click();[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.innerText.trim()==='Add 1 column').click();return true;`);
        await new Promise(resolve => setTimeout(resolve, 1200));
        state.afterAdd = await browserEval(browser.cdp, `return {dialog:document.querySelector('[role="dialog"]')?.innerText.slice(0,1800),editor:document.querySelector('[aria-label="Add columns editor"]')?.innerText.slice(0,1800),proposal:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,1800),status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),alerts:[...document.querySelectorAll('[role="alert"]')].map(item=>item.innerText),body:document.body.innerText.slice(-2000)};`);
        await mkdir(evidenceDirectory,{recursive:true});
        await writeFile(join(evidenceDirectory,'bounded-bodystructure-group.json'),JSON.stringify({pageURL,state,responses},null,2));
        assert(state.afterAdd.body?.includes('2 configured'), 'The field was not saved as a second column');
        await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Summarize into groups')).click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-group"]'))`, 30000);
        state.groupEditor = await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-reshape-group"]')?.innerText.slice(0,2400),keys:[...document.querySelectorAll('[data-testid="construction-reshape-group"] input[aria-label^="Group by"]')].map(input=>({label:input.getAttribute('aria-label'),disabled:input.disabled}))};`);
        const groupStarted = Date.now();
        await browserEval(browser.cdp, `document.querySelector('input[aria-label="Group by includedStructure[].structure.coding[].system"]').click();return true;`);
        await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
        state.timingsMs.groupProposal = Date.now() - groupStarted;
        state.groupProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,1400),preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,1400),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
        await mkdir(evidenceDirectory,{recursive:true});
        await writeFile(join(evidenceDirectory,'bounded-bodystructure-group.json'),JSON.stringify({pageURL,state,responses},null,2));
        assert.equal(state.groupProposal.status, 'ready', state.groupProposal.text);
        assert.equal(state.groupProposal.applyDisabled, false);
        assert(state.groupProposal.preview.includes('135'), 'Group proposal does not contain the raw CDA count');
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
        await navigate(browser.cdp,pageURL);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        const previewStarted = Date.now();
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'`, 30000);
        state.timingsMs.groupPreview = Date.now() - previewStarted;
        state.savedGroup = await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText)),history:document.querySelector('[data-testid^="construction-history-step-"]')?.innerText};`);
        assert(state.savedGroup.rows[0]?.includes('135') && state.savedGroup.rows[0]?.some(cell=>cell.includes('https://cda.readthedocs.io/')), 'Saved group values differ from raw CDA');
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-group"]'))`, 30000);
        state.savedGroupEditor = await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-reshape-group"]')?.innerText.slice(0,1300),keyChecked:document.querySelector('input[aria-label="Group by includedStructure[].structure.coding[].system"]')?.checked};`);
        assert.equal(state.savedGroupEditor.keyChecked, true, 'Saved group key is not editable');
        await browserEval(browser.cdp, `const input=document.querySelector('input[aria-label="Summary output label 1"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'CDA record count');input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
        await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
        state.editProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,900),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
        assert.equal(state.editProposal.applyDisabled, false);
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
        await navigate(browser.cdp,pageURL);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(cell=>cell.innerText==='CDA RECORD COUNT')`, 30000);
        state.editedGroup = await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
        assert(state.editedGroup.rows[0]?.includes('135'), 'Editing the saved group changed its CDA count');
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
        state.removeProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,900),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
        assert.equal(state.removeProposal.applyDisabled, false);
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`, 30000);
        await navigate(browser.cdp,pageURL);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='26'`, 30000);
        state.restored = await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1,5).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText)),historyCount:document.querySelectorAll('[data-testid^="construction-history-step-"]').length};`);
        assert(state.restored.rows.length > 0 && state.restored.rows.every(row=>rawRows.some(raw=>raw.id===row[0])), 'Restored rows do not match raw CDA');
        state.interactions = { clicks: 28, textEdits: 1, includesRowChoiceInspection: true, includesTemporaryTableCleanup: true };
        state.timingsMs.totalBeforeCleanup = Date.now() - journeyStarted;
        state.errors = responses.filter(response => response.status >= 400);
        await writeFile(join(evidenceDirectory,'bounded-bodystructure-group.json'),JSON.stringify({pageURL,state,responses},null,2));
      }
      if (action === 'Verify bounded BodyStructure expansion') {
        const oracleOutput = execFileSync('rtk', ['docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', 'var rows=db.BodyStructure.all().toArray().filter(d=>d.project==="loom_dev_cda_fhir"&&d.dataset_generation==="cda-fhir-v1");print(JSON.stringify(rows.map(d=>({id:d.id,urls:(d.payload.extension||[]).map(e=>e.url)}))))'], { encoding: 'utf8', maxBuffer: 2_000_000 });
        const rawRows = JSON.parse(oracleOutput.slice(oracleOutput.indexOf('[')));
        state.rawOracle = { sourceRecords: rawRows.length, expandedRows: rawRows.reduce((sum, row) => sum + row.urls.length, 0), multi: rawRows.filter(row => row.urls.length > 1) };
        assert(state.rawOracle.multi.length > 0, 'No source record has multiple extension URLs');
        await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.innerText.trim()==='Add 1 column').click();return true;`);
        await waitForBrowser(browser.cdp, `document.body.innerText.includes('2 configured')`, 30000);
        await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value')))`, 30000);
        state.expandChoice = await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value'));return {disabled:button?.disabled,text:button?.innerText};`);
        if (!state.expandChoice.disabled) {
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand a repeated value')).click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Repeated field"]'))`, 30000);
          state.expandEditor = await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-reshape-expand"]')?.innerText.slice(0,1600),fields:[...document.querySelector('select[aria-label="Repeated field"]').options].map(option=>({label:option.textContent,value:option.value,selected:option.selected})),emptyPolicy:document.querySelector('select[aria-label="Empty list policy"]')?.value};`);
          assert.equal(state.expandEditor.fields.length, 1);
          assert(state.expandEditor.fields[0].selected && state.expandEditor.fields[0].label.startsWith('extension[].url'));
          const proposalStarted = Date.now();
          await browserEval(browser.cdp, `document.querySelector('input[aria-label="Include item position"]').click();const select=document.querySelector('select[aria-label="Empty list policy"]');select.value='PRESERVE_PARENT';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
          await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
          state.timingsMs.expandProposal = Date.now() - proposalStarted;
          state.expandProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,900),preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,1500),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
          await mkdir(evidenceDirectory,{recursive:true});
          await writeFile(join(evidenceDirectory,'bounded-bodystructure-expansion.json'),JSON.stringify({pageURL,state,responses},null,2));
          assert.equal(state.expandProposal.status, 'ready', state.expandProposal.text);
          assert.equal(state.expandProposal.applyDisabled, false);
          await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
          await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
          await navigate(browser.cdp,pageURL);
          await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
          await browserEval(browser.cdp, `const select=[...document.querySelectorAll('select')].find(item=>[...item.options].some(option=>option.value==='500')&&[...item.options].some(option=>option.value==='25'));if(!select)throw new Error('Preview row limit missing');select.value='500';select.dispatchEvent(new Event('change',{bubbles:true}));[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='138'`, 30000);
          state.savedExpansion = await browserEval(browser.cdp, `const scroll=document.querySelector('[data-testid="preview-table-scroll"]');const rows=new Map();for(let top=0;top<=scroll.scrollHeight;top+=Math.max(200,scroll.clientHeight-100)){scroll.scrollTop=top;await new Promise(resolve=>setTimeout(resolve,35));for(const row of scroll.querySelectorAll('[role="row"]')){const cells=[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText);if(cells.length)rows.set(row.style.top,cells)}}return {rowCount:scroll.querySelector('[role="table"]')?.getAttribute('aria-rowcount'),headers:[...scroll.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText),renderedRows:rows.size,targetRows:[...rows.values()].filter(cells=>cells[0]===${JSON.stringify(state.rawOracle.multi[0].id)}),sample:[...rows.values()].slice(0,3)};`);
          await writeFile(join(evidenceDirectory,'bounded-bodystructure-expansion.json'),JSON.stringify({pageURL,state,responses},null,2));
          assert.equal(state.savedExpansion.rowCount, String(state.rawOracle.expandedRows + 1));
          assert.equal(state.savedExpansion.targetRows.length, 3, 'The three raw CDA extension values did not become three rendered rows');
          assert(state.savedExpansion.targetRows.every(row=>row.some(cell=>cell.includes('part-of-study'))), 'Expanded values differ from raw CDA');
          assert.deepEqual(state.savedExpansion.targetRows.map(row=>row.at(-1)).sort(), ['0','1','2']);
          await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
          await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-expand"]'))`, 30000);
          state.savedEditor = await browserEval(browser.cdp, `return {field:document.querySelector('select[aria-label="Repeated field"]')?.selectedOptions[0]?.textContent,position:document.querySelector('input[aria-label="Include item position"]')?.checked,emptyPolicy:document.querySelector('select[aria-label="Empty list policy"]')?.value};`);
          assert(state.savedEditor.field?.startsWith('extension[].url') && state.savedEditor.position && state.savedEditor.emptyPolicy==='PRESERVE_PARENT');
          await browserEval(browser.cdp, `const input=document.querySelector('input[aria-label="Expanded item label"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'CDA extension URL');input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
          await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
          state.editProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled,text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,700)};`);
          assert.equal(state.editProposal.applyDisabled, false);
          await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
          await navigate(browser.cdp,pageURL);
          await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
          await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(cell=>cell.innerText==='CDA EXTENSION URL')`, 30000);
          state.editedHeaders = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText);`);
          await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
          await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
          await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
          await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
          state.removeProposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled,text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,700)};`);
          assert.equal(state.removeProposal.applyDisabled, false);
          await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
          await navigate(browser.cdp,pageURL);
          await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
          await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
          await browserEval(browser.cdp, `const select=[...document.querySelectorAll('select')].find(item=>[...item.options].some(option=>option.value==='500')&&[...item.options].some(option=>option.value==='25'));select.value='500';select.dispatchEvent(new Event('change',{bubbles:true}));[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
          await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='136'`, 30000);
          state.restored = await browserEval(browser.cdp, `return {rowCount:document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount'),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),historyCount:document.querySelectorAll('[data-testid^="construction-history-step-"]').length};`);
          assert.equal(state.restored.historyCount, 0);
          assert(state.restored.headers.includes('EXTENSION[].URL') && !state.restored.headers.includes('CDA EXTENSION URL'));
          state.interactions = { clicksAndSelections: 30, textEdits: 1, includesRowChoiceInspection: true, includesTemporaryTableCleanup: true };
          state.timingsMs.totalBeforeCleanup = Date.now() - journeyStarted;
          state.errors = responses.filter(response=>response.status>=400);
          await writeFile(join(evidenceDirectory,'bounded-bodystructure-expansion.json'),JSON.stringify({pageURL,state,responses},null,2));
        }
        await mkdir(evidenceDirectory,{recursive:true});
        await writeFile(join(evidenceDirectory,'bounded-bodystructure-expansion.json'),JSON.stringify({pageURL,state,responses},null,2));
      }
      if (action === 'Verify bounded BodyStructure row definition') {
        const oracleOutput = execFileSync('rtk', [
          'docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh',
          '--server.database', 'loom_dev', '--javascript.execute-string',
          'var rows=db.BodyStructure.all().toArray().filter(d=>d.project==="loom_dev_cda_fhir"&&d.dataset_generation==="cda-fhir-v1");print(JSON.stringify(rows.map(d=>({id:d.id,includedCount:(d.payload.includedStructure||[]).length}))))',
        ], { encoding: 'utf8', maxBuffer: 2_000_000 });
        const rawBodyStructures = JSON.parse(oracleOutput.slice(oracleOutput.indexOf('[')));
        const rawIDs = new Set(rawBodyStructures.map(row => row.id));
        state.rawOracle = { sourceRecords: rawBodyStructures.length, maximumIncludedValues: Math.max(...rawBodyStructures.map(row => row.includedCount)) };
        const expanded = state.options.find(option => option.label.includes('Included anatomic location(s)') && option.label.includes('Keep records with no values'));
        assert(expanded, 'BodyStructure included locations row shape is missing');
        await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="New row shape"]');select.value=${JSON.stringify(expanded.value)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
        const expandedProposalStarted = Date.now();
        await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Preview row change').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[aria-label="Row definition preview"]')) || Boolean(document.querySelector('[role="dialog"] [role="alert"]'))`, 30000);
        state.timingsMs.expandedProposal = Date.now() - expandedProposalStarted;
        state.expandedProposal = await browserEval(browser.cdp, `return {text:document.querySelector('[role="dialog"]')?.innerText,apply:[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Apply row definition')?.disabled};`);
        await mkdir(evidenceDirectory,{recursive:true});
        await writeFile(join(evidenceDirectory,'bounded-bodystructure-row-proposal.json'),JSON.stringify({pageURL,state,responses},null,2));
        assert.equal(state.expandedProposal.apply, false, 'Row definition proposal is not applicable');
        await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Apply row definition').click();return true;`);
        await waitForBrowser(browser.cdp, `!document.querySelector('[role="dialog"]') || document.body.innerText.includes('candidate receipt no longer changes only the requested table rows')`, 30000);
        state.afterApply = await browserEval(browser.cdp, `return {dialog:document.querySelector('[role="dialog"]')?.innerText.slice(0,2000),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText),body:document.body.innerText.slice(0,500)};`);
        await writeFile(join(evidenceDirectory,'bounded-bodystructure-row-proposal.json'),JSON.stringify({pageURL,state,responses},null,2));
        assert.equal(state.afterApply.dialog, undefined, 'Row definition Apply left the editor open');
        await navigate(browser.cdp,pageURL);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        const expandedPreviewStarted = Date.now();
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 60000);
        state.timingsMs.expandedPreview = Date.now() - expandedPreviewStarted;
        state.expandedPreview = await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,4000),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1,26).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText)),rowSetting:document.querySelector('[data-testid="construction-source-setup"]')?.innerText.slice(0,350)};`);
        assert(state.expandedPreview.rows.length > 0 && state.expandedPreview.rows.every(row => rawIDs.has(row[0])), 'Expanded row IDs do not match raw CDA BodyStructure records');
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]')?.open===true`, 30000);
        state.expandedRowSetting = await browserEval(browser.cdp, `return document.querySelector('[aria-label="Row definition settings"]')?.innerText.slice(0,350);`);
        assert(state.expandedRowSetting?.includes('Current rows: One row per value in includedStructure[]'), 'Expanded row meaning was not restored after reload');
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Configure rows').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="New row shape"]'))`, 30000);
        await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="New row shape"]');select.value='records';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
        const restorationProposalStarted = Date.now();
        await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Preview row change').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Apply row definition'&&!button.disabled)) || Boolean(document.querySelector('[role="dialog"] [role="alert"]'))`, 30000);
        state.timingsMs.restorationProposal = Date.now() - restorationProposalStarted;
        state.restorationProposal = await browserEval(browser.cdp, `return document.querySelector('[role="dialog"]')?.innerText.slice(0,2200);`);
        await browserEval(browser.cdp, `const button=[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Apply row definition'&&!button.disabled);if(!button)throw new Error('Restore rows Apply unavailable');button.click();return true;`);
        await waitForBrowser(browser.cdp, `!document.querySelector('[role="dialog"]')`, 30000);
        await navigate(browser.cdp,pageURL);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        const restoredPreviewStarted = Date.now();
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 60000);
        state.timingsMs.restoredPreview = Date.now() - restoredPreviewStarted;
        state.restoredPreview = await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,1000),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1,26).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText)),rowSetting:document.querySelector('[data-testid="construction-source-setup"]')?.innerText.slice(0,350)};`);
        const commonVisibleRows = Math.min(state.restoredPreview.rows.length, state.expandedPreview.rows.length);
        assert(commonVisibleRows > 0, 'No shared CDA rows were rendered after restoration');
        assert.deepEqual(state.restoredPreview.rows.slice(0, commonVisibleRows), state.expandedPreview.rows.slice(0, commonVisibleRows), 'Restoring source-record rows changed the common visible CDA sample');
        assert(state.restoredPreview.rows.every(row => rawIDs.has(row[0])), 'Restored row IDs do not match raw CDA BodyStructure records');
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]')?.open===true`, 30000);
        state.restoredRowSetting = await browserEval(browser.cdp, `return document.querySelector('[aria-label="Row definition settings"]')?.innerText.slice(0,350);`);
        assert(state.restoredRowSetting?.includes('Current rows: One row per source record'), 'Source-record row meaning was not restored after reload');
        state.clicks = 19;
        state.timingsMs.totalBeforeCleanup = Date.now() - journeyStarted;
        state.errors = responses.filter(response => response.status >= 400);
      }
      await mkdir(evidenceDirectory,{recursive:true});
      await writeFile(join(evidenceDirectory,`bounded-${resourceType.toLowerCase()}-row-choices.json`),JSON.stringify({pageURL,state,responses},null,2));
      console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.status>=400)},null,2));
    } finally {
      if (created) {
        await navigate(browser.cdp,pageURL);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        await browserEval(browser.cdp, `document.querySelector('button[aria-label="Delete table"]').click();return true;`);
      }
    }
  } else if (action === 'Inspect bounded related expansion' || action === 'Verify bounded related expansion') {
    const patientIDs = ['02f8e963-73b8-50ea-b840-c4a80719a06a','54b50ad3-aa10-5483-85e2-5382aac7d374'];
    const tableName = `Patient related expansion QA ${Date.now()}`;
    const state = { tableName, patientIDs };
    let created = false;
    try {
      state.selection=await browserEval(browser.cdp, `const base='/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}';const builder=await (await fetch(base+'/authoring/v2/builder')).json();const response=await fetch(base+'/selections',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({snapshotToken:builder.catalog.snapshotToken,idempotencyKey:'cda-related-expand-${Date.now()}',source:{kind:'resources',resources:{refs:${JSON.stringify(patientIDs)}.map(id=>({project:'loom_dev_cda_fhir',generation:builder.catalog.generation,resourceType:'Patient',id}))}}})});return {status:response.status,body:await response.json()};`);
      assert.equal(state.selection.status,201,JSON.stringify(state.selection.body));
      await navigate(browser.cdp,`${pageURL}&selection=${encodeURIComponent(state.selection.body.id)}`);
      await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Choose Patient rows"]:not(:disabled)'))`, 30000);
      await browserEval(browser.cdp, `const input=document.querySelector('#first-table-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(tableName)});input.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('button[aria-label="Choose Patient rows"]').click();return true;`);
      created=true;
      await waitForBrowser(browser.cdp, `document.body.innerText.includes(${JSON.stringify(tableName)})`, 30000);
      await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources'&&!button.disabled))`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources').click();return true;`);
      await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Starting collection"]')?.innerText.includes('constrain one row per Patient')`, 30000);
      await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand related records')))`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Expand related records')).click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-related-expand-editor"] label:nth-of-type(1) select'))`, 30000);
      await browserEval(browser.cdp, `const select=[...document.querySelectorAll('[data-testid="construction-related-expand-editor"] label')].find(label=>label.innerText.startsWith('Related record type'))?.querySelector('select');if(!select)throw new Error('Related type selector missing');select.value='Observation';select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[type="radio"][name^="related-expand-route-"]'))`, 30000);
      state.editor=await browserEval(browser.cdp, `const editor=document.querySelector('[data-testid="construction-related-expand-editor"]');const policy=[...editor.querySelectorAll('label')].find(label=>label.innerText.startsWith('When a parent has no matching record'))?.querySelector('select');return {text:editor?.innerText.slice(0,4000),routes:[...document.querySelectorAll('input[type="radio"][name^="related-expand-route-"]')].map(input=>({label:input.closest('label')?.innerText,checked:input.checked,disabled:input.disabled})),policy:[...policy.options].map(option=>({label:option.textContent,value:option.value})),proposal:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,1200)};`);
      if (action === 'Verify bounded related expansion') {
        await browserEval(browser.cdp, `const input=[...document.querySelectorAll('input[type="radio"][name^="related-expand-route-"]')].find(input=>input.closest('label')?.innerText.trim()==='Observation via subject_Patient');if(!input)throw new Error('Direct Patient to Observation path missing');input.click();const policy=[...document.querySelectorAll('[data-testid="construction-related-expand-editor"] label')].find(label=>label.innerText.startsWith('When a parent has no matching record'))?.querySelector('select');policy.value='PRESERVE_PARENT';policy.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
        await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 60000);
        state.proposal=await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,1000),preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,1400),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
        assert.equal(state.proposal.status,'ready',state.proposal.text);
        assert.equal(state.proposal.applyDisabled,false);
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
        await navigate(browser.cdp,`${pageURL}&selection=${encodeURIComponent(state.selection.body.id)}`);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        await waitForBrowser(browser.cdp, `document.body.innerText.includes(${JSON.stringify(`DATASET WORKSPACE\n\n${tableName}`)})`, 30000);
        await browserEval(browser.cdp, `const limit=document.querySelector('select[aria-label="Preview row limit"]');limit.value='100';limit.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Preview row limit"]')?.value==='100'`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='40'`, 30000);
        state.saved=await browserEval(browser.cdp, `const scroll=document.querySelector('[data-testid="preview-table-scroll"]');const rows=new Map();for(let top=0;top<=scroll.scrollHeight;top+=Math.max(200,scroll.clientHeight-100)){scroll.scrollTop=top;await new Promise(resolve=>setTimeout(resolve,35));for(const row of scroll.querySelectorAll('[role="row"]')){const cells=[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText);if(cells.length)rows.set(row.style.top,cells)}}return {rowcount:scroll.querySelector('[role="table"]')?.getAttribute('aria-rowcount'),headers:[...scroll.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText),rows:[...rows.values()]};`);
        const oracle=JSON.parse(await readFile('.artifacts/cda-builder/2026-09-26T22-25-42.310Z/related-observation-values.json','utf8'));
        const observed=new Set(oracle.patients.find(patient=>patient.patientId===patientIDs[0]).subjectObservations.map(item=>item.id));
        const manyRows=state.saved.rows.filter(row=>row[0]===patientIDs[0]);
        const zeroRows=state.saved.rows.filter(row=>row[0]===patientIDs[1]);
        assert.equal(manyRows.length,38,'All 38 related CDA rows should render');
        assert.deepEqual(new Set(manyRows.map(row=>row[1])),observed);
        assert.equal(zeroRows.length,1);
        assert.equal(zeroRows[0][1],'—');
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-related-expand-editor"]'))`, 30000);
        state.savedEditor=await browserEval(browser.cdp, `const editor=document.querySelector('[data-testid="construction-related-expand-editor"]');const type=[...editor.querySelectorAll('label')].find(label=>label.innerText.startsWith('Related record type'))?.querySelector('select');const policy=[...editor.querySelectorAll('label')].find(label=>label.innerText.startsWith('When a parent has no matching record'))?.querySelector('select');return {type:type?.value,policy:policy?.value,selectedRoute:[...editor.querySelectorAll('input[type="radio"]')].find(input=>input.checked)?.closest('label')?.innerText};`);
        assert.equal(state.savedEditor.type,'Observation');
        assert.equal(state.savedEditor.policy,'PRESERVE_PARENT');
        await browserEval(browser.cdp, `const policy=[...document.querySelectorAll('[data-testid="construction-related-expand-editor"] label')].find(label=>label.innerText.startsWith('When a parent has no matching record'))?.querySelector('select');policy.value='EXCLUDE';policy.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 30000);
        state.excludeProposal=await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText.slice(0,700),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
        assert.equal(state.excludeProposal.applyDisabled,false);
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]')?.innerText.includes('omit parents without a match')`, 30000);
        await navigate(browser.cdp,`${pageURL}&selection=${encodeURIComponent(state.selection.body.id)}`);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        await waitForBrowser(browser.cdp, `document.body.innerText.includes(${JSON.stringify(`DATASET WORKSPACE\n\n${tableName}`)})`, 30000);
        await browserEval(browser.cdp, `const limit=document.querySelector('select[aria-label="Preview row limit"]');limit.value='100';limit.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Preview row limit"]')?.value==='100'`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='39'`, 30000);
        state.savedExclude=await browserEval(browser.cdp, `return {rowcount:document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount'),preview:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,1100),history:document.querySelector('[data-testid^="construction-history-step-"]')?.innerText};`);
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
        await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-remove-step-"]').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 30000);
        await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`, 30000);
        await navigate(browser.cdp,`${pageURL}&selection=${encodeURIComponent(state.selection.body.id)}`);
        await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
        await waitForBrowser(browser.cdp, `document.body.innerText.includes(${JSON.stringify(`DATASET WORKSPACE\n\n${tableName}`)})`, 30000);
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
        await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='3'`, 30000);
        state.restored=await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
        assert.deepEqual(new Set(state.restored.rows.map(row=>row[0])),new Set(patientIDs));
      }
      await mkdir(evidenceDirectory,{recursive:true});
      await writeFile(join(evidenceDirectory,'bounded-related-expansion-inspection.json'),JSON.stringify({pageURL,state,responses},null,2));
      console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.status>=400)},null,2));
    } catch (error) {
      state.failure={message:error instanceof Error?error.message:String(error),dom:await browserEval(browser.cdp, `return {body:document.body.innerText.slice(0,2300),table:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,1000),rowcount:document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount'),alerts:[...document.querySelectorAll('[role="alert"]')].map(item=>item.innerText)};`)};
      await mkdir(evidenceDirectory,{recursive:true});
      await writeFile(join(evidenceDirectory,'bounded-related-expansion-failure.json'),JSON.stringify({pageURL,state,responses},null,2));
      throw error;
    } finally {
      if (created) {
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))?.click();[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Delete')?.click();return true;`);
      }
    }
  } else if (action === 'Verify starting collection') {
    const selectionID = process.argv[4];
    assert(selectionID, 'A CDA selection revision ID is required');
    const tableName = `BodyStructure population QA ${Date.now()}`;
    const selectedID = '9a651f6b-6b9b-54a5-8294-31a42a6df35f';
    const state = { tableName, selectedID, interactions: 0 };
    let created = false;
    try {
      await navigate(browser.cdp,`${pageURL}&selection=${encodeURIComponent(selectionID)}`);
      await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Choose BodyStructure rows"]:not(:disabled)'))`, 30000);
      await browserEval(browser.cdp, `const input=document.querySelector('#first-table-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(tableName)});input.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('button[aria-label="Choose BodyStructure rows"]').click();return true;`);
      state.interactions += 2;
      created = true;
      await waitForBrowser(browser.cdp, `document.body.innerText.includes(${JSON.stringify(tableName)}) && Boolean(document.querySelector('[data-testid="construction-source-setup"]'))`, 30000);
      await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources'))`, 30000);
      state.before=await browserEval(browser.cdp, `return {panel:document.querySelector('[aria-label="Starting collection"]')?.innerText,buttons:[...document.querySelectorAll('[aria-label="Starting collection"] button')].map(button=>({text:button.innerText,disabled:button.disabled})),table:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.slice(0,500)};`);
      assert.equal(state.before.buttons.find(button=>button.text==='Use selected resources')?.disabled,false);
      await browserEval(browser.cdp, `[...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Starting collection"]')?.innerText.includes('constrain one row per BodyStructure')`, 30000);
      state.attachedPanel=await browserEval(browser.cdp, `return {text:document.querySelector('[aria-label="Starting collection"]')?.innerText,buttons:[...document.querySelectorAll('[aria-label="Starting collection"] button')].map(button=>({text:button.innerText,disabled:button.disabled})),attached:document.querySelector('[aria-label="Starting collection"]')?.getAttribute('data-attached-selection-revision-id')};`);
      assert.equal(state.attachedPanel.attached,selectionID);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'`, 30000);
      state.attachedPreview=await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
      assert(state.attachedPreview.rows.some(row=>row.includes(selectedID)));
      await navigate(browser.cdp,`${pageURL}&selection=${encodeURIComponent(selectionID)}`);
      await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
      state.interactions++;
      await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use all authorized rows'))`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'`, 30000);
      state.reloadedPreview=await browserEval(browser.cdp, `return {panel:document.querySelector('[aria-label="Starting collection"]')?.innerText,rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
      assert(state.reloadedPreview.rows.some(row=>row.includes(selectedID)));
      const coverageAvailable=await browserEval(browser.cdp, `return Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Check selected-resource coverage'&&!button.disabled));`);
      if (coverageAvailable) {
        await browserEval(browser.cdp, `[...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Check selected-resource coverage').click();return true;`);
        state.interactions++;
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="population-coverage-report"]')) || Boolean(document.querySelector('[aria-label="Starting collection"] [role="alert"]'))`, 30000);
        state.coverage=await browserEval(browser.cdp, `return {report:document.querySelector('[data-testid="population-coverage-report"]')?.innerText,alert:document.querySelector('[aria-label="Starting collection"] [role="alert"]')?.innerText};`);
        assert(state.coverage.report?.includes('1 selected · 1 produce rows · 0 needs attention'),JSON.stringify(state.coverage));
      }
      await browserEval(browser.cdp, `[...document.querySelectorAll('[aria-label="Starting collection"] button')].find(button=>button.innerText==='Use all authorized rows').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Starting collection"]')?.innerText.includes('ready to constrain this table')`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='26'`, 30000);
      state.clearedPreview=await browserEval(browser.cdp, `return {panel:document.querySelector('[aria-label="Starting collection"]')?.innerText,rowcount:document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount'),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1,4).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
      await navigate(browser.cdp,pageURL);
      await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`);
      state.interactions++;
      await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
      state.interactions++;
      await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Starting collection"]')?.innerText.includes('every authorized BodyStructure resource')`, 30000);
      state.restoredPanel=await browserEval(browser.cdp, `return document.querySelector('[aria-label="Starting collection"]')?.innerText;`);
      assert(!state.restoredPanel.includes('selected BodyStructure resources'));
      await mkdir(evidenceDirectory,{recursive:true});
      await writeFile(join(evidenceDirectory,'starting-collection.json'),JSON.stringify({pageURL,selectionID,state,responses},null,2));
      console.log(JSON.stringify({evidenceDirectory,state,responses:responses.filter(response=>response.status>=400)},null,2));
    } finally {
      if (created) {
        await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))?.click();[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Delete')?.click();return true;`);
        state.interactions += 2;
      }
    }
  } else if (action === 'Inspect starting selection') {
    const selectedID = '9a651f6b-6b9b-54a5-8294-31a42a6df35f';
    const selection = await browserEval(browser.cdp, `const base='/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}';const builder=await (await fetch(base+'/authoring/v2/builder')).json();const response=await fetch(base+'/selections',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({snapshotToken:builder.catalog.snapshotToken,idempotencyKey:'cda-builder-population-${Date.now()}',source:{kind:'resources',resources:{refs:[{project:'loom_dev_cda_fhir',generation:builder.catalog.generation,resourceType:'BodyStructure',id:${JSON.stringify(selectedID)}}]}}})});return {status:response.status,body:await response.json(),generation:builder.catalog.generation};`);
    assert.equal(selection.status,201,JSON.stringify(selection.body));
    await navigate(browser.cdp,`${pageURL}&selection=${encodeURIComponent(selection.body.id)}`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]')?.innerText.includes('selected BodyStructure resources')`, 30000);
    const panel=await browserEval(browser.cdp, `return {text:document.querySelector('[data-testid="construction-source-setup"]')?.innerText.slice(0,1700),selection:document.querySelector('[aria-label="Starting collection"]')?.innerText.slice(0,1000),buttons:[...document.querySelectorAll('[aria-label="Starting collection"] button')].map(button=>({text:button.innerText,disabled:button.disabled}))};`);
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'starting-selection-inspection.json'),JSON.stringify({pageURL,selection,panel,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,selection,panel,responses:responses.filter(response=>response.status>=400)},null,2));
  } else if (action === 'Inspect source setup') {
    await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-source-setup"]')?.open === true`, 30000);
    const state=await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-source-setup"]');const response=await fetch('/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}/authoring/v2/builder');const builder=await response.json();return {text:panel?.innerText.slice(0,5000),controls:[...panel.querySelectorAll('button,select,input')].filter(element=>element.offsetParent!==null).map(element=>({tag:element.tagName,label:element.getAttribute('aria-label'),text:element.innerText?.slice(0,90),disabled:element.disabled,value:element.value})).filter(item=>item.label||item.text?.includes('row')||item.text?.includes('population')),catalog:{snapshotToken:builder.catalog?.snapshotToken,generation:builder.catalog?.generation},responseStatus:response.status};`);
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
  } else if (action === 'Verify related source chooser') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))`, 30000);
    const before = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-add-columns-source"]');return {text:panel.innerText,buttons:[...panel.querySelectorAll('button')].map(button=>({label:button.getAttribute('aria-label'),key:button.dataset.sourceKey,kind:button.dataset.sourceKind,selected:button.getAttribute('aria-pressed')==='true',disabled:button.disabled,visible:button.offsetParent!==null})),search:panel.querySelector('input[type="search"]')?.getAttribute('aria-label')};`);
    assert(before.buttons.some(button=>button.kind==='ROOT' && button.selected));
    assert.deepEqual(new Set(before.buttons.filter(button=>button.kind==='RELATED').map(button=>button.label.split(',')[0])), new Set(['Patient','ResearchStudy','Medication','ResearchSubject','Condition','BodyStructure','MedicationAdministration','Observation']));
    await browserEval(browser.cdp, `const search=document.querySelector('input[aria-label="Search related resources"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(search,'Obser');search.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-source-kind="RELATED"]')].filter(button=>button.offsetParent!==null).length===1`, 30000);
    const filtered = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="construction-add-columns-source-option"]')].filter(button=>button.offsetParent!==null).map(button=>button.getAttribute('aria-label'));`);
    const { source } = await chooseRelatedSource('Observation');
    const after = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-add-columns-source"]');return {selected:[...panel.querySelectorAll('button[aria-pressed="true"]')].map(button=>button.getAttribute('aria-label')),search:panel.querySelector('input[type="search"]')?.value,content:document.querySelector('[aria-label="Add columns editor"]')?.innerText.slice(0,1800)};`);
    assert.equal(after.search,'');
    assert(after.selected.some(label=>label.startsWith('Observation,')));
    await mkdir(evidenceDirectory,{recursive:true});
    const screenshot = await browser.cdp.send('Page.captureScreenshot',{format:'png',captureBeyondViewport:true});
    await writeFile(join(evidenceDirectory,'related-source-chooser.png'),Buffer.from(screenshot.data,'base64'));
    await writeFile(join(evidenceDirectory,'related-source-chooser.json'),JSON.stringify({pageURL,before,filtered,source,after,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,before,filtered,source,after,responses:responses.filter(response=>response.status>=400)},null,2));
  } else if (action === 'Inspect Patient field choice' || action === 'Inspect selected Patient route' || action === 'Inspect Patient proposal' || action === 'Verify Patient related column') {
    let baseline;
    if (action === 'Verify Patient related column') {
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
      baseline = await browserEval(browser.cdp, `return {columns:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1,4).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
    }
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
    const { source } = await chooseRelatedSource('Patient');
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Select Patient.id"]:not(:disabled)'))`, 30000);
    await browserEval(browser.cdp, `document.querySelector('input[aria-label="Select Patient.id"]').click();return true;`);
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Add 1 selected feature').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 30000);
    if (action !== 'Inspect Patient field choice') {
      await browserEval(browser.cdp, `document.querySelector('[role="dialog"] input[type="radio"]').click();return true;`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"] input[aria-label="id: Keep all matching values"]'))`, 30000);
    }
    if (action === 'Inspect Patient proposal' || action === 'Verify Patient related column') {
      await browserEval(browser.cdp, `document.querySelector('[role="dialog"] input[aria-label="id: Keep all matching values"]').click();return true;`);
      await waitForBrowser(browser.cdp, `document.querySelector('[role="dialog"] button:last-child')?.disabled===false`, 30000);
      await browserEval(browser.cdp, `document.querySelector('[role="dialog"] button:last-child').click();return true;`);
      await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 60000);
    }
    const dialog = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');return dialog ? {text:dialog.innerText,controls:[...dialog.querySelectorAll('input,button')].map(element=>({tag:element.tagName,label:element.getAttribute('aria-label')??element.innerText,disabled:element.disabled,checked:element.checked,value:element.value}))} : null;`);
    const proposal = action === 'Inspect Patient proposal' || action === 'Verify Patient related column' ? await browserEval(browser.cdp, `return {panel:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview"] [role="row"]')].slice(1,4).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText)),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`) : undefined;
    let saved;
    if (action === 'Verify Patient related column') {
      assert.equal(proposal?.applyDisabled,false);
      await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
      await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
      await navigate(browser.cdp,pageURL);
      await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
      await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview').click();return true;`);
      await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='4'`, 30000);
      saved = await browserEval(browser.cdp, `return {history:document.querySelector('[data-testid^="construction-history-step-"]')?.innerText,columns:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1,4).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText))};`);
      assert.equal(saved.rows.length,3);
      for (const row of saved.rows) assert.equal(row.at(-1),row[1]?.replace(/^Patient\//,''));
    }
    await mkdir(evidenceDirectory,{recursive:true});
    if (action === 'Inspect selected Patient route') {
      const screenshot = await browser.cdp.send('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});
      await writeFile(join(evidenceDirectory,'patient-selected-route.png'),Buffer.from(screenshot.data,'base64'));
    }
    await writeFile(join(evidenceDirectory,action === 'Verify Patient related column' ? 'patient-related-applied.json' : action === 'Inspect Patient proposal' ? 'patient-proposal.json' : action === 'Inspect selected Patient route' ? 'patient-selected-route.json' : 'patient-field-choice.json'),JSON.stringify({pageURL,source,baseline,dialog,proposal,saved,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,source,baseline,dialog,proposal,saved,responses:responses.filter(response=>response.status>=400)},null,2));
  } else if (action === 'Verify direct Observation COUNT many and zero' || action === 'Verify direct Observation forms many and zero') {
    const includeAllAndPresence = action === 'Verify direct Observation forms many and zero';
    const targetExplorer = 'cda-builder-full-qa-1790440983382';
    const manyPatientId = '02f8e963-73b8-50ea-b840-c4a80719a06a';
    const zeroPatientId = '54b50ad3-aa10-5483-85e2-5382aac7d374';
    const rawOracle = {
      source: 'Arango loom_dev, project loom_dev_cda_fhir, generation cda-fhir-v1, direct subject_Patient edges',
      evidencePath: '.artifacts/cda-builder/2026-09-26T22-25-42.310Z/related-observation-oracle.json',
      manyPatientId,
      manyObservationEdges: 38,
      zeroPatientId,
      zeroObservationEdges: 0,
    };
    const rawValues = includeAllAndPresence
      ? JSON.parse(await readFile('.artifacts/cda-builder/2026-09-26T22-25-42.310Z/related-observation-values.json', 'utf8'))
      : undefined;
    assert.equal(explorerId, targetExplorer);
    const startedAt = Date.now();
    const clicks = [];
    const timings = {};
    const results = {};
    let temporaryTableTitle;
    let temporaryTableCreated = false;
    let scenarioError;
    let cleanupError;
    const clickDOM = async (selector, label) => {
      const started = Date.now();
      clicks.push({ sequence: clicks.length + 1, label, selector, method: 'DOM click()' });
      await browserEval(browser.cdp,
        'const target=document.querySelector(' + JSON.stringify(selector) + ');' +
        'if(!target)throw new Error(' + JSON.stringify('Missing DOM target: ' + selector) + ');' +
        'target.click();return true;');
      timings[label] = Date.now() - started;
    };
    const clickButtonText = async (text, label) => {
      const started = Date.now();
      clicks.push({ sequence: clicks.length + 1, label, text, method: 'DOM button text click()' });
      await browserEval(browser.cdp,
        'const target=[...document.querySelectorAll("button")].find(button=>button.textContent?.trim()===' + JSON.stringify(text) + ');' +
        'if(!target)throw new Error(' + JSON.stringify('Missing button text: ' + text) + ');' +
        'target.click();return true;');
      timings[label] = Date.now() - started;
    };
    const setInputValue = async (selector, value, label) => {
      const started = Date.now();
      clicks.push({ sequence: clicks.length + 1, label, selector, method: 'DOM input event' });
      await browserEval(browser.cdp,
        'const input=document.querySelector(' + JSON.stringify(selector) + ');' +
        'if(!input)throw new Error(' + JSON.stringify('Missing input: ' + selector) + ');' +
        'Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,"value").set.call(input,' + JSON.stringify(value) + ');' +
        'input.dispatchEvent(new Event("input",{bubbles:true}));' +
        'input.dispatchEvent(new Event("change",{bubbles:true}));return true;');
      timings[label] = Date.now() - started;
    };
    const selectTemporaryTable = async () => {
      await waitForBrowser(browser.cdp,
        '[...document.querySelectorAll("button")].some(button=>button.innerText.trim().endsWith(' + JSON.stringify(temporaryTableTitle) + '))', 90000);
      await browserEval(browser.cdp,
        'const button=[...document.querySelectorAll("button")].find(button=>button.innerText.trim().endsWith(' + JSON.stringify(temporaryTableTitle) + '));button.click();return true;');
      await waitForBrowser(browser.cdp,
        'document.body.innerText.includes("DATASET WORKSPACE\\n\\n' + temporaryTableTitle + '")', 30000);
    };
    const previewFor = async (patientId) => {
      const previous = responses.filter(response => response.path.endsWith('/preview')).length;
      const previewStartedAt = Date.now();
      await clickButtonText('Preview', 'Preview table');
      await waitForBrowser(browser.cdp,
        'Boolean(document.querySelector(\'[data-testid="preview-table-scroll"] [role="table"]\')) && document.querySelector(\'[data-testid="preview-table-scroll"]\')?.innerText.includes(' + JSON.stringify(patientId) + ')',
        120000);
      const visible = await browserEval(browser.cdp,
        'return {headers:[...document.querySelectorAll("[data-testid=\\"preview-table-scroll\\"] [role=\\"columnheader\\"]")].map(cell=>cell.innerText),rows:[...document.querySelectorAll("[data-testid=\\"preview-table-scroll\\"] [role=\\"row\\"]")].slice(1,4).map(row=>[...row.querySelectorAll("[role=\\"cell\\"]")].map(cell=>cell.innerText))};');
      const deadline = Date.now() + 120000;
      while (responses.filter(response => response.path.endsWith('/preview')).length <= previous && Date.now() < deadline) {
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      const response = responses.filter(item => item.path.endsWith('/preview')).at(-1);
      assert(response, 'Preview request did not return');
      assert.equal(response.status, 200);
      const body = await browser.cdp.send('Network.getResponseBody', { requestId: response.requestId });
      const payload = JSON.parse(body.body);
      const row = payload.rows?.find(candidate => Object.values(candidate ?? {}).includes(patientId));
      assert(row, 'Preview did not return Patient.id ' + patientId);
      const outputName = results.relatedOutput?.name;
      const countColumn = payload.columns?.find(column =>
        column.column === outputName || column.name === outputName || column.id === results.relatedOutput?.id);
      const countKey = countColumn?.column ?? countColumn?.name ?? outputName;
      const visibleRow = visible.rows.find(cells => cells.includes(patientId));
      assert(visibleRow, 'Rendered preview did not show Patient.id ' + patientId);
      assert(visibleRow.includes(String(row[countKey])), 'Rendered preview did not show related COUNT ' + row[countKey]);
      return {
        patientId,
        rowCount: payload.rowCount,
        countKey,
        count: row[countKey],
        row,
        columns: payload.columns,
        visible,
        previewElapsedMs: Date.now() - previewStartedAt,
        responsePath: response.path,
      };
    };
    const addRelatedForm = async (form, labelPattern, contributorValue) => {
      await clickDOM('button[aria-label^="Add columns:"]', `Open related columns for ${form}`);
      await chooseRelatedSource('Observation');
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Select Observation.id"]:not(:disabled)'))`, 60000);
      await clickDOM('input[aria-label="Select Observation.id"]', `Select Observation.id for ${form}`);
      await clickButtonText('Add 1 selected feature', `Open Observation.id ${form} choices`);
      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 30000);
      await browserEval(browser.cdp,
        'const input=[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].find(input=>/Direct relationship: Patient to Observation via Subject/i.test(input.getAttribute("aria-label")??""));if(!input)throw new Error("Direct subject route missing");input.click();return true;');
      await waitForBrowser(browser.cdp,
        '[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].some(input=>(input.getAttribute("aria-label")??"").includes("matching"))', 30000);
      const formChoices = await browserEval(browser.cdp,
        'return [...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].map(input=>({label:input.getAttribute("aria-label"),disabled:input.disabled}));');
      results.formDialogs ??= [];
      results.formDialogs.push({ form, formChoices });
      const choice = formChoices.find(option => labelPattern.test(option.label ?? ''));
      assert(choice && !choice.disabled, `${form} is unavailable for Observation.id`);
      await browserEval(browser.cdp,
        'const input=[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].find(input=>input.getAttribute("aria-label")===' + JSON.stringify(choice.label) + ');input.click();return true;');
      if (contributorValue) {
        const predicateChoices = await browserEval(browser.cdp,
          'return [...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].filter(input=>input.name.startsWith("construction-condition-")).map(input=>({name:input.name,label:input.closest("label")?.innerText??"",disabled:input.disabled}));');
        results.predicateChoices = predicateChoices;
        const equals = predicateChoices.find(option => /Only records where id equals/i.test(option.label));
        assert(equals && !equals.disabled, 'Related Observation.id equality condition is unavailable');
        await browserEval(browser.cdp,
          'const input=[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].find(input=>input.name===' + JSON.stringify(equals.name) + '&&/Only records where id equals/i.test(input.closest("label")?.innerText??""));input.click();return true;');
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"] input[aria-label="id exact value"]'))`, 30000);
        await setInputValue('[role="dialog"] input[aria-label="id exact value"]', contributorValue, 'Limit related Observations to one ID');
      }
      const proposalStartedAt = Date.now();
      await browserEval(browser.cdp,
        'const button=[...document.querySelectorAll("[role=\\"dialog\\"] button")].find(button=>button.innerText.trim()==="Add 1 column"&&!button.disabled);if(!button)throw new Error("Add form button unavailable");button.click();return true;');
      await waitForBrowser(browser.cdp,
        'document.querySelector("[data-testid=\\"construction-proposal-panel\\"]")?.getAttribute("data-proposal-status")==="ready"', 180000);
      const proposalPreviewMs = Date.now() - proposalStartedAt;
      const proposalPreview = await browserEval(browser.cdp,
        'return document.querySelector("[data-testid=\\"construction-proposal-preview\\"]")?.innerText;');
      const request = proposalRequests.filter(item => item.postData).at(-1);
      assert(request, `${form} proposal request missing`);
      const proposal = JSON.parse(request.postData);
      const step = proposal.candidateConstruction?.steps?.filter(item => item.operation?.kind === 'RELATED_SOURCE').at(-1);
      assert.equal(step?.operation?.relatedSource?.form, form);
      if (contributorValue) {
        assert.equal(step.operation.relatedSource.contributorRule?.predicate?.operator, 'EQUALS');
      }
      const output = step.outputs?.find(item => item.id === step.operation.relatedSource.outputColumnId);
      assert(output, `${form} output missing from proposal`);
      await clickDOM('[data-testid="construction-apply-proposal"]', `Apply Observation.id ${form}`);
      await navigate(browser.cdp, pageURL);
      await selectTemporaryTable();
      return { output, proposalPreview, proposalPreviewMs, contributorValue };
    };
    try {
      await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('Specimen'))`, 30000);
      const baseline = await browserEval(browser.cdp,
        'return [...document.querySelectorAll("button")].filter(button=>button.innerText.trim().startsWith("▤")).map(button=>button.innerText.trim());');
      results.baselineTables = baseline;
      await clickButtonText('New table', 'Open new table picker');
      await waitForBrowser(browser.cdp,
        'Boolean(document.querySelector("button[aria-label=\\"Choose Patient rows\\"]:not(:disabled)"))', 30000);
      temporaryTableTitle = 'Patient match QA ' + Date.now();
      await setInputValue('#first-table-name', temporaryTableTitle, 'Name temporary Patient table');
      await clickDOM('button[aria-label="Choose Patient rows"]', 'Create Patient row table');
      temporaryTableCreated = true;
      await selectTemporaryTable();
      results.temporaryTableTitle = temporaryTableTitle;

      await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label^="Filter rows:"]:not(:disabled)')) && !document.body.innerText.includes('Loading the preview…')`, 120000);
      await clickDOM('button[aria-label^="Filter rows:"]', 'Open Patient ID filter');
      results.filterStart = await browserEval(browser.cdp,
        'return {text:document.body.innerText.slice(0,6500),controls:[...document.querySelectorAll("select,input,button")].filter(element=>element.offsetParent!==null).map(element=>({tag:element.tagName,label:element.getAttribute("aria-label"),text:element.innerText.slice(0,70),disabled:element.disabled,value:element.value})).filter(item=>item.label||item.text.includes("Filter")||item.text.includes("Column")).slice(0,80),editors:[...document.querySelectorAll("[data-testid]")].map(element=>element.getAttribute("data-testid")).filter(value=>value.includes("filter")||value.includes("construction"))};');
      const filterOptions = await browserEval(browser.cdp,
        'return {columns:[...document.querySelector("select[aria-label=\\"Column\\"]")?.options??[]].map(option=>({label:option.textContent.trim(),value:option.value})),conditions:[...document.querySelector("select[aria-label=\\"Condition\\"]")?.options??[]].map(option=>({label:option.textContent.trim(),value:option.value}))};');
      const idOption = filterOptions.columns.find(option => /^Patient ID\b/i.test(option.label));
      assert(idOption, 'Patient.id is not available in the row filter');
      await browserEval(browser.cdp,
        'const select=document.querySelector("select[aria-label=\\"Column\\"]");' +
        'select.value=' + JSON.stringify(idOption.value) + ';select.dispatchEvent(new Event("change",{bubbles:true}));return true;');
      const equalsOption = filterOptions.conditions.find(option => /equal/i.test(option.label));
      if (equalsOption) {
        await browserEval(browser.cdp,
          'const select=document.querySelector("select[aria-label=\\"Condition\\"]");' +
          'select.value=' + JSON.stringify(equalsOption.value) + ';select.dispatchEvent(new Event("change",{bubbles:true}));return true;');
      }
      await setInputValue('[data-testid="construction-filter-editor"] input[aria-label="Value"]', manyPatientId, 'Set many Patient ID');
      await waitForBrowser(browser.cdp,
        'document.querySelector("[data-testid=\\"construction-proposal-panel\\"]")?.getAttribute("data-proposal-status")==="ready"',
        120000);
      results.manyFilterProposal = await browserEval(browser.cdp,
        'return document.querySelector("[data-testid=\\"construction-proposal-preview\\"]")?.innerText;');
      assert(results.manyFilterProposal?.includes(manyPatientId), 'Many Patient ID is absent from filter proposal');
      await clickDOM('[data-testid="construction-apply-proposal"]', 'Apply many Patient filter');
      await waitForBrowser(browser.cdp,
        'document.querySelectorAll("[data-testid^=\\"construction-history-step-\\"]").length>=1', 60000);
      await navigate(browser.cdp, pageURL);
      await selectTemporaryTable();
      await waitForBrowser(browser.cdp,
        'document.body.innerText.includes("DATASET WORKSPACE") && document.querySelectorAll("[data-testid^=\\"construction-history-step-\\"]").length>=1',
        60000);

      await clickDOM('button[aria-label^="Add columns:"]', 'Open related columns');
      const relatedSource = await chooseRelatedSource('Observation');
      clicks.push({ sequence: clicks.length + 1, label: 'Select Observation related source', source: relatedSource.source });
      await waitForBrowser(browser.cdp,
        'Boolean(document.querySelector("input[aria-label=\\"Select Observation.id\\"]:not(:disabled)"))', 60000);
      await clickDOM('input[aria-label="Select Observation.id"]', 'Select Observation.id');
      await waitForBrowser(browser.cdp,
        '[...document.querySelectorAll("button")].some(button=>button.textContent?.trim()==="Add 1 selected feature"&&!button.disabled)',
        30000);
      await clickButtonText('Add 1 selected feature', 'Open Observation.id form choices');
      await waitForBrowser(browser.cdp, 'Boolean(document.querySelector("[role=\\"dialog\\"]"))', 30000);
      const radios = await browserEval(browser.cdp,
        'return [...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].map((input,index)=>({index,label:input.getAttribute("aria-label"),text:input.closest("label")?.innerText??input.parentElement?.innerText??"",disabled:input.disabled}));');
      results.routeDialog = await browserEval(browser.cdp,
        'const dialog=document.querySelector("[role=\\"dialog\\"]");return {text:dialog?.innerText.slice(0,6500),buttons:[...dialog?.querySelectorAll("button")??[]].map(button=>({text:button.innerText,disabled:button.disabled})),radios:[...dialog?.querySelectorAll("input[type=\\"radio\\"]")??[]].map(input=>({label:input.getAttribute("aria-label"),checked:input.checked,disabled:input.disabled}))};');
      const directSubject = radios.find(radio => /Direct relationship: Patient to Observation via Subject/i.test(radio.label ?? ''));
      assert(directSubject, 'Direct Observation subject relationship is missing');
      await browserEval(browser.cdp,
        'document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")[' + directSubject.index + '].click();return true;');
      await waitForBrowser(browser.cdp,
        '[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].some(input=>/Count matching records/i.test(input.getAttribute("aria-label")??""))', 30000);
      const formRadios = await browserEval(browser.cdp,
        'return [...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].map((input,index)=>({index,label:input.getAttribute("aria-label"),text:input.closest("label")?.innerText??"",disabled:input.disabled}));');
      const countChoice = formRadios.find(radio => /count matching records/i.test((radio.label ?? '') + ' ' + radio.text));
      assert(countChoice, 'Observation.id COUNT choice is missing');
      await browserEval(browser.cdp,
        'const input=document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")[' + countChoice.index + '];' +
        'if(!input)throw new Error("Observation.id COUNT radio is missing");input.click();return true;');
      const addChoiceButton = await browserEval(browser.cdp,
        'return [...document.querySelectorAll("[role=\\"dialog\\"] button")].map((button,index)=>({index,text:button.textContent.trim(),disabled:button.disabled})).find(button=>/^Add 1 (column|selected feature)$/i.test(button.text)&&!button.disabled)??null;');
      assert(addChoiceButton, 'Add Observation.id COUNT button is missing');
      const relatedPreviewStartedAt = Date.now();
      await browserEval(browser.cdp,
        'const button=document.querySelectorAll("[role=\\"dialog\\"] button")[' + addChoiceButton.index + '];button.click();return true;');
      await waitForBrowser(browser.cdp,
        'document.querySelector("[data-testid=\\"construction-proposal-panel\\"]")?.getAttribute("data-proposal-status")==="ready"',
        180000);
      results.relatedProposalPreviewMs = Date.now() - relatedPreviewStartedAt;
      results.relatedProposalVisible = await browserEval(browser.cdp,
        'return document.querySelector("[data-testid=\\"construction-proposal-preview\\"]")?.innerText;');
      const proposalRequest = proposalRequests.filter(request => request.postData).at(-1);
      assert(proposalRequest, 'Related source proposal request was not captured');
      const proposal = JSON.parse(proposalRequest.postData);
      const relatedStep = proposal.candidateConstruction?.steps?.find(step =>
        step.operation?.kind === 'RELATED_SOURCE' &&
        step.operation.relatedSource?.source?.resourceType === 'Observation' &&
        step.operation.relatedSource?.source?.path === 'id');
      assert(relatedStep, 'Proposal does not contain the Observation.id related source step');
      const related = relatedStep.operation.relatedSource;
      assert.equal(related.form, 'COUNT');
      assert.equal(related.route?.length, 1, 'Observation source route is not a single direct edge');
      assert(/subject/i.test(JSON.stringify(related.route)), 'Observation source route does not use subject');
      results.relatedSource = relatedSource.source;
      results.relatedRoute = related.route;
      results.relatedOutput = relatedStep.outputs?.find(output => output.id === related.outputColumnId);
      assert(results.relatedOutput, 'Related COUNT output is missing from the proposal step');
      results.relatedProposal = proposal;
      await clickDOM('[data-testid="construction-apply-proposal"]', 'Apply Observation.id COUNT');
      await navigate(browser.cdp, pageURL);
      await selectTemporaryTable();
      await waitForBrowser(browser.cdp,
        'document.body.innerText.includes("DATASET WORKSPACE") && document.querySelectorAll("[data-testid^=\\"construction-history-step-\\"]").length>=2',
        90000);
      results.many = await previewFor(manyPatientId);
      assert.equal(results.many.rowCount, 1);
      assert.equal(results.many.count, rawOracle.manyObservationEdges, 'many Patient COUNT differs from raw CDA Oracle');
      if (includeAllAndPresence) {
        results.allForm = await addRelatedForm('ALL', /Keep all matching values/i);
        results.presenceForm = await addRelatedForm('PRESENCE', /Show whether a match exists/i);
        results.manyForms = await previewFor(manyPatientId);
        const expectedIDs = rawValues.patients.find(patient => patient.patientId === manyPatientId).subjectObservations.map(observation => observation.id).sort();
        assert.deepEqual([...results.manyForms.row[results.allForm.output.name]].sort(), expectedIDs);
        assert.equal(results.manyForms.row[results.presenceForm.output.name], true);
        results.filteredForm = await addRelatedForm('COUNT', /Count matching records/i, expectedIDs[0]);
        results.manyFiltered = await previewFor(manyPatientId);
        assert.equal(results.manyFiltered.row[results.filteredForm.output.name], 1);
        assert(results.manyFiltered.visible.rows.some(cells => cells.includes('1')));
        const relatedHistoryId = await browserEval(browser.cdp,
          'return [...document.querySelectorAll("[data-testid^=\\"construction-history-step-\\"]")].at(-1)?.getAttribute("data-testid");');
        assert(relatedHistoryId, 'Saved related contributor step is missing');
        await clickDOM('[data-testid=' + JSON.stringify(relatedHistoryId) + ']', 'Select saved contributor step');
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
        await clickDOM('[data-testid^="construction-edit-step-"]', 'Edit saved contributor step');
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="related-source-step-editor"]'))`, 30000);
        results.savedContributorEditor = await browserEval(browser.cdp,
          'const editor=document.querySelector("[data-testid=\\"related-source-step-editor\\"]");return {text:editor.innerText.slice(0,4500),controls:[...editor.querySelectorAll("input,button")].filter(element=>element.offsetParent!==null).map(element=>({tag:element.tagName,label:element.getAttribute("aria-label"),text:element.innerText.slice(0,70),disabled:element.disabled,value:element.value})).slice(0,35)};');
        assert(results.savedContributorEditor.text.includes('Fields on Observation'), 'Saved editor did not open its related Observation source');
        await clickDOM('[data-testid="related-source-step-editor"] input[aria-label="Select Observation.id"]', 'Select saved Observation.id field');
        await clickButtonText('Add 1 selected feature', 'Open saved Observation.id choices');
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 30000);
        await browserEval(browser.cdp,
          'const input=[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].find(input=>/Direct relationship: Patient to Observation via Subject/i.test(input.getAttribute("aria-label")??""));if(!input)throw new Error("Saved direct subject route missing");input.click();return true;');
        await waitForBrowser(browser.cdp,
          '[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].some(input=>/Count matching records/i.test(input.getAttribute("aria-label")??""))', 30000);
        await browserEval(browser.cdp,
          'const input=[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].find(input=>/Count matching records/i.test(input.getAttribute("aria-label")??""));input.click();return true;');
        await browserEval(browser.cdp,
          'const input=[...document.querySelectorAll("[role=\\"dialog\\"] input[type=\\"radio\\"]")].find(input=>input.name.startsWith("construction-condition-")&&/Only records where id equals/i.test(input.closest("label")?.innerText??""));if(!input)throw new Error("Saved equality condition missing");input.click();return true;');
        await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"] input[aria-label="id exact value"]'))`, 30000);
        await setInputValue('[role="dialog"] input[aria-label="id exact value"]', expectedIDs[1], 'Change saved contributor ID');
        await browserEval(browser.cdp,
          'const button=[...document.querySelectorAll("[role=\\"dialog\\"] button")].find(button=>button.innerText.trim()==="Add 1 column"&&!button.disabled);if(!button)throw new Error("Save edited source choice unavailable");button.click();return true;');
        await waitForBrowser(browser.cdp,
          'document.querySelector("[data-testid=\\"construction-proposal-panel\\"]")?.getAttribute("data-proposal-status")==="ready"', 180000);
        const editedRequest = JSON.parse(proposalRequests.filter(item => item.postData).at(-1)?.postData ?? '{}');
        const editedStep = editedRequest.candidateConstruction?.steps?.filter(item => item.operation?.kind === 'RELATED_SOURCE').at(-1);
        assert.equal(editedStep?.operation?.relatedSource?.contributorRule?.predicate?.value?.string, expectedIDs[1]);
        results.editedContributorProposal = await browserEval(browser.cdp,
          'return document.querySelector("[data-testid=\\"construction-proposal-preview\\"]")?.innerText;');
        await clickDOM('[data-testid="construction-apply-proposal"]', 'Apply saved contributor edit');
        await navigate(browser.cdp, pageURL);
        await selectTemporaryTable();
        results.editedContributor = await previewFor(manyPatientId);
        assert.equal(results.editedContributor.row[results.filteredForm.output.name], 1);
        assert(results.editedContributor.visible.headers.some(header => header.toLowerCase().includes(expectedIDs[1].toLowerCase())), 'Edited contributor label was not restored');
      }

      const filterStep = await browserEval(browser.cdp,
        'return [...document.querySelectorAll("[data-testid^=\\"construction-history-step-\\"]")].map((element,index)=>({index,testId:element.getAttribute("data-testid"),text:element.innerText})).find(step=>/Filter rows|Keep rows/i.test(step.text))??null;');
      assert(filterStep, 'Saved Patient ID filter step is missing');
      await clickDOM('[data-testid=' + JSON.stringify(filterStep.testId) + ']', 'Select Patient ID filter step');
      await waitForBrowser(browser.cdp,
        'Boolean(document.querySelector("[data-testid^=\\"construction-edit-step-\\"]"))', 30000);
      await clickDOM('[data-testid^="construction-edit-step-"]', 'Edit Patient ID filter');
      await waitForBrowser(browser.cdp,
        'Boolean(document.querySelector("[data-testid=\\"construction-filter-editor\\"] input[aria-label=\\"Value\\"]:not(:disabled)"))',
        60000);
      await setInputValue('[data-testid="construction-filter-editor"] input[aria-label="Value"]', zeroPatientId, 'Set zero Patient ID');
      await waitForBrowser(browser.cdp,
        'document.querySelector("[data-testid=\\"construction-proposal-panel\\"]")?.getAttribute("data-proposal-status")==="ready"',
        120000);
      results.zeroFilterProposal = await browserEval(browser.cdp,
        'return document.querySelector("[data-testid=\\"construction-proposal-preview\\"]")?.innerText;');
      assert(results.zeroFilterProposal?.includes(zeroPatientId), 'Zero Patient ID is absent from filter proposal');
      await clickDOM('[data-testid="construction-apply-proposal"]', 'Apply zero Patient filter');
      await navigate(browser.cdp, pageURL);
      await selectTemporaryTable();
      await waitForBrowser(browser.cdp,
        'document.body.innerText.includes("DATASET WORKSPACE") && document.querySelectorAll("[data-testid^=\\"construction-history-step-\\"]").length>=2',
        90000);
      results.zero = await previewFor(zeroPatientId);
      assert.equal(results.zero.rowCount, 1);
      assert.equal(results.zero.count, rawOracle.zeroObservationEdges, 'zero Patient COUNT differs from raw CDA Oracle');
      if (includeAllAndPresence) {
        assert.deepEqual(results.zero.row[results.allForm.output.name], []);
        assert.equal(results.zero.row[results.presenceForm.output.name], false);
        assert.equal(results.zero.row[results.filteredForm.output.name], 0);
      }

      results.assertions = [
        'Created and selected a temporary Patient root through Builder DOM controls.',
        'Selected the Observation related source and the Observation.id COUNT form through Builder DOM controls.',
        'Proposal route contains exactly one direct subject edge from Patient to Observation.',
        'Applied and reloaded the related COUNT construction before preview.',
        'Filtered to the known many Patient and matched the independent raw count of 38.',
        'Edited the saved Patient.id filter to the known zero Patient and matched the independent raw count of 0.',
        'No publication action was invoked.',
      ];
      if (includeAllAndPresence) {
        results.assertions.push(
          'ALL matched every raw CDA Observation ID for the many Patient and returned an empty list for the zero Patient.',
          'PRESENCE returned true for the many Patient and false for the zero Patient.',
          'A contributor rule limited related Observation.id to one raw CDA ID and changed COUNT from 38 to 1 for the many Patient; the zero Patient remained at 0.',
        );
      }
    } catch (error) {
      scenarioError = { message: error.message, stack: error.stack };
    }
    if (temporaryTableCreated) {
      try {
        await navigate(browser.cdp, pageURL);
        await selectTemporaryTable();
        await browserEval(browser.cdp, 'window.confirm=()=>true;return true;');
        await clickDOM('button[aria-label="Delete table"]', 'Delete temporary Patient table');
        await waitForBrowser(browser.cdp,
          '![...document.querySelectorAll("button")].some(button=>button.innerText.trim().endsWith(' + JSON.stringify(temporaryTableTitle) + '))', 60000);
        await navigate(browser.cdp, pageURL);
        await waitForBrowser(browser.cdp,
          '[...document.querySelectorAll("button")].filter(button=>button.innerText.trim().startsWith("▤")).length===' + results.baselineTables.length,
          60000);
        results.cleaned = true;
        results.finalTables = await browserEval(browser.cdp,
          'return [...document.querySelectorAll("button")].filter(button=>button.innerText.trim().startsWith("▤")).map(button=>button.innerText.trim());');
        assert.deepEqual(results.finalTables, results.baselineTables);
      } catch (error) {
        cleanupError = { message: error.message, stack: error.stack };
        results.cleaned = false;
      }
    } else {
      results.cleaned = true;
      results.cleanup = 'No temporary table was created.';
    }
    results.rawOracle = rawOracle;
    results.clicks = clicks;
    results.timings = { ...timings, totalMs: Date.now() - startedAt };
    results.responses = responses;
    results.commandRequests = requests;
    results.proposalRequests = proposalRequests;
    results.pageURL = pageURL;
    results.evidenceDirectory = evidenceDirectory;
    results.error = scenarioError;
    results.cleanupError = cleanupError;
    await mkdir(evidenceDirectory, { recursive: true });
    await writeFile(join(evidenceDirectory, includeAllAndPresence ? 'patient-related-forms-many-zero.json' : 'patient-related-count-many-zero.json'), JSON.stringify(results, null, 2));
    console.log(JSON.stringify({
      evidenceDirectory,
      many: results.many && { patientId: results.many.patientId, count: results.many.count },
      zero: results.zero && { patientId: results.zero.patientId, count: results.zero.count },
      all: results.allForm && { output: results.allForm.output.name, manyValues: results.manyForms?.row[results.allForm.output.name]?.length, zeroValues: results.zero?.row[results.allForm.output.name]?.length },
      presence: results.presenceForm && { output: results.presenceForm.output.name, many: results.manyForms?.row[results.presenceForm.output.name], zero: results.zero?.row[results.presenceForm.output.name] },
      contributor: results.filteredForm && { value: results.filteredForm.contributorValue, many: results.manyFiltered?.row[results.filteredForm.output.name], zero: results.zero?.row[results.filteredForm.output.name] },
      relatedRoute: results.relatedRoute,
      clicks: results.clicks,
      timings: results.timings,
      cleaned: results.cleaned,
      error: scenarioError?.message,
      cleanupError: cleanupError?.message,
    }, null, 2));
    if (scenarioError) throw new Error(scenarioError.message);
    if (cleanupError) throw new Error(cleanupError.message);
  } else if (action === 'Inspect Observation concepts') {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
    const { source: observation, sources } = await chooseRelatedSource('Observation');
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
    const { source } = await chooseRelatedSource('Observation');
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
    const { source } = await chooseRelatedSource('Observation');
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
  } else if (action === 'Cleanup orphan Patient table') {
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('Patient'))`, 30000);
    const before = await browserEval(browser.cdp, `return [...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim().replace(/\s+/g,' '));`);
    const patientTables = before.filter(text => text.endsWith('Patient'));
    assert.equal(patientTables.length, 1, `Expected one orphan Patient table, found ${patientTables.length}`);
    await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith('Patient'));if(!button)throw new Error('Orphan Patient table button is missing');button.click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Delete table"]'))`, 30000);
    await browserEval(browser.cdp, `window.confirm=()=>true;document.querySelector('button[aria-label="Delete table"]').click();return true;`);
    await waitForBrowser(browser.cdp, `![...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('Patient'))`, 60000);
    await navigate(browser.cdp, pageURL);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE') && ![...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith('Patient'))`, 60000);
    const after = await browserEval(browser.cdp, `return [...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim().replace(/\s+/g,' '));`);
    assert(!after.some(text => text.endsWith('Patient')), 'Orphan Patient table remains after reload');
    await mkdir(evidenceDirectory,{recursive:true});
    await writeFile(join(evidenceDirectory,'orphan-patient-cleanup.json'),JSON.stringify({pageURL,before,after,responses},null,2));
    console.log(JSON.stringify({evidenceDirectory,before,after,deleteResponses:responses.filter(response=>response.path.endsWith('/commands'))},null,2));
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
