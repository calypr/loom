import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = process.argv[2] ?? 'cda-builder-full-qa-1790440983382';
const pageURL = `http://127.0.0.1:30002/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const browser = await launchBrowser('/private/tmp');
const requests = [];
const responses = [];
browser.cdp.on('Network.requestWillBeSent', (event) => {
  if (event.request.url.includes('/authoring/v2/commands') || event.request.url.includes('/authoring/v2/construction-proposals')) requests.push({ requestId: event.requestId, path: new URL(event.request.url).pathname, postData: event.request.postData });
});
browser.cdp.on('Network.responseReceived', (event) => {
  if (event.response.url.includes('/authoring/v2/')) responses.push({ requestId: event.requestId, path: new URL(event.response.url).pathname, status: event.response.status });
});
const waitForResponse = async (startIndex, pathSuffix, timeoutMs = 30000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const found = responses.slice(startIndex).find(response => response.path.endsWith(pathSuffix) && response.status === 200);
    if (found) return found;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out waiting for HTTP 200 ${pathSuffix}`);
};

try {
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
  const before = await browserEval(browser.cdp, `return {explorer:document.querySelector('select[aria-label="Explorer"]')?.value,tables:[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim()),history:document.querySelectorAll('[data-testid^="construction-history-step-"]').length};`);
  assert.equal(before.explorer, explorerId);
  assert(before.tables.includes('▤\nSpecimen'), `Specimen table missing: ${JSON.stringify(before.tables)}`);
  assert.equal(before.history, 0, 'QA specimen table should start without reshape history');

  const fields = ['subject.reference', 'collection.bodySite.reference.reference'];
  const currentColumns = await browserEval(browser.cdp, `return document.body.innerText.slice(document.body.innerText.indexOf('Select columns for an action'),document.body.innerText.indexOf('Preview and configure'));`);
  let source = null;
  let selected = null;
  let added = null;
  if (!fields.every(field => currentColumns.includes(field))) {
    await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Add columns:"]').click();return true;`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="Add columns source"]'))`, 30000);
    source = await browserEval(browser.cdp, `return [...document.querySelector('select[aria-label="Add columns source"]').options].find(option=>option.textContent.startsWith('Specimen'))?.value;`);
    assert(source, 'Specimen concept source is missing');
    await browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="Add columns source"]');select.value=${JSON.stringify(source)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
    for (const field of fields) await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Select Specimen.${field}"]:not(:disabled)`)}))`, 30000);
    for (const field of fields) await browserEval(browser.cdp, `document.querySelector(${JSON.stringify(`input[aria-label="Select Specimen.${field}"]`)}).click();return true;`);
    await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.textContent?.trim()==='Add 2 selected features'&&!button.disabled)`, 30000);
    selected = await browserEval(browser.cdp, `return {checked:[...document.querySelectorAll('input[aria-label^="Select Specimen."]')].filter(input=>input.checked).map(input=>input.getAttribute('aria-label')),button:[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Add 2 selected features')?.innerText};`);
    assert.equal(selected.checked.length, 2, 'Expected exactly two selected scalar fields');
    await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Add 2 selected features').click();return true;`);
    await new Promise(resolve => setTimeout(resolve, 1500));
    added = await browserEval(browser.cdp, `return {dialog:[...document.querySelectorAll('[role="dialog"]')].map(dialog=>dialog.innerText),tableTabs:[...document.querySelectorAll('button')].filter(button=>button.innerText.trim().startsWith('▤')).map(button=>button.innerText.trim()),alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText)};`);
  }

  const sourcePreviewStart = responses.length;
  await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview')?.click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
  const sourcePreviewResponse = await waitForResponse(sourcePreviewStart, '/preview');
  const sourcePreview = JSON.parse((await browser.cdp.send('Network.getResponseBody', { requestId:sourcePreviewResponse.requestId })).body);
  assert(sourcePreview.columns.some(column=>column.label==='subject.reference'));
  assert(sourcePreview.columns.some(column=>column.label==='collection.bodySite.reference.reference'));
  assert(sourcePreview.rows.some(row=>row['col_69ee827a3bd92ad6cb1c86c0']?.startsWith('Patient/')));
  assert(sourcePreview.rows.some(row=>row['col_d9e50113230ca5d6c7600beb']?.startsWith('BodyStructure/')));

  await browserEval(browser.cdp, `document.querySelector('button[aria-label^="Reshape:"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Turn columns into rows')))`, 30000);
  await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.startsWith('Turn columns into rows')).click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-unpivot"]'))`, 30000);
  const editor = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-reshape-unpivot"]');return {text:panel?.innerText,inputs:[...panel.querySelectorAll('input,select')].map(input=>({label:input.getAttribute('aria-label'),value:input.value,checked:input.checked,disabled:input.disabled}))};`);
  assert(editor.inputs.some(input=>input.label === 'Unpivot subject.reference'));
  assert(editor.inputs.some(input=>input.label === 'Unpivot collection.bodySite.reference.reference'));
  for (const field of fields) await browserEval(browser.cdp, `const input=document.querySelector(${JSON.stringify(`input[aria-label="Unpivot ${field}"]`)});if(!input.checked)input.click();return true;`);
  for (const [label, value] of [['Unpivot key output name','qa_unpivot_key'],['Unpivot key output label','QA unpivot key'],['Unpivot value output name','qa_unpivot_value'],['Unpivot value output label','QA unpivot value']]) await browserEval(browser.cdp, `setInput(${JSON.stringify(label)},${JSON.stringify(value)});return true;`);
  const configured = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-reshape-unpivot"]');return {text:panel?.innerText,inputs:[...panel.querySelectorAll('input,select')].map(input=>({label:input.getAttribute('aria-label'),value:input.value,checked:input.checked,disabled:input.disabled}))};`);
  await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`, 30000);
  const proposalDOM = await browserEval(browser.cdp, `const table=document.querySelector('[data-testid="construction-proposal-preview"]');return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),panel:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,previewText:table?.innerText.slice(0,2500),applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
  const proposalResponse = responses.filter(response=>response.path.endsWith('/construction-proposals')).at(-1);
  let proposalBody = null;
  if (proposalResponse) {
    try { proposalBody = JSON.parse((await browser.cdp.send('Network.getResponseBody', { requestId:proposalResponse.requestId })).body); }
    catch { /* retain DOM proposal evidence if CDP released the response body */ }
  }
  const proposal = { ...proposalDOM, response:proposalBody };
  const proposalSummary = { status:proposal.status, panel:proposal.panel, applyDisabled:proposal.applyDisabled, responseStatus:proposalResponse?.status, responseKeys:proposalBody ? Object.keys(proposalBody) : [] };
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'unpivot-proposal.json'), JSON.stringify({ pageURL, before, currentColumns, source, selected, added, sourcePreview, editor, configured, proposal, requests, responses }, null, 2));
  console.log(JSON.stringify({ evidenceDirectory, sourcePreview:{rowCount:sourcePreview.rowCount,columns:sourcePreview.columns.map(column=>column.label),firstRows:sourcePreview.rows.slice(0,3)}, configured:configured.inputs.filter(input=>input.checked||input.label?.startsWith('Unpivot key output')||input.label?.startsWith('Unpivot value output')), proposal:proposalSummary, responses:responses.filter(response => response.path.endsWith('/construction-proposals') || response.path.endsWith('/preview') || response.path.endsWith('/commands')) }, null, 2));
  assert.equal(proposal.status, 'ready', `Unpivot proposal did not become ready: ${proposal.panel}`);
  assert.equal(proposal.applyDisabled, false, 'Apply should be enabled for a ready unpivot proposal');
  await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
  const savedHistory = await browserEval(browser.cdp, `return document.querySelector('[data-testid^="construction-history-step-"]')?.innerText;`);
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 30000);
  const reloadedHistory = await browserEval(browser.cdp, `return document.querySelector('[data-testid^="construction-history-step-"]')?.innerText;`);
  const appliedPreviewStart = responses.length;
  await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview'&&!button.disabled)?.click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
  const appliedPreviewResponse = await waitForResponse(appliedPreviewStart, '/preview');
  const appliedPreview = JSON.parse((await browser.cdp.send('Network.getResponseBody', { requestId:appliedPreviewResponse.requestId })).body);
  const appliedRows = appliedPreview.rows.map(row=>({ id:row['col_17edcedbd7920c717b54b398'], key:row.qa_unpivot_key, value:row.qa_unpivot_value }));
  const proposedRows = proposalBody.preview.rows.map(row=>({ id:row['col_17edcedbd7920c717b54b398'], key:row.qa_unpivot_key, value:row.qa_unpivot_value }));
  assert.equal(appliedPreview.rowCount, proposalBody.preview.rowCount, 'Applied preview row count differs from the reviewed proposal');
  assert.deepEqual(appliedRows, proposedRows, 'Applied preview values differ from the reviewed proposal sample');
  await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-history-step-"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
  await browserEval(browser.cdp, `document.querySelector('[data-testid^="construction-edit-step-"]').click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-unpivot"]'))`, 30000);
  const restoredEditor = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-reshape-unpivot"]');return {inputs:[...panel.querySelectorAll('input,select')].map(input=>({label:input.getAttribute('aria-label'),value:input.value,checked:input.checked})),text:panel.innerText};`);
  assert(restoredEditor.inputs.some(input=>input.label==='Unpivot subject.reference'&&input.checked));
  assert(restoredEditor.inputs.some(input=>input.label==='Unpivot collection.bodySite.reference.reference'&&input.checked));
  assert(restoredEditor.inputs.some(input=>input.label==='Unpivot key output name'&&input.value==='qa_unpivot_key'));
  assert(restoredEditor.inputs.some(input=>input.label==='Unpivot value output name'&&input.value==='qa_unpivot_value'));
  const stepTestId = await browserEval(browser.cdp, `return document.querySelector('[data-testid^="construction-history-step-"]')?.getAttribute('data-testid');`);
  const stepId = stepTestId?.replace('construction-history-step-', '');
  assert(stepId, 'Saved unpivot step id is missing');
  await browserEval(browser.cdp, `document.querySelector(${JSON.stringify(`[data-testid="${stepTestId}"]`)}).click();return true;`);
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-reshape-unpivot"]')&&Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-remove-step-${stepId}"]`)}))&&!document.querySelector(${JSON.stringify(`[data-testid="construction-remove-step-${stepId}"]`)}).disabled`, 30000);
  const removeProposalStart = responses.length;
  const removeRequestStart = requests.length;
  await browserEval(browser.cdp, `document.querySelector(${JSON.stringify(`[data-testid="construction-remove-step-${stepId}"]`)}).click();return true;`);
  const removeProposalResponse = await waitForResponse(removeProposalStart, '/construction-proposals');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 30000);
  const removeProposalRequest = requests.slice(removeRequestStart).find(request=>request.path.endsWith('/construction-proposals'));
  assert(removeProposalRequest?.postData, 'Remove step should send a construction proposal request');
  assert.deepEqual(JSON.parse(removeProposalRequest.postData).removeStepIds, [stepId]);
  const removeProposal = await browserEval(browser.cdp, `return {panel:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,preview:document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0,1500),disabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled};`);
  assert.equal(removeProposal.disabled, false, `Remove proposal Apply should be enabled: ${removeProposal.panel}`);
  const removeApplyStart = responses.length;
  await browserEval(browser.cdp, `document.querySelector('[data-testid="construction-apply-proposal"]').click();return true;`);
  const removeCommandResponse = await waitForResponse(removeApplyStart, '/commands');
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`, 30000);
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`, 30000);
  const previewStart = responses.length;
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.textContent?.trim()==='Preview'&&!button.disabled)`, 30000);
  await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview'&&!button.disabled)?.click();return true;`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
  const restoredResponse = await waitForResponse(previewStart, '/preview');
  const restoredBody = JSON.parse((await browser.cdp.send('Network.getResponseBody', { requestId:restoredResponse.requestId })).body);
  const restoredRows = restoredBody.rows.map(row=>({ id:row['col_17edcedbd7920c717b54b398'], subject:row['col_69ee827a3bd92ad6cb1c86c0'], bodySite:row['col_d9e50113230ca5d6c7600beb'] }));
  const expectedRows = sourcePreview.rows.map(row=>({ id:row['col_17edcedbd7920c717b54b398'], subject:row['col_69ee827a3bd92ad6cb1c86c0'], bodySite:row['col_d9e50113230ca5d6c7600beb'] }));
  assert.deepEqual(restoredRows, expectedRows, 'Removing the unpivot should restore the original source rows exactly');
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'unpivot-end-to-end.json'), JSON.stringify({ pageURL, before, currentColumns, sourcePreview: { outputId:sourcePreview.outputId, rowCount:sourcePreview.rowCount, columns:sourcePreview.columns, rows:expectedRows }, editor, configured, proposal, savedHistory, reloadedHistory, appliedPreview: { rowCount:appliedPreview.rowCount, columns:appliedPreview.columns, rows:appliedRows }, restoredEditor, removeProposalRequest:JSON.parse(removeProposalRequest.postData), removeProposal, removeProposalStatus:removeProposalResponse.status, removeApplyStatus:removeCommandResponse.status, restored: { outputId:restoredBody.outputId, rowCount:restoredBody.rowCount, columns:restoredBody.columns, rows:restoredRows }, requests, responses }, null, 2));
  console.log(JSON.stringify({ evidenceDirectory, sourcePreview:{rowCount:sourcePreview.rowCount,columns:sourcePreview.columns.map(column=>column.label),firstRows:expectedRows.slice(0,3)},proposal:proposalSummary,savedHistory,reloadedHistory,appliedPreview:{rowCount:appliedPreview.rowCount,rows:appliedRows.slice(0,3)},restoredEditor:restoredEditor.inputs.filter(input=>input.checked||input.label?.startsWith('Unpivot key output')||input.label?.startsWith('Unpivot value output')),removeProposal,removeProposalStatus:removeProposalResponse.status,removeApplyStatus:removeCommandResponse.status,restored:{rowCount:restoredBody.rowCount,columns:restoredBody.columns.map(column=>column.label),firstRows:restoredRows.slice(0,3)},publishRequests:requests.filter(request=>request.path.endsWith('/publish'))}, null, 2));
  assert.equal(restoredBody.rowCount, sourcePreview.rowCount, 'Restored preview row count differs from source');
  assert.deepEqual(requests.filter(request=>request.path.endsWith('/publish')), [], 'Focused unpivot verification must not publish');
} finally {
  await browser.close();
}
