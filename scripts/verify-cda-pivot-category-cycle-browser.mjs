import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const project = 'loom_dev_cda_fhir';
const explorer = `pivot-category-cycle-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-pivot-category-cycle-browser-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const relatedApplyOnly = process.env.LOOM_RELATED_APPLY_ONLY === '1';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = {
  explorer,
  ...(relatedApplyOnly ? { mode: 'relatedApplyOnly' } : {}),
  cases: [],
  errors: [],
  requests: [],
  authoringRequests: [],
  started: new Date().toISOString(),
};
await mkdir(evidence, { recursive: true });
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(fileURLToPath(new URL('..', import.meta.url)));
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };
let browser, builder, outputId;
let editedPivotPresentation=false;
const pendingResponseReads=new Set();
const networkRequests=new Map();
const failedRequests=new Map();
const parseNetworkJSON = value => {
  try { return JSON.parse(value); } catch { return value; }
};
const correlateAuthoringRequests = () => {
  for (const reconcile of report.authoringRequests.filter(request => request.endpoint === 'reconcile' && request.responseReceiptId)) {
    const command = [...report.authoringRequests].reverse().find(request =>
      request.endpoint === 'commands' &&
      request.responseDraftVersion === reconcile.requestDraftVersion &&
      request.responseDraftDigest === reconcile.requestDraftDigest &&
      request.requestStartedAtMs <= reconcile.requestStartedAtMs,
    );
    if (command) reconcile.commandRequestId = command.requestId;
    for (const preview of report.authoringRequests.filter(request =>
      request.endpoint === 'preview' && request.requestReceiptId === reconcile.responseReceiptId,
    )) {
      preview.reconcileRequestId = reconcile.requestId;
      preview.reconciledDraftVersion = reconcile.requestDraftVersion;
      preview.reconciledDraftDigest = reconcile.requestDraftDigest;
      if (reconcile.commandRequestId) preview.commandRequestId = reconcile.commandRequestId;
    }
  }
};
const summarizeAuthoringRequest = request => request ? ({
  requestId: request.requestId,
  status: request.status,
  requestDraftVersion: request.requestDraftVersion,
  requestDraftDigest: request.requestDraftDigest,
  responseDraftVersion: request.responseDraftVersion,
  responseDraftDigest: request.responseDraftDigest,
  requestOutputId: request.requestOutputId,
  responseOutputId: request.responseOutputId,
  requestReceiptId: request.requestReceiptId,
  responseReceiptId: request.responseReceiptId,
  responseRowCount: request.responseRowCount,
  durationMs: request.durationMs,
}) : undefined;
const relatedApplyRequestTrace = () => {
  const applies = report.authoringRequests.filter(request =>
    request.endpoint === 'commands' &&
    request.body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL'),
  );
  const secondApply = applies[1];
  const secondReconcile = secondApply
    ? report.authoringRequests.find(request =>
        request.endpoint === 'reconcile' && request.commandRequestId === secondApply.requestId,
      )
    : undefined;
  const secondPreview = secondReconcile
    ? report.authoringRequests.find(request =>
        request.endpoint === 'preview' && request.reconcileRequestId === secondReconcile.requestId,
      )
    : undefined;
  return {
    proposalApplies: applies.map(summarizeAuthoringRequest),
    secondReconcile: summarizeAuthoringRequest(secondReconcile),
    secondPreview: summarizeAuthoringRequest(secondPreview),
  };
};
const drainPendingResponseReads = async () => {
  while (pendingResponseReads.size > 0) {
    await Promise.all([...pendingResponseReads]);
  }
};
const captureFailureDOM = () => browserEval(browser.cdp, `return (() => {
  const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
  const preview = document.querySelector('[data-testid="preview-table-scroll"]');
  const previewPanel = document.querySelector('[data-testid="construction-preview"]');
  const table = preview?.querySelector('[role="table"]');
  const rows = selector => [...document.querySelectorAll(selector)].slice(1).map(row =>
    [...row.querySelectorAll('[role="cell"], td')].map(cell => cell.innerText.trim()),
  );
  return {
    capturedAt: new Date().toISOString(),
    proposal: proposal ? {
      status: proposal.dataset.proposalStatus,
      proposalId: proposal.dataset.proposalId,
      text: proposal.innerText,
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row =>
        [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()),
      ),
    } : undefined,
    savedPreview: preview ? {
      visible: preview.getClientRects().length > 0,
      status: previewPanel?.getAttribute('data-preview-status'),
      receiptId: previewPanel?.getAttribute('data-preview-receipt-id'),
      outputId: previewPanel?.getAttribute('data-preview-output-id'),
      currentDraftVersion: previewPanel?.getAttribute('data-current-draft-version'),
      currentDraftDigest: previewPanel?.getAttribute('data-current-draft-digest'),
      ariaRowCount: table?.getAttribute('aria-rowcount'),
      headers: [...preview.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
      rows: rows('[data-testid="preview-table-scroll"] [role="row"]'),
      loading: document.body.innerText.includes('Loading your table…'),
    } : undefined,
    bodyText: document.body.innerText,
  };
})();`);
const captureRelatedApplyDOM = () => browserEval(browser.cdp, `return (async () => {
  const previewPanel = document.querySelector('[data-testid="construction-preview"]');
  const preview = document.querySelector('[data-testid="preview-table-scroll"]');
  const table = preview?.querySelector('[role="table"]');
  if (!preview || !table || !previewPanel) return undefined;
  const totalRows = Math.max(0, Number(table.getAttribute('aria-rowcount')) - 1);
  const rows = new Map();
  for (let page = 0; page < 100 && rows.size < totalRows; page++) {
    for (const row of table.querySelectorAll('[role="row"]')) {
      const firstCell = row.firstElementChild;
      const rowNumber = Number(firstCell?.textContent?.trim());
      if (!Number.isInteger(rowNumber) || rowNumber < 1) continue;
      rows.set(rowNumber, {
        rowNumber,
        rowIdentityLabel: row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label'),
        values: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()),
      });
    }
    if (rows.size >= totalRows) break;
    const maxTop = Math.max(0, preview.scrollHeight - preview.clientHeight);
    const nextTop = Math.min(preview.scrollTop + Math.max(1, preview.clientHeight / 2), maxTop);
    if (nextTop === preview.scrollTop) break;
    preview.scrollTop = nextTop;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  }
  const renderedRows = [...rows.values()].sort((left, right) => left.rowNumber - right.rowNumber);
  preview.scrollTop = 0;
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  return {
    selectedTableTestId: document.querySelector('[data-testid^="construction-table-"][aria-current="page"]')?.getAttribute('data-testid'),
    preview: {
      status: previewPanel.getAttribute('data-preview-status'),
      receiptId: previewPanel.getAttribute('data-preview-receipt-id'),
      outputId: previewPanel.getAttribute('data-preview-output-id'),
      currentDraftVersion: previewPanel.getAttribute('data-current-draft-version'),
      currentDraftDigest: previewPanel.getAttribute('data-current-draft-digest'),
      ariaRowCount: table.getAttribute('aria-rowcount'),
      loading: document.body.innerText.includes('Loading your table…'),
      totalRows,
      rows: renderedRows,
    },
  };
})();`);
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `pivot-category-cycle-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const proposal = async (name, start, expectedRows) => {
  await waitForBrowser(browser.cdp, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
  const result = await browserEval(browser.cdp, `const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};`);
  assert.equal(result.status, 'ready', result.text);
  assert.equal(result.rows.length, Math.min(25, expectedRows.length));
  const permitted = new Set(expectedRows.map(row=>JSON.stringify(row)));
  for (const row of result.rows) assert(permitted.has(JSON.stringify(row)), 'Preview row must match an independent CDA relationship witness: '+JSON.stringify(row));
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const apply = async expectedRows => {
  const start = Date.now();
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async expectedRows => {
  const start = Date.now();
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await rendered(expectedRows);
  recordRender('load-to-render', start);
};
const rendered = async expectedRows => {
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, expectedRows.length) + 1))} && !document.body.innerText.includes('Loading your table…')`);
  const rows = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);`);
  assert(rows.length > 0 || expectedRows.length === 0);
  const savedRows = editedPivotPresentation ? expectedRows.map(row=>[row[1],row[0]]) : expectedRows;
  if(editedPivotPresentation){
    const headers=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(e=>e.innerText.trim());`);
    assert(headers.some(h=>h.toLowerCase().includes(report.oracle.source.id)),JSON.stringify(headers));
    assert(headers.some(h=>h.includes('OBSERVATION FHIR RESOURCE ID')),JSON.stringify(headers));
  }
  for (const row of rows) assert(savedRows.some(expected=>row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
try {
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.id);
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Related Pivot composition QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Pivot QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: source.generation, resourceType: 'Specimen', id: source.id }] } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.exceptionThrown', e => report.errors.push({kind:'runtime',details:e.exceptionDetails}));
  browser.cdp.on('Runtime.consoleAPICalled', e => {if(e.type==='error')report.errors.push({kind:'console',args:e.args});});
  browser.cdp.on('Network.requestWillBeSent',({requestId,request,timestamp,wallTime})=>{
    if(request.url.includes('/related-expand-choices'))networkRequests.set(requestId,request.postData);
    const endpoint=['commands','reconcile','preview'].find(path=>new URL(request.url).pathname.endsWith(`/authoring/v2/${path}`));
    if(endpoint){
      const body=request.postData?parseNetworkJSON(request.postData):undefined;
      const captured={endpoint,requestId,url:request.url,method:request.method,requestStartedAtMs:timestamp*1000,wallTimeMs:wallTime*1000,observedAfter:report.cases.at(-1)?.name,body,
        requestDraftVersion:body?.expectedDraftVersion??body?.draftVersion,requestDraftDigest:body?.expectedDraftDigest??body?.draftDigest,
        requestOutputId:body?.outputId,requestReceiptId:body?.receiptId};
      report.authoringRequests.push(captured);
      if(endpoint==='commands'){report.browserCommands??=[];report.browserCommands.push(captured);}
      networkRequests.set(requestId,captured);
    }
  });
  browser.cdp.on('Network.loadingFinished',({requestId,timestamp})=>{
    const captured=networkRequests.get(requestId);
    const error=failedRequests.get(requestId);
    if((captured&&typeof captured==='object')||error){
      const responseRead=browser.cdp.send('Network.getResponseBody',{requestId}).then(value=>{
        const raw=value.isBase64Encoded?Buffer.from(value.body,'base64').toString('utf8'):value.body;
        const parsed=parseNetworkJSON(raw);
        if(captured&&typeof captured==='object'){
          captured.response=parsed;
          captured.responseFinishedAtMs=timestamp*1000;
          captured.durationMs=captured.responseFinishedAtMs-captured.requestStartedAtMs;
          captured.responseDraftVersion=parsed?.draftVersion;
          captured.responseDraftDigest=parsed?.draftDigest;
          captured.responseReceiptId=parsed?.receiptId;
          captured.responseOutputId=parsed?.outputId;
          captured.responseRowCount=parsed?.rowCount??(Array.isArray(parsed?.rows)?parsed.rows.length:undefined);
          correlateAuthoringRequests();
        }
        if(error)error.body=raw;
      }).catch(e=>{
        if(captured&&typeof captured==='object')captured.responseBodyError=String(e);
        if(error)error.bodyError=String(e);
      }).finally(()=>pendingResponseReads.delete(responseRead));
      pendingResponseReads.add(responseRead);
    }
  });
  browser.cdp.on('Network.responseReceived', ({response,requestId,timestamp})=>{
    const captured=networkRequests.get(requestId);
    if(captured && typeof captured==='object'){
      captured.status=response.status;
      captured.responseStartedAtMs=timestamp*1000;
    }
    if(response.status>=400&&!response.url.endsWith('/favicon.ico')){
      const error={kind:'http',requestId,url:response.url,status:response.status,observedAfter:report.cases.at(-1)?.name,request:networkRequests.get(requestId)};
      report.errors.push(error);
      failedRequests.set(requestId,error);
    }
  });
  browser.cdp.on('Network.loadingFailed', e=>{
    const captured=networkRequests.get(e.requestId);
    if(captured&&typeof captured==='object')captured.loadingFailure={errorText:e.errorText,canceled:e.canceled};
    if(e.type==='Script'&&e.errorText!=='net::ERR_ABORTED')report.errors.push({kind:'module',error:e.errorText});
  });
  const rawQuery = query => {
    const r=spawnSync('rtk',['proxy','docker','exec',process.env.LOOM_ARANGO_CONTAINER??'loom-dev-6d7df93d6a37-arangodb-1','arangosh','--server.database','loom_dev','--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
    assert.equal(r.status,0,r.stderr);return JSON.parse(r.stdout.slice(r.stdout.indexOf('[')));
  };
  let witnesses=[{anchor:source._id,values:[source.id]}];
  let expected=witnesses.map(w=>w.values);
  await open(expected);
  const chain=[
    {from:'Specimen',to:'Patient',label:'subject_Patient',field:'subject',direction:'OUTBOUND'},
    {from:'Patient',to:'Observation',label:'subject_Patient',field:'subject',direction:'INBOUND'},
  ];
  for(const hop of chain){
    const next=[];
    for(const witness of witnesses){
      const endpoint=hop.direction==='OUTBOUND'?'_from':'_to';
      const target=hop.direction==='OUTBOUND'?'_to':'_from';
      const query=`FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to+'/')}) LET d=DOCUMENT(e.${target}) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`;
      const matches=witness.anchor?rawQuery(query):[];
      if(matches.length)for(const match of matches)next.push({anchor:match._id,values:[...witness.values,match.id]});
      else next.push({anchor:null,values:[...witness.values,'—']});
    }
    assert(next.length<=1000,'Use a bounded CDA chain fixture');
    witnesses=next;expected=witnesses.map(w=>w.values);
    report.oracle.chain??=[];report.oracle.chain.push({hop,witnesses});
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false`);
    await click(browser.cdp,'[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForBrowser(browser.cdp,`document.querySelector('${panel} select[aria-label="Related record type"]')?.disabled===false`);
    let start=Date.now();
    await selectOption(browser.cdp,panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForBrowser(browser.cdp,`document.querySelector(${JSON.stringify(panel+' input[aria-label="'+label+'"]')})`,5000);
    await click(browser.cdp,panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  if (relatedApplyOnly) {
    await drainPendingResponseReads();
    correlateAuthoringRequests();
    const savedDocument = doc(builder);
    assert(savedDocument, `Saved output ${outputId} must still exist after the second related expansion.`);
    const savedSteps = savedDocument.construction?.steps ?? [];
    assert.deepEqual(
      savedSteps.map(step => step.operation.kind),
      ['RELATED_EXPAND', 'RELATED_EXPAND'],
      'The saved output must contain exactly the two related expansion steps.',
    );
    const requestTrace = relatedApplyRequestTrace();
    assert.equal(requestTrace.proposalApplies.length, 2, 'Both related expansion proposals must be applied exactly once.');
    const [firstApply, secondApply] = requestTrace.proposalApplies;
    assert.equal(secondApply.status, 200, 'The second related expansion command must be accepted.');
    assert.equal(secondApply.requestDraftVersion, firstApply.responseDraftVersion, 'The second Apply must use the first Apply draft version.');
    assert.equal(secondApply.responseDraftVersion, builder.draftVersion, 'The saved builder must reflect the second Apply version.');
    assert.equal(requestTrace.secondReconcile?.status, 200, 'The second Apply draft must reconcile successfully.');
    assert.equal(requestTrace.secondReconcile?.requestDraftVersion, secondApply.responseDraftVersion, 'Reconciliation must target the second Apply draft.');
    assert.equal(requestTrace.secondPreview?.status, 200, 'The second Apply receipt must preview successfully.');
    assert.equal(requestTrace.secondPreview?.requestOutputId, outputId, 'The second preview must target the created output.');
    assert.equal(requestTrace.secondPreview?.requestReceiptId, requestTrace.secondReconcile?.responseReceiptId, 'The second preview must use the second Apply receipt.');
    assert.equal(requestTrace.secondPreview?.responseRowCount, Math.min(25, expected.length), 'The second preview must return the expected bounded row count.');
    const dom = await captureRelatedApplyDOM();
    assert.equal(dom.selectedTableTestId, `construction-table-${outputId}`, 'The created output must remain selected after the second Apply.');
    assert.equal(dom.preview?.status, 'ready', 'The saved preview must be ready after the second Apply.');
    assert.equal(dom.preview?.outputId, outputId, 'The visible preview must belong to the created output.');
    assert.equal(dom.preview?.receiptId, requestTrace.secondReconcile?.responseReceiptId, 'The visible preview must show the reconciled receipt.');
    assert.equal(dom.preview?.loading, false, 'The saved preview must finish loading after the second Apply.');
    assert.equal(Number(dom.preview?.ariaRowCount) - 1, Math.min(25, expected.length), 'The visible table must have the expected bounded row count.');
    assert.equal(dom.preview?.rows.length, Math.min(25, expected.length), 'Scrolling the virtualized table must render its expected bounded rows.');
    assert.deepEqual(
      dom.preview?.rows.map(row => row.rowNumber),
      Array.from({ length: Math.min(25, expected.length) }, (_, index) => index + 1),
      'Every rendered row ordinal must be collected once without collapsing repeated values.',
    );
    assert.deepEqual(
      dom.preview?.rows.map(row => row.rowIdentityLabel),
      Array.from({ length: Math.min(25, expected.length) }, (_, index) => `Inspect row ${index + 1} identity`),
      'Every rendered row must retain its native identity inspector.',
    );
    const expectedRows = new Set(expected.map(row => JSON.stringify(row)));
    for (const row of dom.preview?.rows ?? []) {
      assert(expectedRows.has(JSON.stringify(row.values)), `Saved row must match an independent CDA witness: ${JSON.stringify(row.values)}`);
    }
    report.relatedApplyOnly = {
      outputId,
      savedDraftVersion: builder.draftVersion,
      savedDraftDigest: builder.draftDigest,
      savedSteps: savedSteps.map(step => ({ id: step.id, kind: step.operation.kind })),
      expectedRowCount: expected.length,
      displayedRowLimit: Math.min(25, expected.length),
      requestTrace,
      dom,
    };
    const savedAfterSecondApply = structuredClone(builder);
    await open(expected);
    builder = await api(base + '/builder');
    assert.equal(builder.draftVersion, savedAfterSecondApply.draftVersion, 'Reload must preserve the accepted second Apply draft version.');
    assert.equal(builder.draftDigest, savedAfterSecondApply.draftDigest, 'Reload must preserve the accepted second Apply draft digest.');
    assert.deepEqual(builder.workspace, savedAfterSecondApply.workspace, 'Reload must preserve both expansions and the exact selected source membership.');
    const reloaded = await captureRelatedApplyDOM();
    assert.equal(reloaded.preview?.status, 'ready');
    assert.equal(reloaded.preview?.outputId, outputId);
    assert.equal(reloaded.preview?.loading, false);
    assert.equal(reloaded.preview?.rows.length, Math.min(25, expected.length));
    for (const row of reloaded.preview?.rows ?? []) {
      assert(expectedRows.has(JSON.stringify(row.values)), `Reloaded row must match an independent CDA witness: ${JSON.stringify(row.values)}`);
    }
    report.relatedApplyOnly.reloaded = reloaded;
  } else {
  const expanded=builder;
  assert(expected.length>1,'Require multiple related category records');
  const categoryIds=[...new Set(expected.map(row=>row[2]))];
  assert.equal(categoryIds.length,expected.length,'Each Observation category must occur once');
  const patient=expected[0][1];
  assert(expected.every(row=>row[1]===patient));
  const pivoted=[[source.id,...categoryIds.map(()=>patient)]];
  let start;
  const configurePivot=async()=>{
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await click(browser.cdp,'[data-testid="construction-action-pivot-rows"]');
    await waitForBrowser(browser.cdp,`document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')`);
    const chooseField=async(label,prefix)=>{
      const options=await browserEval(browser.cdp,`return [...document.querySelector('select[aria-label="${label}"]').options].map(o=>({value:o.value,label:o.textContent}));`);
      const field=options.find(o=>o.label.startsWith(prefix));assert(field,JSON.stringify(options));
      await selectOption(browser.cdp,`select[aria-label="${label}"]`,field.value);
    };
    await click(browser.cdp,'input[aria-label="Pivot group Specimen ID"]');
    await chooseField('Pivot category field','Observation FHIR resource ID');
    start=Date.now();
    await chooseField('Pivot values field','Patient FHIR resource ID');
  };
  await open(expected);
  await configurePivot();
  await proposal('related-pivot-preview',start,pivoted);
  await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base+'/builder')).workspace,expanded.workspace);
  await configurePivot();
  await proposal('confirmed-related-pivot-preview',start,pivoted);
  await apply(pivoted);
  await open(pivoted);
  const pivot=doc(builder).construction.steps.find(s=>s.operation.kind==='PIVOT');assert(pivot);
  assert.deepEqual(new Set(pivot.operation.pivot.categories.map(c=>c.key.string)),new Set(categoryIds),'Persisted Pivot categories must match raw CDA Observation IDs');
  await click(browser.cdp,`[data-testid="construction-history-step-${pivot.id}"]`);
  await click(browser.cdp,`[data-testid="construction-edit-step-${pivot.id}"]`);
  await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-reshape-pivot-advanced"] summary')`);
  await click(browser.cdp,'[data-testid="construction-reshape-pivot-advanced"] summary');
  start=Date.now();
  await selectOption(browser.cdp,'select[aria-label="Pivot missing cell policy"]','ERROR');
  await proposal('edit-pivot-missing-policy-preview',start,pivoted);
  await apply(pivoted);
  await open(pivoted);
  assert.equal(doc(builder).construction.steps.find(s=>s.id===pivot.id).operation.pivot.missingCellPolicy,'ERROR');
  const beforeFilter=builder;
  const filterPanel='[data-testid="construction-filter-editor"]';
  const configureMissing=async()=>{
    await click(browser.cdp,'[data-testid="construction-action-keep-rows"]');
    await waitForBrowser(browser.cdp,`document.querySelector('${filterPanel} select[aria-label="Condition"]:not(:disabled)')`);
    const options=await browserEval(browser.cdp,`return [...document.querySelector('${filterPanel} select[aria-label="Column"]').options].map(o=>({value:o.value,label:o.textContent}));`);
    const category=options.find(o=>o.label.startsWith(categoryIds[0]));
    assert(category,'Pivot-generated category must be filterable: '+JSON.stringify(options));
    await selectOption(browser.cdp,filterPanel+' select[aria-label="Column"]',category.value);
    await selectOption(browser.cdp,filterPanel+' select[aria-label="Condition"]','EQUALS');
    start=Date.now();
    await selectOption(browser.cdp,filterPanel+' select[aria-label="Condition"]','MISSING');
  };
  await configureMissing();
  await proposal('pivot-category-missing-preview',start,[]);
  await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeFilter.workspace);
  await configureMissing();
  await proposal('confirmed-pivot-category-missing-preview',start,[]);
  await apply([]);
  await open([]);
  const filter=doc(builder).construction.steps.find(s=>s.operation.kind==='FILTER');assert(filter);
  await click(browser.cdp,`[data-testid="construction-history-step-${filter.id}"]`);
  await click(browser.cdp,`[data-testid="construction-edit-step-${filter.id}"]`);
  await waitForBrowser(browser.cdp,`document.querySelector('${filterPanel} select[aria-label="Condition"]:not(:disabled)')`);
  await selectOption(browser.cdp,filterPanel+' select[aria-label="Condition"]','EQUALS');
  await click(browser.cdp,filterPanel+' input[aria-label="Value"]');
  start=Date.now();
  await browser.cdp.send('Input.insertText',{text:patient});
  await proposal('pivot-category-equality-preview',start,pivoted);
  await apply(pivoted);
  await open(pivoted);
  const beforeCategoryEdit=builder;
  const editCategories=async()=>{
  await click(browser.cdp,`[data-testid="construction-history-step-${pivot.id}"]`);
  await click(browser.cdp,`[data-testid="construction-edit-step-${pivot.id}"]`);
  await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Pivot group Specimen ID"]:not(:disabled)')`);
  await click(browser.cdp,'input[aria-label="Pivot group Specimen ID"]');
  const categoryOptions=await browserEval(browser.cdp,`return [...document.querySelector('select[aria-label="Pivot category field"]').options].map(o=>({value:o.value,label:o.textContent}));`);
  const specimenCategory=categoryOptions.find(o=>o.label==='Specimen ID');assert(specimenCategory,JSON.stringify(categoryOptions));
  await selectOption(browser.cdp,'select[aria-label="Pivot category field"]',specimenCategory.value);
  await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Pivot group Observation FHIR resource ID"]:not(:disabled)')`);
  start=Date.now();
  await click(browser.cdp,'input[aria-label="Pivot group Observation FHIR resource ID"]');
  };
  await editCategories();
  const changed=expected.map(row=>[row[2],row[1]]);
  await proposal('pivot-category-rediscovery-preview',start,changed);
  report.categoryEditWarning=await browserEval(browser.cdp,`return document.querySelector('[data-testid="construction-proposal-panel"]').innerText;`);
  await click(browser.cdp,'input[aria-label="Pivot group Observation FHIR resource ID"]');
  const originalOptions=await browserEval(browser.cdp,`return [...document.querySelector('select[aria-label="Pivot category field"]').options].map(o=>({value:o.value,label:o.textContent}));`);
  const originalCategory=originalOptions.find(o=>o.label==='Observation FHIR resource ID');assert(originalCategory,JSON.stringify(originalOptions));
  await selectOption(browser.cdp,'select[aria-label="Pivot category field"]',originalCategory.value);
  start=Date.now();
  await click(browser.cdp,'input[aria-label="Pivot group Specimen ID"]');
  await proposal('return-to-original-pivot-category-preview',start,pivoted);
  const restoredWarning=await browserEval(browser.cdp,`return document.querySelector('[data-testid="construction-proposal-panel"]').innerText;`);
  report.restoredWarning=restoredWarning;
  assert(!restoredWarning.includes('dependent step'),'Returning to the original fields must retain the dependent filter: '+restoredWarning);
  await apply(pivoted);
  await open(pivoted);
  assert.deepEqual(doc(builder).construction,doc(beforeCategoryEdit).construction,'A field round trip must preserve authored column IDs and downstream filter');

  }
  await drainPendingResponseReads();
  correlateAuthoringRequests();
  assert.deepEqual(report.errors,[]);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  await drainPendingResponseReads();
  report.savedBuilderAtFailure=await api(base + '/builder').catch(error=>({readError:String(error)}));
  report.failureDOM = browser ? await captureFailureDOM().catch(error=>({captureError:String(error)})) : undefined;
  report.failureUI = report.failureDOM?.bodyText;
  await drainPendingResponseReads();
  correlateAuthoringRequests();
  report.pendingAuthoringRequests=report.authoringRequests.filter(request=>request.responseFinishedAtMs===undefined&&!request.loadingFailure).map(request=>({endpoint:request.endpoint,requestId:request.requestId,method:request.method,url:request.url,requestStartedAtMs:request.requestStartedAtMs,status:request.status,requestDraftVersion:request.requestDraftVersion,requestDraftDigest:request.requestDraftDigest,requestOutputId:request.requestOutputId,requestReceiptId:request.requestReceiptId}));
  if (relatedApplyOnly) {
    const failedDocument = report.savedBuilderAtFailure?.workspace?.documents?.find(document => document.output.id === outputId);
    report.relatedApplyOnly = {
      outputId,
      savedDraftVersion: report.savedBuilderAtFailure?.draftVersion,
      savedDraftDigest: report.savedBuilderAtFailure?.draftDigest,
      savedSteps: failedDocument?.construction?.steps?.map(step => ({ id: step.id, kind: step.operation.kind })),
      expectedRowCount: report.oracle?.chain?.at(-1)?.witnesses?.length,
      displayedRowLimit: Math.min(25, report.oracle?.chain?.at(-1)?.witnesses?.length ?? 0),
      requestTrace: relatedApplyRequestTrace(),
      dom: report.failureDOM,
    };
  }
} finally {
  await drainPendingResponseReads();
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      ...(await sourceFreeze.assertUnchanged()),
      finishedAt: sourceFreezeFinishedAt,
    };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      error: String(error),
      finishedAt: sourceFreezeFinishedAt,
    };
    process.exitCode = 1;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(c => ({ name: c.name, durationMs: c.durationMs })), error: report.error }));
